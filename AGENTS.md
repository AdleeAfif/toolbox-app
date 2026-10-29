# AGENTS.md - toolbox-app

Instructions for coding agents working in this repository. Read this whole file before making changes.

## What this is

A toolbox of small utilities. The home page is a closed toolbox. Opening it pops up a dialog listing the tools, and picking one shows it in that same dialog, with Back to toolbox and Close toolbox buttons. Each tool also stays a full page at its own URL, with an htmx form and result area.

Current tools:

- **URL to QR**: paste an http(s) URL, optionally upload a logo, get a QR code PNG in the page, and download it.
- **Text compare**: paste two texts and see a line diff.
- **Compress**: upload a JPEG, PNG, or PDF, then download a smaller file.
- **Route stops**: give a start (or the browser's location) and a destination, pick petrol, food, cafe, or mosque, and get places along the driving route labelled by how easy they are to stop at.

One Go binary serves both the backend and the htmx frontend. Templates live in `web/templates` and are embedded with `go:embed`, so there is no separate frontend process.

## Stack (do not substitute)

- Go standard `net/http` (`ServeMux` method patterns), `html/template`, `go:embed`. No router library.
- `github.com/skip2/go-qrcode` for encoding.
- `github.com/sergi/go-diff` v1.4.0 for line diffs.
- `github.com/pdfcpu/pdfcpu` v0.10.2 for in-memory PDF optimize. v0.11 and later need a newer Go. Do not use `@latest`.
- `golang.org/x/image/draw` for scaling. Pinned to v0.30.0, the last release that supports Go 1.23 (the dev machine runs 1.23.2). Run `go get` with `GOTOOLCHAIN=local` so it doesn't bump the `go` line.
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
- `internal/compare` - line diff of two texts.
- `internal/compress` - shrinks a JPEG, PNG, or PDF in memory.
- `internal/stops` - geocodes, routes, and searches OpenStreetMap along the route, then labels each place.
- `internal/server` - `GET /` (tool list), `GET /healthz` and `GET /readyz`, and `GET` and `POST` for `/qr`, `/compare`, `/compress`, and `/stops`. Each `POST` returns an HTML partial. A `GET` with the `HX-Request` header returns only the tool (`<tool>-tool`) for the dialog, and a plain `GET` returns the full page.
- `web/templates/index.html` - the toolbox home page (a box button, one `<dialog>` with a tool list view and a tool view, and a small inline script), a `<tool>-tool` and a full-page template per tool, plus `result`, `compare-result`, `compress-result`, `stops-result`, and `error`. The shared `head` holds `toolboxLocate`, the location helper for Route stops.
- `Dockerfile`, `deploy/k8s`, `.github/workflows/release.yml` - the Pi release path. See `docs/deploy.md`.

## Adding a tool

- Put the logic in its own package under `internal/<tool>`, with a test.
- Add `GET /<tool>` for the page and `POST /<tool>` that returns an HTML partial.
- Split its template into `<tool>-tool` (heading, form, result area) and `<tool>` (head, back link, then `<tool>-tool`). `GET` goes through `page()` so htmx gets just the tool.
- Add a tile to the list in the dialog on the home page, and a link to the `<noscript>` line, with `href` and `hx-get` to `/<tool>`, `hx-target="#tool-body"`, and a `.name` span.

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

## Compare rules

- Do not trim. Spaces and blank lines are part of the text.
- Both empty returns `enter text to compare`. Either side over 256 KiB returns `text is longer than 256 KB`.
- Identical text says `No differences.` Otherwise show changed lines with up to 2 unchanged lines of context. Collapse a longer unchanged run into `… N unchanged lines …`.
- Use `github.com/sergi/go-diff` line mode (`DiffLinesToRunes`, `DiffMainRunes`, then map back to lines). Do not write a diff algorithm.

## Compress rules

- One file, detected from the bytes: PNG, JPEG, or PDF, at most 20 MB. Anything else returns `file must be a PNG, JPEG, or PDF under 20 MB`.
- Check image dimensions with `image.DecodeConfig` before decoding. Reject a side over 8192.
- JPEG is re-encoded at quality 75. If either side is over 2048 px, scale the long side to 2048 with `draw.CatmullRom` and keep the aspect ratio. PNG uses the same scale rule and stays PNG, including transparency.
- PDF uses `api.Optimize` and `model.NewDefaultConfiguration()` on a `bytes.Reader`. Do not call `OptimizeFile`. Do not write a config file or a temp PDF. Keep pdfcpu at v0.10.2, `go 1.23.2`, and `golang.org/x/image` v0.30.0.
- If the result is not smaller, return the original bytes and say `Already as small as this tool can make it.`
- Download names are `compressed.jpg`, `compressed.png`, or `compressed.pdf`. Show an image preview for JPEG and PNG only.
- A failed PDF optimize is logged and shown as `could not compress this PDF`.

## Route stops rules

- This is the one tool that calls outside services: Nominatim (place to coordinates), OSRM (driving route), and Overpass (places). Each call has a timeout: 30 seconds for Nominatim and OSRM, 60 for Overpass, which is often slow under load. Retry Overpass once, only on a 502, 503, or 504 that came back within 10 seconds. The whole search stops at 90 seconds, under Cloudflare's 100 second limit. Send the `toolbox-app (https://toolbox.adlee.my)` User-Agent. Overpass filters are exact tag matches in a union, not regexes. No API keys, no new dependencies.
- Tests never call the real services. Point `stops.New` at `httptest` servers.
- One search at a time, waiting up to 10 seconds, then `another search is running, try again in a moment`. Space Nominatim calls at least 1 second apart.
- A `From` of `lat, lng` skips Nominatim. Place names are trimmed and at most 200 characters. Routes over 300 km are rejected before Overpass is called.
- Search within 1 km of the route, simplified to at most 400 points. Show at most 40 places, ordered by km ahead.
- Labels: `Easy stop` when the nearest part of the route is not expressway. On expressway, `Easy stop · R&R` inside a `services` or `rest_area` area on the left or within 60 m of the line, `R&R on the other side` inside one on the right, and `Near the route, may need an exit` otherwise. Malaysia drives on the left.
- Never log coordinates, place names, or the route. Strip the URL from `url.Error` before logging. `POST` only, and send `Cache-Control: no-store`.
- Show `Map data © OpenStreetMap contributors` under every result.
- An upstream failure is logged with the service name and shown as `the map service is not responding, try again in a minute`.

## Out of scope unless asked

Colors, size picker, SVG, history, auth, a second frontend process, another image or cluster. Releases go through the existing workflow.
