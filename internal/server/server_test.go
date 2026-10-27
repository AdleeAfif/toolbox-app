package server

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"toolbox-app/internal/qr"
)

func multipartBody(t *testing.T, rawURL string, logo []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("url", rawURL)
	if logo != nil {
		fw, err := mw.CreateFormFile("logo", "logo.png")
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(logo)
	}
	mw.Close()
	return &body, mw.FormDataContentType()
}

func TestPages(t *testing.T) {
	home := get(t, "/")
	if !strings.Contains(home, `href="/qr"`) || strings.Contains(home, `hx-post="/qr"`) {
		t.Fatalf("home should list the QR tool: %.200q", home)
	}
	page := get(t, "/qr")
	if !strings.Contains(page, `hx-post="/qr"`) || !strings.Contains(page, `href="/"`) {
		t.Fatalf("QR page should contain the form and a link home: %.200q", page)
	}
}

func get(t *testing.T, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	return rec.Body.String()
}

func TestGenerate(t *testing.T) {
	var logo bytes.Buffer
	if err := png.Encode(&logo, image.NewGray(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	withLogo, withLogoType := multipartBody(t, "https://example.com", logo.Bytes())
	noLogo, noLogoType := multipartBody(t, "https://example.com", nil)
	huge, hugeType := multipartBody(t, "https://example.com", make([]byte, qr.MaxLogoBytes+128<<10))
	form := strings.NewReader(url.Values{"url": {"https://example.com"}}.Encode())

	for name, tc := range map[string]struct {
		body        io.Reader
		contentType string
		want        string
	}{
		"multipart with logo": {withLogo, withLogoType, "<img"},
		"multipart no logo":   {noLogo, noLogoType, "<img"},
		"urlencoded":          {form, "application/x-www-form-urlencoded", "<img"},
		"logo too large":      {huge, hugeType, "logo must be"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/qr", tc.body)
		req.Header.Set("Content-Type", tc.contentType)
		rec := httptest.NewRecorder()
		New().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: status %d, body %.120q, want %q", name, rec.Code, rec.Body.String(), tc.want)
		}
	}
}
