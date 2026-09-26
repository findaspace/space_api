package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"path"
	"strings"

	"golang.org/x/image/draw"
)

// CardEdge is the long edge of the rendition used by search results and
// listing cards.
//
// A card is at most about 300 CSS pixels wide on a desktop grid, which is 600
// device pixels on a high-density screen. 640 covers that with room to spare.
// The full image stays at MaxEdge for the listing page, where someone has
// chosen to look closely.
const CardEdge = 640

// cardQuality is lower than the full image's. At card size the difference is
// invisible, and on a page of twenty results over mobile data the bytes are
// not.
const cardQuality = 72

// encodeCard scales an already-normalised image to card size and encodes it as
// a JPEG, whatever the upload was: a PNG card of a photograph is several times
// the size for no visible gain.
//
// JPEG has no transparency, and encoding a transparent PNG directly turns every
// clear pixel black, so a floor plan on a transparent background would arrive
// as a black rectangle. The image is flattened onto white first.
func encodeCard(src image.Image) ([]byte, int, int, error) {
	small := downscale(src, CardEdge)
	bounds := small.Bounds()

	flat := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), small, bounds.Min, draw.Over)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: cardQuality}); err != nil {
		return nil, 0, 0, fmt.Errorf("encode card: %w", err)
	}
	return buf.Bytes(), bounds.Dx(), bounds.Dy(), nil
}

// CardKey is where a photo's card rendition lives, derived from the full
// image's key so the two can never drift apart:
//
//	spaces/abc/0199aa.jpg  ->  spaces/abc/0199aa-card.jpg
//	spaces/abc/0199aa.png  ->  spaces/abc/0199aa-card.jpg
//
// Always .jpg, because the card is always a JPEG.
func CardKey(objectKey string) string {
	return strings.TrimSuffix(objectKey, path.Ext(objectKey)) + "-card.jpg"
}
