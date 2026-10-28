package server

import (
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"

	"toolbox-app/internal/compare"
	"toolbox-app/internal/compress"
	"toolbox-app/internal/qr"
	"toolbox-app/web"
)

var tmpl = template.Must(template.ParseFS(web.Templates, "templates/*.html"))

func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", index)
	mux.HandleFunc("GET /healthz", live)
	mux.HandleFunc("GET /readyz", live)
	mux.HandleFunc("GET /qr", qrPage)
	mux.HandleFunc("POST /qr", generate)
	mux.HandleFunc("GET /compare", comparePage)
	mux.HandleFunc("POST /compare", comparePost)
	mux.HandleFunc("GET /compress", compressPage)
	mux.HandleFunc("POST /compress", compressPost)
	return mux
}

func index(w http.ResponseWriter, r *http.Request) {
	render(w, "index", nil)
}

func live(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok\n")
}

// page renders a tool as a full page, or as just the tool when htmx loads it
// into the toolbox dialog.
func page(w http.ResponseWriter, r *http.Request, name string) {
	w.Header().Add("Vary", "HX-Request")
	if r.Header.Get("HX-Request") == "true" {
		name += "-tool"
	}
	render(w, name, nil)
}

func qrPage(w http.ResponseWriter, r *http.Request) {
	page(w, r, "qr")
}

func generate(w http.ResponseWriter, r *http.Request) {
	// htmx only swaps 2xx responses by default, so every error below renders with 200.
	r.Body = http.MaxBytesReader(w, r.Body, qr.MaxLogoBytes+64<<10)
	//nolint:gosec // G120: MaxLogoBytes is the cap, and MaxBytesReader already limited the body.
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

func comparePage(w http.ResponseWriter, r *http.Request) {
	page(w, r, "compare")
}

func comparePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, compare.MaxBytes*2+64<<10)
	if err := r.ParseForm(); err != nil {
		render(w, "error", compare.ErrTooLong.Error())
		return
	}
	res, err := compare.Diff(r.PostFormValue("before"), r.PostFormValue("after"))
	if err != nil {
		msg := err.Error()
		var inputErr *compare.InputError
		if !errors.As(err, &inputErr) {
			log.Printf("compare: %v", err)
			msg = "could not compare the text"
		}
		render(w, "error", msg)
		return
	}
	render(w, "compare-result", res)
}

func compressPage(w http.ResponseWriter, r *http.Request) {
	page(w, r, "compress")
}

func compressPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, compress.MaxBytes+64<<10)
	//nolint:gosec // G120: MaxBytes is the cap, and MaxBytesReader already limited the body.
	if err := r.ParseMultipartForm(compress.MaxBytes); err != nil {
		render(w, "error", compress.ErrFile.Error())
		return
	}
	data, err := readUpload(r, "file", compress.MaxBytes)
	if err != nil {
		log.Printf("read file: %v", err)
		render(w, "error", compress.ErrFile.Error())
		return
	}
	res, err := compress.File(data)
	if err != nil {
		msg := err.Error()
		var inputErr *compress.InputError
		if errors.Is(err, compress.ErrPDF) {
			log.Printf("compress pdf: %v", err)
			msg = "could not compress this PDF"
		} else if !errors.As(err, &inputErr) {
			log.Printf("compress: %v", err)
			msg = "could not compress the file"
		}
		render(w, "error", msg)
		return
	}
	render(w, "compress-result", map[string]any{
		"Before":  res.Before,
		"After":   res.After,
		"Note":    res.Note,
		"Name":    res.Name,
		"MIME":    res.MIME,
		"Base64":  base64.StdEncoding.EncodeToString(res.Data),
		"Preview": res.Preview,
	})
}

func readUpload(r *http.Request, name string, max int64) ([]byte, error) {
	if r.MultipartForm == nil {
		return nil, errors.New("missing form")
	}
	f, _, err := r.FormFile(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max+1))
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
