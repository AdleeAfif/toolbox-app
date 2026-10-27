package qr

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

var red = color.RGBA{255, 0, 0, 255}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func redLogo(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		for y := range 20 {
			img.Set(x, y, red)
		}
	}
	return encodePNG(t, img)
}

func TestPNG(t *testing.T) {
	out, err := PNG("  https://example.com  ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("output is not a PNG")
	}
}

func TestPNGWithLogo(t *testing.T) {
	for name, url := range map[string]string{
		"short": "https://example.com",
		"dense": "https://example.com/" + strings.Repeat("a", 600),
	} {
		out, err := PNG(url, redLogo(t))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b := img.Bounds()
		if b.Dx() < size || b.Dx() != b.Dy() {
			t.Fatalf("%s: size %v, want square of at least %d", name, b.Size(), size)
		}
		if got := color.RGBAModel.Convert(img.At(b.Dx()/2, b.Dy()/2)); got != red {
			t.Fatalf("%s: center pixel %v, want logo red", name, got)
		}
	}
}

func TestPNGRejectsBadInput(t *testing.T) {
	wide := encodePNG(t, image.NewGray(image.Rect(0, 0, maxLogoSide+1, 1)))
	logo := redLogo(t)

	for name, tc := range map[string]struct {
		url  string
		logo []byte
		want error
	}{
		"empty":         {"   \n", nil, ErrEmpty},
		"too long":      {"https://example.com/" + strings.Repeat("a", MaxBytes), nil, ErrTooLong},
		"plain text":    {"hello world", nil, ErrInvalidURL},
		"no host":       {"https://", nil, ErrInvalidURL},
		"bad scheme":    {"javascript:alert(1)", nil, ErrInvalidURL},
		"logo not img":  {"https://example.com", []byte("not an image"), ErrLogo},
		"logo too big":  {"https://example.com", make([]byte, MaxLogoBytes+1), ErrLogo},
		"logo too wide": {"https://example.com", wide, ErrLogo},
		"logo long url": {"https://example.com/" + strings.Repeat("a", 1500), logo, ErrLogoURL},
	} {
		if _, err := PNG(tc.url, tc.logo); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}
