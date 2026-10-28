package server

import (
	"bytes"
	"image"
	"image/jpeg"
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
	if err := mw.WriteField("url", rawURL); err != nil {
		t.Fatal(err)
	}
	if logo != nil {
		fw, err := mw.CreateFormFile("logo", "logo.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(logo); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

func TestHealth(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
		if body := get(t, path); body != "ok\n" {
			t.Fatalf("%s = %q", path, body)
		}
	}
}

func TestPages(t *testing.T) {
	home := get(t, "/")
	if !strings.Contains(home, `href="/qr"`) || !strings.Contains(home, `href="/compare"`) || !strings.Contains(home, `href="/compress"`) || strings.Contains(home, `hx-post=`) {
		t.Fatalf("home should list the tools: %.300q", home)
	}
	for _, want := range []string{`id="toolbox"`, `id="picker"`, `hx-get="/qr"`, `hx-target="#tool-body"`, `<dialog id="tool"`} {
		if !strings.Contains(home, want) {
			t.Fatalf("home should contain %q", want)
		}
	}
	for _, tc := range []struct{ path, post string }{
		{"/qr", `hx-post="/qr"`},
		{"/compare", `hx-post="/compare"`},
		{"/compress", `hx-post="/compress"`},
	} {
		page := get(t, tc.path)
		if !strings.Contains(page, tc.post) || !strings.Contains(page, `href="/"`) {
			t.Fatalf("%s should contain the form and a link home: %.200q", tc.path, page)
		}
	}
}

func TestToolFragment(t *testing.T) {
	for _, tc := range []struct{ path, post string }{
		{"/qr", `hx-post="/qr"`},
		{"/compare", `hx-post="/compare"`},
		{"/compress", `hx-post="/compress"`},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		New().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, tc.post) || strings.Contains(body, "<html") {
			t.Errorf("%s: htmx should get just the tool: status %d, %.200q", tc.path, rec.Code, body)
		}
		if rec.Header().Get("Vary") != "HX-Request" {
			t.Errorf("%s: Vary = %q", tc.path, rec.Header().Get("Vary"))
		}
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

func TestComparePost(t *testing.T) {
	body := strings.NewReader(url.Values{
		"before": {"<script>alert(1)</script>"},
		"after":  {"ok"},
	}.Encode())
	req := httptest.NewRequest(http.MethodPost, "/compare", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "&lt;script&gt;") {
		t.Fatalf("status %d body %.200q", rec.Code, rec.Body.String())
	}
}

func TestCompressPost(t *testing.T) {
	var img bytes.Buffer
	if err := jpeg.Encode(&img, image.NewGray(image.Rect(0, 0, 8, 8)), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	okBody, okType := fileBody(t, "photo.jpg", img.Bytes())
	ok := post(t, "/compress", okBody, okType)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `download="compressed.jpg"`) {
		t.Fatalf("status %d body %.200q", ok.Code, ok.Body.String())
	}

	badBody, badType := fileBody(t, "notes.txt", []byte("hello"))
	bad := post(t, "/compress", badBody, badType)
	if bad.Code != http.StatusOK || !strings.Contains(bad.Body.String(), "file must be a PNG, JPEG, or PDF") {
		t.Fatalf("status %d body %.200q", bad.Code, bad.Body.String())
	}
}

func fileBody(t *testing.T, name string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

func post(t *testing.T, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	return rec
}
