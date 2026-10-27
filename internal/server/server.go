package server

import (
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"

	"url-tool/internal/qr"
	"url-tool/web"
)

var tmpl = template.Must(template.ParseFS(web.Templates, "templates/*.html"))

func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", index)
	mux.HandleFunc("POST /qr", generate)
	return mux
}

func index(w http.ResponseWriter, r *http.Request) {
	render(w, "index", nil)
}

func generate(w http.ResponseWriter, r *http.Request) {
	// htmx only swaps 2xx responses by default, so every error below renders with 200.
	r.Body = http.MaxBytesReader(w, r.Body, qr.MaxLogoBytes+64<<10)
	if err := r.ParseMultipartForm(qr.MaxLogoBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		render(w, "error", qr.ErrLogo.Error())
		return
	}
	rawURL := r.FormValue("url")
	logo, err := readLogo(r)
	if err != nil {
		log.Printf("read logo: %v", err)
		render(w, "error", qr.ErrLogo.Error())
		return
	}

	png, err := qr.PNG(rawURL, logo)
	if err != nil {
		msg := err.Error()
		var inputErr *qr.InputError
		if !errors.As(err, &inputErr) {
			log.Printf("encode qr: %v", err)
			msg = "could not generate the QR code"
		}
		render(w, "error", msg)
		return
	}
	render(w, "result", map[string]string{
		"Base64": base64.StdEncoding.EncodeToString(png),
		"URL":    strings.TrimSpace(rawURL),
	})
}

func readLogo(r *http.Request) ([]byte, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	f, _, err := r.FormFile("logo")
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, qr.MaxLogoBytes+1))
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}
