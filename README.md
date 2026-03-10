# Stream Monitor

A real-time streaming dashboard that monitors OBS stats, YouTube live viewers/chat, and GPU utilisation — all from your phone.

Single binary. No dependencies. No API keys.

<!-- TODO: Replace with actual screenshot -->
<!-- ![Dashboard Screenshot](docs/screenshot.png) -->

## Quick Start

```bash
make run
```

The dashboard starts at [localhost:8888](http://localhost:8888). Open it on your phone while streaming.

## Install

**Prerequisites:** [Go 1.24+](https://go.dev/dl/) and GNU Make

```bash
# Windows
winget install GnuWin32.Make

# Build
make
./dist/stream_monitor.exe
```

On first run, you'll be prompted for your OBS WebSocket password and YouTube channel.

## What It Monitors

| Source | Data | Update Interval |
|--------|------|-----------------|
| **OBS** | Bitrate, FPS, dropped frames, CPU usage, memory | 1s |
| **YouTube** | Live viewer count, live chat messages | 30s / live |
| **GPU** | Utilisation %, GPU name | 2s |

## How It Works

- Connects to OBS via WebSocket v5 (raw TCP, RFC 6455)
- Scrapes YouTube public pages — no API key needed
- Reads GPU stats from HWiNFO shared memory (Windows) or nvidia-smi (Linux)
- Serves an embedded web UI with slot-machine number animations and auto-scrolling chat

<details>
<summary><strong>Architecture</strong></summary>

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

Each package runs as a goroutine, writing to shared state behind `sync.RWMutex`. The HTTP server reads snapshots and serves them as JSON.

</details>

<details>
<summary><strong>Build Targets</strong></summary>

```bash
make          # vet + build → dist/stream_monitor.exe
make run      # build and run
make test     # run all tests
make fmt      # format code
make lint     # staticcheck or go vet
make linux    # cross-compile → dist/stream_monitor-linux-amd64
make darwin   # cross-compile → dist/stream_monitor-darwin-arm64
make clean    # remove dist/
```

</details>

<details>
<summary><strong>Platform Support</strong></summary>

| Platform | GPU Monitoring | Status |
|----------|---------------|--------|
| Windows | HWiNFO shared memory → nvidia-smi | Full support |
| Linux | nvidia-smi → sysfs | Full support |
| macOS | — | Builds, no GPU stats |

</details>

## License

MIT
