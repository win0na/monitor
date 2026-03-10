# Stream Monitor

A real-time streaming dashboard that monitors OBS stats, YouTube live viewers/chat, and GPU utilisation — all from your phone.

Single binary. No dependencies. No API keys.

<!-- TODO: Replace with actual screenshot -->
<!-- ![Dashboard Screenshot](docs/screenshot.png) -->

## Quick Start

```bash
mage run
```

The dashboard starts at [localhost:8888](http://localhost:8888). Open it on your phone while streaming.

## Install

**Prerequisites:** [Go 1.24+](https://go.dev/dl/)

```bash
go install github.com/magefile/mage@latest
mage build
./dist/stream_monitor.exe    # or .\dist\stream_monitor.exe on Windows
```

On first run, you'll be prompted for your OBS WebSocket password and YouTube channel.

## What It Monitors

**OBS** — bitrate, FPS, dropped frames, CPU usage, memory (updates every 1s)

**YouTube** — live viewer count and live chat messages (viewers every 30s, chat live)

**GPU** — utilisation % and GPU name (updates every 2s)

## How It Works

- Connects to OBS via WebSocket v5 (raw TCP, RFC 6455)
- Scrapes YouTube public pages — no API key needed
- Reads GPU stats from HWiNFO shared memory (Windows) or nvidia-smi (Linux)
- Serves an embedded web UI with slot-machine number animations and auto-scrolling chat

## Architecture

Each package runs as a goroutine, writing to shared state behind `sync.RWMutex`. The HTTP server reads snapshots and serves them as JSON.

```
main.go                     → wires packages, starts goroutines
internal/
  obs/obs.go                → OBS WebSocket v5 client, 3s auto-reconnect
  youtube/youtube.go        → scraper + innertube live chat
  gpu/gpu.go                → common polling loop
  gpu/gpu_windows.go        → HWiNFO shared memory → nvidia-smi fallback
  gpu/gpu_linux.go          → nvidia-smi → sysfs fallback
  server/server.go          → HTTP server, embedded static files, /stats JSON
  config/config.go          → JSON config persistence
  state/state.go            → thread-safe shared state structs
static/                     → vanilla HTML/CSS/JS (embedded into binary)
```

## Building

Uses [Mage](https://magefile.org/) — a cross-platform build tool written in Go.

```bash
mage build    # compile for current platform
mage run      # build and run
mage test     # run all tests
mage fmt      # format code
mage lint     # staticcheck or go vet
mage windows  # cross-compile → dist/stream_monitor-windows-amd64.exe
mage linux    # cross-compile → dist/stream_monitor-linux-amd64
mage darwin   # cross-compile → dist/stream_monitor-darwin-arm64
mage clean    # remove dist/
```

## Platform Support

**Windows** — full support, GPU via HWiNFO shared memory with nvidia-smi fallback

**Linux** — full support, GPU via nvidia-smi with sysfs fallback

**macOS** — builds and runs, but no GPU monitoring yet

## License

MIT
