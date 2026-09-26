package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// photo builds a noisy image, so JPEG sizes behave like a real photograph
// rather than a flat colour that compresses to almost nothing.
func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(7)
	for y := range h {
		for x := range w {
			seed = seed*1664525 + 1013904223
			img.Set(x, y, color.RGBA{uint8(x ^ y), uint8(seed >> 24), uint8(y), 255})
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCardIsSmallAndBounded(t *testing.T) {
	n, err := Normalize(bytes.NewReader(encodeJPEG(t, photo(4000, 2667))), ContentJPEG, 64<<20)
	if err != nil {
		t.Fatal(err)
	}

	if n.CardWidth != CardEdge {
		t.Errorf("card width = %d, want %d", n.CardWidth, CardEdge)
	}
	if want := n.Height * CardEdge / n.Width; n.CardHeight != want {
		t.Errorf("card height = %d, want %d to keep the aspect ratio", n.CardHeight, want)
	}

	card, err := jpeg.Decode(bytes.NewReader(n.Card))
	if err != nil {
		t.Fatalf("card is not a valid JPEG: %v", err)
	}
	if card.Bounds().Dx() != n.CardWidth || card.Bounds().Dy() != n.CardHeight {
		t.Errorf("reported %dx%d but encoded %dx%d",
			n.CardWidth, n.CardHeight, card.Bounds().Dx(), card.Bounds().Dy())
	}

	// The whole point: a page of twenty cards must not cost twenty full photos.
	ratio := float64(len(n.Data)) / float64(len(n.Card))
	t.Logf("full %d KB, card %d KB, %.1fx smaller", len(n.Data)/1024, len(n.Card)/1024, ratio)
	if ratio < 5 {
		t.Errorf("card is only %.1fx smaller than the full image", ratio)
	}
}

func TestPortraitCardBoundsTheLongEdge(t *testing.T) {
	n, err := Normalize(bytes.NewReader(encodeJPEG(t, photo(1200, 1800))), ContentJPEG, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	if n.CardHeight != CardEdge || n.CardWidth != 426 {
		t.Errorf("card = %dx%d, want 426x%d", n.CardWidth, n.CardHeight, CardEdge)
	}
}

// A photo already smaller than a card is left alone, never enlarged.
func TestSmallPhotoIsNotUpscaled(t *testing.T) {
	n, err := Normalize(bytes.NewReader(encodeJPEG(t, photo(400, 300))), ContentJPEG, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	if n.CardWidth != 400 || n.CardHeight != 300 {
		t.Errorf("card = %dx%d, want the original 400x300", n.CardWidth, n.CardHeight)
	}
}

// A transparent PNG encoded straight to JPEG turns every clear pixel black. A
// floor plan on a transparent background would arrive as a black rectangle.
func TestTransparentPNGCardIsFlattenedOntoWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	for y := 200; y < 400; y++ {
		for x := 300; x < 500; x++ {
			img.Set(x, y, color.NRGBA{20, 40, 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	n, err := Normalize(&buf, ContentPNG, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	card, err := jpeg.Decode(bytes.NewReader(n.Card))
	if err != nil {
		t.Fatalf("a PNG upload must still produce a JPEG card: %v", err)
	}
	r, g, b, _ := card.At(5, 5).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Errorf("transparent corner = rgb(%d,%d,%d), want near white", r>>8, g>>8, b>>8)
	}
}

// The card goes through the same decode and re-encode as the full image, so
// the GPS tags a phone writes cannot reach it either.
func TestCardCarriesNoMetadata(t *testing.T) {
	src := encodeJPEG(t, photo(1000, 750))
	exif := []byte{0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 'G', 'P', 'S', '!', '!', '!', '!', '!'}
	tagged := append(append([]byte{0xFF, 0xD8}, exif...), src[2:]...)

	n, err := Normalize(bytes.NewReader(tagged), ContentJPEG, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"full": n.Data, "card": n.Card} {
		if bytes.Contains(data, []byte("Exif")) || bytes.Contains(data, []byte("GPS")) {
			t.Errorf("%s image still carries the metadata", name)
		}
	}
}

func TestCardKey(t *testing.T) {
	cases := map[string]string{
		"spaces/abc/0199aa.jpg":  "spaces/abc/0199aa-card.jpg",
		"spaces/abc/0199aa.png":  "spaces/abc/0199aa-card.jpg",
		"spaces/abc/0199aa.jpeg": "spaces/abc/0199aa-card.jpg",
		"spaces/abc/0199aa":      "spaces/abc/0199aa-card.jpg",
	}
	for in, want := range cases {
		if got := CardKey(in); got != want {
			t.Errorf("CardKey(%q) = %q, want %q", in, got, want)
		}
	}
}
