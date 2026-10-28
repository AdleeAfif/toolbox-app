package compress

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestJPEGScales(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3000, 10))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	res, err := File(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(res.Data, []byte{0xff, 0xd8, 0xff}) {
		t.Fatal("not a jpeg")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(res.Data))
	if err != nil || format != "jpeg" || cfg.Width > 2048 {
		t.Fatalf("format %s %dx%d err %v", format, cfg.Width, cfg.Height, err)
	}
}

func TestPNGKeepsTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 0})
	src.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 128})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	res, err := File(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(res.Data))
	if err != nil || format != "png" {
		t.Fatalf("format %s err %v", format, err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("transparent pixel alpha %d", a)
	}
	if _, _, _, a := img.At(1, 0).RGBA(); a == 0xffff {
		t.Fatal("translucent pixel became opaque")
	}
}

func TestWideHeaderRejected(t *testing.T) {
	_, err := File(pngHeader(8193, 1))
	if err != ErrFile {
		t.Fatalf("got %v", err)
	}
}

func TestRandomRejected(t *testing.T) {
	_, err := File([]byte("not a file"))
	if err != ErrFile {
		t.Fatalf("got %v", err)
	}
}

func TestPDF(t *testing.T) {
	res, err := File(tinyPDF(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(res.Data, []byte("%PDF-")) {
		t.Fatalf("prefix %q", res.Data[:min(8, len(res.Data))])
	}
	if res.Preview {
		t.Fatal("pdf should not preview")
	}

	_, err = File([]byte("%PDF-1.4\nthis is not a pdf"))
	if !errors.Is(err, ErrPDF) {
		t.Fatalf("garbage pdf: %v", err)
	}
}

func tinyPDF(t *testing.T) []byte {
	t.Helper()
	model.ConfigPath = "disable"
	const spec = `{"pages":{"1":{"content":{"text":[{"value":"Hi","anchor":"c","font":{"name":"Helvetica","size":12}}]}}}}`
	var buf bytes.Buffer
	if err := api.Create(nil, strings.NewReader(spec), &buf, model.NewDefaultConfiguration()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngHeader(w, h int) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8] = 8
	ihdr[9] = 2
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	writeChunk(&buf, "IHDR", ihdr)
	return buf.Bytes()
}

func writeChunk(buf *bytes.Buffer, typ string, data []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data)))
	buf.Write(n[:])
	buf.WriteString(typ)
	buf.Write(data)
	sum := crc32.ChecksumIEEE(append([]byte(typ), data...))
	binary.BigEndian.PutUint32(n[:], sum)
	buf.Write(n[:])
}
