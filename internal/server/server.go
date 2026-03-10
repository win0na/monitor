// Package server provides the HTTP server for the Stream Monitor.
//
// Serves the embedded static frontend (HTML/CSS/JS) and a /stats JSON
// endpoint that returns the current state of OBS, YouTube, and GPU monitors.
package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"stream_monitor/internal/state"
)

// contentTypes maps file extensions to MIME types for served static files.
var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
}

// Run starts the HTTP server on the given port (blocking).
// staticFS must contain the embedded static/ directory.
func Run(port int, staticFS fs.FS, obs *state.OBSState, yt *state.YTState, gpu *state.GPUState) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Strip query string for routing (supports cache-busting ?_=...)
		urlPath := strings.SplitN(r.URL.Path, "?", 2)[0]

		switch urlPath {
		case "/", "/index.html":
			serveStatic(w, staticFS, "static/index.html")
		case "/css/style.css":
			serveStatic(w, staticFS, "static/css/style.css")
		case "/js/app.js":
			serveStatic(w, staticFS, "static/js/app.js")
		case "/stats":
			serveStats(w, obs, yt, gpu)
		default:
			w.WriteHeader(404)
		}
	})

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	server := &http.Server{Addr: addr, Handler: mux}

	for {
		err := server.ListenAndServe()
		if err != nil {
			fmt.Printf("  Server error: %v — restarting...\n", err)
		}
	}
}

// serveStatic serves an embedded static file with appropriate headers.
func serveStatic(w http.ResponseWriter, staticFS fs.FS, filepath string) {
	data, err := fs.ReadFile(staticFS, filepath)
	if err != nil {
		w.WriteHeader(404)
		return
	}

	ext := path.Ext(filepath)
	ct, ok := contentTypes[ext]
	if !ok {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// serveStats serializes current monitor state as JSON and sends it.
func serveStats(w http.ResponseWriter, obs *state.OBSState, yt *state.YTState, gpu *state.GPUState) {
	snap := map[string]any{
		"obs":   obs.Snapshot(),
		"yt":    yt.Snapshot(),
		"gpu":   gpu.Snapshot(),
		"_boot": fmt.Sprintf("%d", state.BootTime),
	}

	body, err := json.Marshal(snap)
	if err != nil {
		body, _ = json.Marshal(map[string]string{"error": err.Error()})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.Write(body)
}
