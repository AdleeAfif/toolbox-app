package qr

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/image/draw"
)

const (
	MaxBytes     = 2048
	MaxLogoBytes = 1 << 20
	maxLogoSide  = 4096
	size         = 256
	minModulePx  = 4
	logoPad      = 4
)

// InputError is a problem with what the user sent, safe to show them.
type InputError struct{ msg string }

func (e *InputError) Error() string { return e.msg }

var (
	ErrEmpty      = &InputError{"enter a URL"}
	ErrTooLong    = &InputError{"URL is longer than 2048 bytes"}
	ErrInvalidURL = &InputError{"enter a full http or https URL, like https://example.com"}
	ErrLogo       = &InputError{"logo must be a PNG or JPEG under 1 MB and at most 4096x4096"}
	ErrLogoURL    = &InputError{"URL is too long to fit a logo, remove the logo or shorten the URL"}
)

// PNG trims rawURL, checks it is an http(s) URL, and encodes it as a QR code PNG.
// A non-empty logo (PNG or JPEG) is drawn in the center. It never fetches the URL.
func PNG(rawURL string, logo []byte) ([]byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, ErrEmpty
	}
	if len(rawURL) > MaxBytes {
		return nil, ErrTooLong
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, ErrInvalidURL
	}
	if len(logo) == 0 {
		q, err := qrcode.New(rawURL, qrcode.Medium)
		if err != nil {
			return nil, err
		}
		return q.PNG(sideFor(q))
	}

	src, err := decodeLogo(logo)
	if err != nil {
		return nil, err
	}
	// Highest recovers about 30% damage, so a long URL may not fit.
	q, err := qrcode.New(rawURL, qrcode.Highest)
	if err != nil {
		return nil, ErrLogoURL
	}
	side := sideFor(q)
	code := q.Image(side)
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(img, img.Bounds(), code, code.Bounds().Min, draw.Src)

	// 22% of the side covers about 5% of the area, well inside what Highest recovers.
	logoBox := side * 22 / 100
	padded := logoBox + 2*logoPad
	draw.Draw(img, centered(side, padded, padded), image.NewUniform(color.White), image.Point{}, draw.Src)
	b := src.Bounds()
	w, h := logoBox, logoBox
	if b.Dx() > b.Dy() {
		h = max(1, logoBox*b.Dy()/b.Dx())
	} else {
		w = max(1, logoBox*b.Dx()/b.Dy())
	}
	draw.CatmullRom.Scale(img, centered(side, w, h), src, b, draw.Over, nil)

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func decodeLogo(data []byte) (image.Image, error) {
	if len(data) > MaxLogoBytes {
		return nil, ErrLogo
	}
	// Check dimensions before decoding so a small file can't expand into a huge image.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxLogoSide || cfg.Height > maxLogoSide {
		return nil, ErrLogo
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrLogo
	}
	return img, nil
}

// sideFor gives long URLs a bigger image, because 1-2px modules don't scan reliably.
func sideFor(q *qrcode.QRCode) int {
	return max(size, len(q.Bitmap())*minModulePx)
}

func centered(side, w, h int) image.Rectangle {
	x, y := (side-w)/2, (side-h)/2
	return image.Rect(x, y, x+w, y+h)
}
