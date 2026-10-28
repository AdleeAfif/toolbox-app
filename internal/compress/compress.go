package compress

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"golang.org/x/image/draw"
)

const (
	MaxBytes = 20 << 20
	maxSide  = 8192
	longSide = 2048
	quality  = 75
)

// InputError is a problem with what the user sent, safe to show them.
type InputError struct{ msg string }

func (e *InputError) Error() string { return e.msg }

var (
	ErrFile = &InputError{"file must be a PNG, JPEG, or PDF under 20 MB"}
	ErrPDF  = errors.New("pdf optimize failed")
)

const already = "Already as small as this tool can make it."

// Result is the file to download. Preview is set for JPEG and PNG.
type Result struct {
	Data    []byte
	MIME    string
	Name    string
	Before  int
	After   int
	Note    string
	Preview bool
}

// File shrinks a PNG, JPEG, or PDF. The type comes from the bytes.
// A result that is not smaller comes back as the original bytes.
func File(in []byte) (Result, error) {
	if len(in) == 0 || len(in) > MaxBytes {
		return Result{}, ErrFile
	}
	switch http.DetectContentType(in) {
	case "image/png":
		return imageFile(in, "image/png", "compressed.png")
	case "image/jpeg":
		return imageFile(in, "image/jpeg", "compressed.jpg")
	case "application/pdf":
		return pdfFile(in)
	default:
		return Result{}, ErrFile
	}
}

func imageFile(in []byte, mime, name string) (Result, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(in))
	if err != nil || cfg.Width > maxSide || cfg.Height > maxSide || cfg.Width < 1 || cfg.Height < 1 {
		return Result{}, ErrFile
	}
	img, _, err := image.Decode(bytes.NewReader(in))
	if err != nil {
		return Result{}, ErrFile
	}
	img = scale(img)

	var buf bytes.Buffer
	if mime == "image/jpeg" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		return Result{}, err
	}
	return finish(in, buf.Bytes(), mime, name, true), nil
}

func scale(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= longSide && h <= longSide {
		return src
	}
	long := w
	if h > long {
		long = h
	}
	nw := w * longSide / long
	nh := h * longSide / long
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

func pdfFile(in []byte) (Result, error) {
	// NewDefaultConfiguration writes config.yml unless this is set.
	model.ConfigPath = "disable"
	var buf bytes.Buffer
	err := api.Optimize(bytes.NewReader(in), &buf, model.NewDefaultConfiguration())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrPDF, err)
	}
	return finish(in, buf.Bytes(), "application/pdf", "compressed.pdf", false), nil
}

func finish(in, out []byte, mime, name string, preview bool) Result {
	note := ""
	if len(out) >= len(in) {
		out = in
		note = already
	}
	return Result{
		Data:    out,
		MIME:    mime,
		Name:    name,
		Before:  len(in),
		After:   len(out),
		Note:    note,
		Preview: preview,
	}
}
