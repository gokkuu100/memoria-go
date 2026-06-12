package media

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"

	"github.com/google/uuid"
	"golang.org/x/image/draw"
)

// ThumbMaxPx is the longest edge of widget-sized JPEG thumbnails.
const ThumbMaxPx = 400

// ThumbKey returns the R2 object key for a media row's widget thumbnail.
func ThumbKey(ownerID, mediaID uuid.UUID) string {
	return fmt.Sprintf("media/%s/%s_thumb.jpg", ownerID, mediaID)
}

// GenerateJPEGThumb scales src so its longest edge is at most maxPx and encodes JPEG.
func GenerateJPEGThumb(src image.Image, maxPx int) ([]byte, error) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid image dimensions %dx%d", w, h)
	}

	outW, outH := w, h
	if w > maxPx || h > maxPx {
		if w >= h {
			outW = maxPx
			outH = max(1, h*maxPx/w)
		} else {
			outH = maxPx
			outW = max(1, w*maxPx/h)
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WidgetThumbKey picks the object key widgets should fetch: generated JPEG
// thumb for photos; full media key for video (first-frame extraction deferred).
func WidgetThumbKey(thumbKey *string, mediaKey, kind string) string {
	if thumbKey != nil && *thumbKey != "" {
		return *thumbKey
	}
	if kind == "video" {
		return mediaKey
	}
	return ""
}
