# CLAUDE.md

> **MANDATORY FIRST ACTION — every interaction, no exceptions:**
> Run `git status --short` before doing anything else. If there are no changes, proceed silently. If there are uncommitted user changes, complete the user's request first, then at the end ask if they'd like you to commit those changes along with any changes you made (suggest an appropriate message). Only commit if the user confirms.

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Git Conventions

Commit after every logical change. Format: `topic: short description`. For large changes, use a multi-line message where the first line is the short label and the body describes changes in detail.

## Token Optimization

**CRITICAL — apply to every interaction:**

- Read only the lines you need (use offset/limit); never re-read files already in context
- Use targeted Grep/Glob with specific patterns; avoid broad or exploratory searches
- Prefer Edit over Write for small changes; batch related edits when possible
- Keep all output concise — no filler, no restating file contents, no unnecessary explanations
- When multiple tasks arrive in one message, prioritize the latest/most specific request
- Do not echo back code the user can already see; summarize changes instead

## What This Is

A real-time streaming dashboard that monitors OBS stats, YouTube live viewer counts/chat, and GPU utilisation. It compiles to a single native binary (Windows/Linux/macOS) with no runtime dependencies. Serves a responsive web UI designed for phone use during streams.

## Building & Running

Build targets are defined in `magefile.go` using [Mage](https://magefile.org/) — a cross-platform build tool written in Go.

```bash
mage build    # compile for current platform → dist/stream_monitor[.exe]
mage run      # build and run
mage test     # run all tests
mage vet      # go vet ./...
mage fmt      # gofmt -w .
mage lint     # staticcheck (falls back to go vet)
mage windows  # cross-compile → dist/stream_monitor-windows-amd64.exe
mage linux    # cross-compile → dist/stream_monitor-linux-amd64
mage darwin   # cross-compile → dist/stream_monitor-darwin-arm64
mage clean    # remove dist/
```

**Install Mage:** `go install github.com/magefile/mage@latest` (requires Go).

The server starts on port 8888. No YouTube API key is required — it scrapes public pages. Static files (HTML/CSS/JS) are embedded into the binary via `//go:embed`.

## Project Structure

```
stream_monitor/
├── main.go                          # entry point — wires packages, starts goroutines
├── go.mod
├── magefile.go
├── CLAUDE.md
├── static/                          # embedded frontend (HTML/CSS/JS)
│   ├── index.html
│   ├── css/style.css
│   └── js/app.js
└── internal/                        # application packages (not importable externally)
    ├── state/state.go               # shared state structs (OBSState, YTState, GPUState)
    ├── config/config.go             # JSON config persistence + interactive prompts
    ├── obs/obs.go                   # OBS WebSocket v5 client (RFC 6455)
    ├── youtube/youtube.go           # YouTube scraper + live chat via innertube
    ├── gpu/
    │   ├── gpu.go                   # common polling loop
    │   ├── gpu_windows.go           # HWiNFO shared memory → nvidia-smi fallback
    │   ├── gpu_linux.go             # nvidia-smi → sysfs fallback
    │   └── gpu_darwin.go            # IOKit (ioreg) → powermetrics fallback
    └── server/server.go             # HTTP server, static file routes, /stats endpoint
```

## Architecture

Single Go module with domain logic split into `internal/` packages. `main.go` wires everything together.

**Backend** — each `internal/` package owns one concern. Monitoring goroutines receive state pointers from `main.go` and write through exported `Mu` fields (`sync.RWMutex`). The HTTP server reads via `Snapshot()` methods:

- `internal/obs` — WebSocket v5 connection to OBS via raw TCP sockets (RFC 6455); polls stats every 1s, computes rolling 5s bitrate average; auto-reconnects with 3s backoff
- `internal/youtube` — accepts channel handles (`@handle`), video IDs, or full YouTube URLs; validates input at startup; scrapes `/@handle/live` or monitors a video directly; polls viewer count every 30s; live chat via innertube API scraping
- `internal/gpu` — common polling loop; platform-specific implementations in `gpu_windows.go` (HWiNFO shared memory → nvidia-smi fallback), `gpu_linux.go` (nvidia-smi → sysfs fallback), and `gpu_darwin.go` (IOKit via ioreg → powermetrics fallback)
- `internal/server` — `net/http` server; serves embedded static files and `/stats` JSON endpoint
- `internal/config` — persists OBS password and YouTube input (handle/video ID/URL) to `stream_monitor_config.json`
- `internal/state` — shared state structs (`OBSState`, `YTState`, `GPUState`) with `sync.RWMutex` for safe concurrent access

**Frontend** — pure vanilla JS/CSS, no build step (embedded via `//go:embed static/*` in `main.go`):

- `static/index.html` — two-column layout (stats left, chat right on desktop; stacked on mobile)
- `static/js/app.js` — polls `/stats` every 250ms; per-digit slot-machine animations for stat values; auto-scrolling chat; server-lost detection after 3 consecutive failures
- `static/css/style.css` — CSS custom properties for dark/light theming; responsive breakpoints at 900px and 1400px

**Key pattern**: `main.go` creates state structs and passes pointers to each package's `Run()` function. Goroutines access fields through `RLock`/`Lock` on the struct's exported `Mu` field.

## Code Style

Every file is written to be human-readable. All Go functions have doc comments (godoc style), all JS functions have JSDoc comments, and CSS sections are separated by labeled comment headers. When adding or modifying code, maintain this convention — every exported and unexported function must be documented.

## Frontend Routing

The web server maps URL paths to embedded static files in `internal/server/server.go`:
- `/` and `/index.html` → `static/index.html`
- `/css/style.css` → `static/css/style.css`
- `/js/app.js` → `static/js/app.js`
- `/stats` → JSON snapshot of OBS, YouTube, and GPU state

If you add new static files, you must add a corresponding route in `server.Run()`.

## Platform-Specific Code

GPU monitoring uses Go build tags for platform separation:
- `internal/gpu/gpu_windows.go` (`//go:build windows`) — HWiNFO shared memory via `syscall`, falls back to `nvidia-smi`
- `internal/gpu/gpu_linux.go` (`//go:build linux`) — `nvidia-smi`, falls back to `/sys/class/drm` sysfs
- `internal/gpu/gpu_darwin.go` (`//go:build darwin`) — IOKit via `ioreg` (Apple Silicon & Intel), falls back to `powermetrics` (requires sudo)
