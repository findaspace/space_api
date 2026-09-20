package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var ErrNotFound = errors.New("objectstore: object not found")

type Config struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
	PublicBaseURL  string
	PresignTTL     time.Duration
}

type Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	cfg     Config
}

func New(ctx context.Context, c Config) (*Store, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(c.Region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""),
		),
		// Without this the SDK computes a CRC32 over an empty body and signs it
		// into every presigned PUT, where it can never match what is actually
		// uploaded. R2, Garage and SeaweedFS reject it outright.
		config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		config.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
		o.UsePathStyle = c.ForcePathStyle
	})

	return &Store{
		client:  client,
		presign: s3.NewPresignClient(client),
		cfg:     c,
	}, nil
}

// PresignPut returns a URL the client PUTs to directly. Content-Type is signed,
// so the client cannot upload an executable claiming to be a JPEG.
func (s *Store) PresignPut(ctx context.Context, key, contentType string) (string, time.Time, error) {
	out, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(s.cfg.PresignTTL))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign put: %w", err)
	}
	return out.URL, time.Now().Add(s.cfg.PresignTTL), nil
}

type ObjectInfo struct {
	Size        int64
	ContentType string
}

func (s *Store) Head(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, fmt.Errorf("head object: %w", err)
	}

	info := ObjectInfo{}
	if out.ContentLength != nil {
		info.Size = *out.ContentLength
	}
	if out.ContentType != nil {
		info.ContentType = *out.ContentType
	}
	return info, nil
}

// Get returns the object body. The caller closes it.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get object: %w", err)
	}
	return out.Body, nil
}

func (s *Store) Put(ctx context.Context, key, contentType string, body []byte, cacheControl string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.cfg.Bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		CacheControl:  aws.String(cacheControl),
		Body:          strings.NewReader(string(body)),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// PublicURL is how clients fetch the image. Listing photos are public and
// cacheable, so they are not served through expiring presigned GETs: a URL that
// expires cannot sit in a CDN.
func (s *Store) PublicURL(key string) string {
	return strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/" + key
}

// EnsureBucket creates the bucket and opens it for anonymous reads. Development
// only: in production the bucket and its policy are infrastructure, not
// something the application grants itself at boot.
func (s *Store) EnsureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.cfg.Bucket)})
	if err == nil {
		return nil
	}

	if _, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s.cfg.Bucket),
	}); err != nil {
		var owned *types.BucketAlreadyOwnedByYou
		if !errors.As(err, &owned) {
			return fmt.Errorf("create bucket: %w", err)
		}
	}

	policy := fmt.Sprintf(`{
		"Version":"2012-10-17",
		"Statement":[{
			"Effect":"Allow",
			"Principal":"*",
			"Action":["s3:GetObject"],
			"Resource":["arn:aws:s3:::%s/*"]
		}]
	}`, s.cfg.Bucket)

	if _, err := s.client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(s.cfg.Bucket),
		Policy: aws.String(policy),
	}); err != nil {
		return fmt.Errorf("put bucket policy: %w", err)
	}
	return nil
}

func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	var nf *types.NotFound
	if errors.As(err, &nsk) || errors.As(err, &nf) {
		return true
	}
	// HeadObject returns a bare 404 with no typed error on some backends.
	var re interface{ HTTPStatusCode() int }
	if errors.As(err, &re) {
		return re.HTTPStatusCode() == http.StatusNotFound
	}
	return false
}
