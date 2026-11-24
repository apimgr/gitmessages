package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"path"
	"strings"

	"github.com/apimgr/gitmessages/src/config"
)

var pwaIconSizes = map[string]int{
	"favicon-16.png":        16,
	"favicon-32.png":        32,
	"icon-72.png":           72,
	"icon-96.png":           96,
	"icon-128.png":          128,
	"icon-144.png":          144,
	"icon-152.png":          152,
	"icon-180.png":          180,
	"icon-192.png":          192,
	"icon-384.png":          384,
	"icon-512.png":          512,
	"icon-maskable-192.png": 192,
	"icon-maskable-512.png": 512,
}

func pwaIcon(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	name := path.Base(r.URL.Path)
	size, ok := pwaIconSizes[name]
	if !ok || !strings.HasPrefix(name, "icon-") && !strings.HasPrefix(name, "favicon-") {
		http.NotFound(w, r)
		return
	}
	if source := strings.TrimSpace(config.Get().Server.Branding.Favicon); source != "" {
		if imageData, err := loadBrandingImage(r.Context(), source); err == nil {
			if encoded, err := cachedScaledBrandingPNG(imageData.body, size, size, false); err == nil {
				w.Header().Set("Content-Type", "image/png")
				if !setBrandingImageCacheHeaders(w, r, encoded) || r.Method == http.MethodHead {
					return
				}
				_, _ = w.Write(encoded)
				return
			}
		}
	}
	if !setVersionedAssetHeaders(w, r, true) {
		return
	}
	maskable := strings.HasPrefix(name, "icon-maskable-")
	encoded, err := renderPWAIcon(size, maskable)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ASSET_UNAVAILABLE", tr(r, "api.encode_failed"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(encoded)
}

func renderPWAIcon(size int, maskable bool) ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, size, size))
	background := color.RGBA{R: 40, G: 42, B: 54, A: 255}
	accent := color.RGBA{R: 189, G: 147, B: 249, A: 255}
	radius := size / 5
	if maskable {
		radius = 0
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if radius == 0 || inRoundedSquare(x, y, size, radius) {
				canvas.SetRGBA(x, y, background)
			}
		}
	}
	left, right := size/5, size*4/5
	if maskable {
		left, right = size/4, size*3/4
	}
	barHeight := max(1, size/12)
	for index := 0; index < 3; index++ {
		top := size * (2 + index*2) / 8
		bottom := top + barHeight
		end := left + (right-left)*(4-index)/4
		for y := top; y < bottom && y < size; y++ {
			for x := left; x < end && x < size; x++ {
				canvas.SetRGBA(x, y, accent)
			}
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func inRoundedSquare(x, y, size, radius int) bool {
	if x >= radius && x < size-radius || y >= radius && y < size-radius {
		return true
	}
	cx, cy := radius, radius
	if x >= size-radius {
		cx = size - radius - 1
	}
	if y >= size-radius {
		cy = size - radius - 1
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}

func pwaSplash(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	name := path.Base(r.URL.Path)
	dimensions, ok := map[string][2]int{
		"iphone-1179x2556.png": {1179, 2556},
		"iphone-1284x2778.png": {1284, 2778},
	}[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	width, height := dimensions[0], dimensions[1]
	if !setVersionedAssetHeaders(w, r, true) {
		return
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	background := color.RGBA{R: 40, G: 42, B: 54, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.SetRGBA(x, y, background)
		}
	}
	size := width / 3
	icon, err := renderPWAIcon(size, true)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ASSET_UNAVAILABLE", tr(r, "api.encode_failed"))
		return
	}
	decoded, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "ASSET_UNAVAILABLE", tr(r, "api.encode_failed"))
		return
	}
	iconRect := image.Rect((width-size)/2, (height-size)/2, (width+size)/2, (height+size)/2)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			canvas.Set(x+iconRect.Min.X, y+iconRect.Min.Y, decoded.At(x, y))
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		writeError(w, r, http.StatusInternalServerError, "ASSET_UNAVAILABLE", tr(r, "api.encode_failed"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(output.Bytes())
}
