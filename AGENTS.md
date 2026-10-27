# AGENTS.md - toolbox-app

Instructions for coding agents working in this repository. Read this whole file before making changes.

## What this is

A toolbox of small utilities on one page. Each tool is a section with its own htmx form and result area.

Current tools:

- **URL to QR**: paste an http(s) URL, optionally upload a logo, get a QR code PNG in the page, and download it.

One Go binary serves both the backend and the htmx frontend. Templates live in `web/templates` and are embedded with `go:embed`, so there is no separate frontend process.

## Stack (do not substitute)

- Go standard `net/http` (`ServeMux` method patterns), `html/template`, `go:embed`. No router library.
- `github.com/skip2/go-qrcode` for encoding.
- `golang.org/x/image/draw` for scaling logos. Pinned to v0.30.0, the last release that supports Go 1.23 (the dev machine runs 1.23.2). Run `go get` with `GOTOOLCHAIN=local` so it doesn't bump the `go` line.
- htmx 2 from a CDN script tag. No Node, npm, bundler, or CSS framework.
- No database.

## Commands

```bash
make run   # go run ./cmd/server, serves http://localhost:8080
make test  # go test ./...
```

Without `make` on Windows, run the two `go` commands directly.

## Layout

- `cmd/server` - entry point, listens on `:8080`.
- `internal/qr` - trims and validates the URL, encodes it to a PNG, and draws an optional logo in the center.
- `internal/server` - routes `GET /` (page) and `POST /qr` (multipart htmx form, returns a partial).
- `web/templates/index.html` - `index`, `result`, and `error` templates.

## Adding a tool

- Put the logic in its own package under `internal/<tool>`, with a test.
- Add one `POST /<tool>` route in `internal/server` that returns an HTML partial.
- Add a `<section>` to `index.html` with a form targeting its own result `div`.

## Rules

- Trim input. A URL tool rejects empty input, input longer than 2048 bytes, or anything that is not an absolute http/https URL.
- Never fetch or follow a URL unless a tool explicitly needs to, and then only with a timeout.
- Render user input only through `html/template` so it stays escaped.
- Routes return the `error` partial with HTTP 200, because htmx only swaps 2xx responses by default.

## QR rules

- Images are at least 256px and at least 4px per module. Smaller modules don't scan for long URLs.
- Without a logo use Medium error correction. With a logo use Highest, and keep the logo at about 22% of the side (about 5% of the area) on a white pad.
- Logos are PNG or JPEG, at most 1 MB and 4096x4096. Check dimensions with `image.DecodeConfig` before decoding.
- A URL too long for Highest with a logo returns `ErrLogoURL`, not a generic error.
- After changing the encoding, check that output still scans with a real decoder, not just the unit tests.

## Out of scope unless asked

Colors, size picker, SVG, history, auth, Docker, a second frontend process.
