package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
)

const (
	MaxEdge     = 2560
	jpegQuality = 85
	maxPixels   = 50_000_000 // 50MP: beyond this, decoding is a memory attack
	ContentJPEG = "image/jpeg"
	ContentPNG  = "image/png"
)

var (
	ErrUnsupportedType = errors.New("media: unsupported image type")
	ErrCorruptImage    = errors.New("media: image could not be decoded")
	ErrImageTooLarge   = errors.New("media: image dimensions are too large")
)

type Normalized struct {
	Data        []byte
	ContentType string
	Width       int
	Height      int

	// Card is the small rendition search results load. Always a JPEG.
	Card       []byte
	CardWidth  int
	CardHeight int
}

// Normalize decodes, downscales and re-encodes an image, and derives the card
// rendition from the same decode.
//
// The re-encode is the security control: Go's decoders keep no metadata, so
// EXIF cannot survive the round trip, and the GPS tags a phone writes into a
// listing photo never reach the internet. The card goes through the same
// re-encode, so it is equally clean.
//
// Decoding is the expensive step, so it happens once. The card is scaled from
// the already-downscaled image rather than the original, which is faster and
// looks identical at that size.
func Normalize(r io.Reader, declaredType string, limit int64) (Normalized, error) {
	if declaredType != ContentJPEG && declaredType != ContentPNG {
		return Normalized{}, ErrUnsupportedType
	}

	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return Normalized{}, fmt.Errorf("read image: %w", err)
	}
	if int64(len(raw)) > limit {
		return Normalized{}, ErrImageTooLarge
	}

	// Header only. A 50000x50000 PNG is a few hundred KB compressed and about
	// 10GB decoded, so the dimension check has to come before the decode.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Normalized{}, ErrCorruptImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return Normalized{}, ErrImageTooLarge
	}

	// The declared content type is the client's claim; the magic bytes are the
	// truth. Disagreement means a mislabelled or hostile upload.
	if (format == "jpeg") != (declaredType == ContentJPEG) {
		return Normalized{}, ErrUnsupportedType
	}

	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Normalized{}, ErrCorruptImage
	}

	full := downscale(src, MaxEdge)
	bounds := full.Bounds()

	var buf bytes.Buffer
	switch declaredType {
	case ContentJPEG:
		err = jpeg.Encode(&buf, full, &jpeg.Options{Quality: jpegQuality})
	case ContentPNG:
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		err = enc.Encode(&buf, full)
	}
	if err != nil {
		return Normalized{}, fmt.Errorf("encode image: %w", err)
	}

	card, cw, ch, err := encodeCard(full)
	if err != nil {
		return Normalized{}, err
	}

	return Normalized{
		Data:        buf.Bytes(),
		ContentType: declaredType,
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
		Card:        card,
		CardWidth:   cw,
		CardHeight:  ch,
	}, nil
}
func downscale(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return src
	}

	if w >= h {
		h = h * maxEdge / w
		w = maxEdge
	} else {
		w = w * maxEdge / h
		h = maxEdge
	}

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// CatmullRom over ApproxBiLinear: photos downscaled by 3x or more look
	// noticeably soft with the fast kernel, and this runs once per upload.
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
