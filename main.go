// Stream Monitor — Entry Point
//
// Prompts for OBS and YouTube configuration, launches all monitoring
// goroutines, and starts the HTTP server to serve the web dashboard.
//
// Usage:
//
//	go run .
//	# or build and run:
//	go build -o stream_monitor.exe . && ./stream_monitor.exe
package main

import (
	"embed"
	"fmt"
	"net"

	"stream_monitor/internal/config"
	"stream_monitor/internal/gpu"
	"stream_monitor/internal/obs"
	"stream_monitor/internal/server"
	"stream_monitor/internal/state"
	"stream_monitor/internal/youtube"
)

//go:embed static/*
var staticFS embed.FS

const httpPort = 8888

func main() {
	cfg := config.Load()

	fmt.Println()
	fmt.Println("  OBS + YouTube Stream Monitor")
	fmt.Println("  ─────────────────────────────")
	fmt.Println("  (Press Enter to keep saved value)")
	fmt.Println()

	obsPass := config.Prompt("OBS WebSocket password (blank if none)", cfg["obs_pass"], true)
	ytInput := config.Prompt("YouTube channel or video (@handle / video ID / URL)", cfg["yt_channel"], false)
	fmt.Println()

	// Validate YouTube input
	ytValid := false
	if ytInput != "" {
		kind, value := youtube.ParseInput(ytInput)
		switch kind {
		case "channel":
			fmt.Printf("  Validating channel @%s...\n", value)
			if youtube.ValidateChannel(value) {
				fmt.Printf("  ✓ Channel @%s found\n", value)
				ytValid = true
			} else {
				fmt.Printf("  ✗ Channel @%s not found — YouTube monitoring disabled\n", value)
			}
		case "video":
			fmt.Printf("  Validating video %s...\n", value)
			if youtube.ValidateVideo(value) {
				fmt.Printf("  ✓ Video %s is live\n", value)
				ytValid = true
			} else {
				fmt.Printf("  ✗ Video %s not found or not live — YouTube monitoring disabled\n", value)
			}
		default:
			fmt.Println("  ✗ Unrecognised input — YouTube monitoring disabled")
		}
		fmt.Println()
	}

	// Save config if changed
	newCfg := map[string]string{"obs_pass": obsPass, "yt_channel": ytInput}
	if newCfg["obs_pass"] != cfg["obs_pass"] || newCfg["yt_channel"] != cfg["yt_channel"] {
		config.Save(newCfg)
	}

	// Initialise shared state
	obsState := state.NewOBSState()
	ytState := state.NewYTState()
	gpuState := &state.GPUState{}

	// Start monitoring goroutines
	go obs.Run(obsPass, obsState)
	go gpu.Run(gpuState)

	if ytValid {
		go youtube.RunStats(ytInput, ytState)
		go youtube.RunChat(ytInput, ytState)
	} else if ytInput == "" {
		fmt.Println("  (YouTube skipped — no input provided)")
	}

	// Print the local address for mobile access
	localIP := "your-pc-ip"
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				localIP = ipNet.IP.String()
				break
			}
		}
	}

	fmt.Printf("  Open on your phone:\n")
	fmt.Printf("    http://%s:%d\n\n", localIP, httpPort)

	// Start the HTTP server (blocking)
	server.Run(httpPort, staticFS, obsState, ytState, gpuState)
}
