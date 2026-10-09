// verify checks the configured providers without printing credentials.
// DB is read-only; storage creates and removes one unique object; SMS sends
// exactly one message only when selected explicitly with a recipient.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/platform/objectstore"
	"github.com/samnart1/space_api/internal/platform/postgres"
	"github.com/samnart1/space_api/internal/platform/sms"
)

func main() {
	checks := flag.String("checks", "db,storage", "Comma-separated checks: db,storage,sms")
	flag.Parse()
	results := []map[string]string{}
	failed := false
	for _, name := range strings.Split(*checks, ",") {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		var err error
		switch name {
		case "db":
			err = database(ctx)
		case "storage":
			err = storage(ctx)
		case "sms":
			err = sendSMS(ctx)
		default:
			err = errors.New("unknown check")
		}
		cancel()
		item := map[string]string{"check": name, "status": "passed"}
		if err != nil {
			failed = true
			item["status"] = "failed"
			item["detail"] = safeError(err)
		} else if name == "sms" {
			item["detail"] = "Provider accepted one test SMS. Confirm receipt on the phone; acceptance is not delivery."
		}
		results = append(results, item)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"results": results})
	if failed {
		os.Exit(1)
	}
}
func required(keys ...string) error {
	for _, k := range keys {
		if strings.TrimSpace(os.Getenv(k)) == "" {
			return fmt.Errorf("%s is required", k)
		}
	}
	return nil
}
func safeError(err error) string {
	s := err.Error()
	// Provider/driver errors may contain URLs, so suppress them and secret values.
	for _, entry := range os.Environ() {
		kv := strings.SplitN(entry, "=", 2)
		if len(kv) == 2 && strings.HasPrefix(kv[0], "SPACE_") && len(kv[1]) >= 8 {
			s = strings.ReplaceAll(s, kv[1], "[configured value]")
		}
	}
	for _, word := range strings.Fields(s) {
		if strings.Contains(word, "://") {
			s = strings.ReplaceAll(s, word, "[provider URL]")
		}
	}
	return s
}
func database(ctx context.Context) error {
	if err := required("SPACE_DATABASE_URL"); err != nil {
		return err
	}
	raw := os.Getenv("SPACE_DATABASE_URL")
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid database URL")
	}
	if u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Query().Get("sslmode") != "require" && u.Query().Get("sslmode") != "verify-full" {
		return errors.New("remote database requires sslmode=require or verify-full")
	}
	pool, err := postgres.NewPool(ctx, postgres.Config{URL: raw, MaxConns: 2, MinConns: 1, ConnLifetime: time.Minute, PingTimeout: 30 * time.Second, ExecMode: "exec"})
	if err != nil {
		return err
	}
	defer pool.Close()
	var extensions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_extension WHERE extname IN ('postgis','btree_gist','pg_trgm','citext')`).Scan(&extensions); err != nil {
		return err
	}
	if extensions != 4 {
		return errors.New("required spatial/search extensions are not installed; apply migrations using the direct URL")
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		return err
	}
	if version < 23 {
		return fmt.Errorf("schema version %d is below 23", version)
	}
	var valid bool
	if err := pool.QueryRow(ctx, `SELECT ST_DWithin(ST_SetSRID(ST_MakePoint(-0.187,5.6037),4326)::geography,ST_SetSRID(ST_MakePoint(-0.187,5.6037),4326)::geography,100)`).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errors.New("ghana spatial query failed")
	}
	var queues bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('river_job') IS NOT NULL`).Scan(&queues); err != nil {
		return err
	}
	if !queues {
		return errors.New("river schema missing")
	}
	return nil
}
func storage(ctx context.Context) (result error) {
	if err := required("SPACE_STORAGE_ENDPOINT", "SPACE_STORAGE_BUCKET", "SPACE_STORAGE_ACCESS_KEY", "SPACE_STORAGE_SECRET_KEY", "SPACE_STORAGE_PUBLIC_BASE_URL", "SPACE_VERIFY_WEB_ORIGIN"); err != nil {
		return err
	}
	cfg := objectstore.Config{Endpoint: os.Getenv("SPACE_STORAGE_ENDPOINT"), Region: os.Getenv("SPACE_STORAGE_REGION"), Bucket: os.Getenv("SPACE_STORAGE_BUCKET"), AccessKey: os.Getenv("SPACE_STORAGE_ACCESS_KEY"), SecretKey: os.Getenv("SPACE_STORAGE_SECRET_KEY"), PublicBaseURL: os.Getenv("SPACE_STORAGE_PUBLIC_BASE_URL"), ForcePathStyle: os.Getenv("SPACE_STORAGE_PATH_STYLE") != "false", PresignTTL: time.Minute * 5}
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	store, err := objectstore.New(ctx, cfg)
	if err != nil {
		return err
	}
	key := "_verification/" + uuid.NewString() + ".png"
	payload, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	signed, _, err := store.PresignPut(ctx, key, "image/png")
	if err != nil {
		return err
	}
	origin := os.Getenv("SPACE_VERIFY_WEB_ORIGIN")
	client := &http.Client{Timeout: 30 * time.Second}
	pre, _ := http.NewRequestWithContext(ctx, "OPTIONS", signed, nil)
	pre.Header.Set("Origin", origin)
	pre.Header.Set("Access-Control-Request-Method", "PUT")
	pre.Header.Set("Access-Control-Request-Headers", "content-type")
	resp, err := client.Do(pre)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	allowedHeaders := strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers"))
	if resp.StatusCode >= 300 || !corsOrigin(resp, origin) || !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "PUT") || (!strings.Contains(allowedHeaders, "content-type") && allowedHeaders != "*") {
		return errors.New("presigned upload CORS preflight failed")
	}
	// A failed or interrupted upload can still leave an object. Always attempt cleanup.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := store.Delete(cleanup, key); err != nil {
			result = errors.Join(result, fmt.Errorf("verification object cleanup failed: %w", err))
		}
	}()
	put, _ := http.NewRequestWithContext(ctx, "PUT", signed, bytes.NewReader(payload))
	put.Header.Set("Content-Type", "image/png")
	put.Header.Set("Origin", origin)
	resp, err = client.Do(put)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 || !corsOrigin(resp, origin) {
		return fmt.Errorf("presigned upload failed or lacks CORS (HTTP %d)", resp.StatusCode)
	}
	info, err := store.Head(ctx, key)
	if err != nil {
		return err
	}
	if info.Size != int64(len(payload)) || info.ContentType != "image/png" {
		return errors.New("uploaded object metadata differs")
	}
	body, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	actual, err := io.ReadAll(io.LimitReader(body, 1024))
	_ = body.Close()
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, payload) {
		return errors.New("authenticated storage round trip differs")
	}
	get, _ := http.NewRequestWithContext(ctx, "GET", store.PublicURL(key), nil)
	get.Header.Set("Origin", origin)
	resp, err = client.Do(get)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	actual, err = io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 || !bytes.Equal(actual, payload) || !corsOrigin(resp, origin) {
		return errors.New("public photo delivery or public-domain CORS failed")
	}
	return nil
}
func corsOrigin(resp *http.Response, origin string) bool {
	allow := resp.Header.Get("Access-Control-Allow-Origin")
	return allow == origin || allow == "*"
}
func sendSMS(ctx context.Context) error {
	if err := required("SPACE_ARKESEL_API_KEY", "SPACE_SMS_SENDER_ID", "SPACE_VERIFY_SMS_PHONE"); err != nil {
		return err
	}
	if os.Getenv("SPACE_SMS_PROVIDER") != "arkesel" || os.Getenv("SPACE_ARKESEL_SANDBOX") != "false" {
		return errors.New("real SMS check requires arkesel and SPACE_ARKESEL_SANDBOX=false")
	}
	senderID := os.Getenv("SPACE_SMS_SENDER_ID")
	if len(senderID) > 11 {
		return errors.New("sender ID exceeds 11 characters")
	}
	phone, err := identity.NormalizePhone(os.Getenv("SPACE_VERIFY_SMS_PHONE"))
	if err != nil {
		return errors.New("invalid verification phone number")
	}
	return sms.NewArkesel(os.Getenv("SPACE_ARKESEL_API_KEY"), senderID, false).Send(ctx, phone, "Findaspace SMS delivery test. Reply in your setup conversation when this arrives. This is not a login code.")
}
