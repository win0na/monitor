// Package gpu provides GPU utilisation monitoring for the Stream Monitor.
//
// Platform-specific implementations live in gpu_windows.go and gpu_linux.go.
// This file contains the common polling loop.
package gpu

import (
	"fmt"
	"time"

	"stream_monitor/internal/state"
)

// Run continuously polls GPU utilisation (blocking, runs in a goroutine).
// Updates the given GPUState every 2 seconds with the latest reading.
func Run(s *state.GPUState) {
	reported := false
	for {
		pct, label := readGPU()

		s.Mu.Lock()
		s.Pct = pct
		s.Label = label
		s.Mu.Unlock()

		if pct == nil && !reported {
			reason := "unknown"
			if label != nil {
				reason = *label
			}
			fmt.Printf("  GPU unavailable: %s\n", reason)
			reported = true
		} else if pct != nil {
			reported = false
		}

		time.Sleep(2 * time.Second)
	}
}
