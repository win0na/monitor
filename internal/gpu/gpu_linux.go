// GPU utilisation monitor — Linux implementation via nvidia-smi.
//
// Reads GPU utilisation by calling nvidia-smi. Falls back to
// /sys/class/drm sysfs entries for AMD/Intel GPUs.

//go:build linux

package gpu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// readGPU reads GPU utilisation. Tries nvidia-smi first, then sysfs.
func readGPU() (*float64, *string) {
	if pct, label := readNvidiaSMI(); pct != nil {
		return pct, label
	}
	return readSysfs()
}

// readNvidiaSMI reads GPU utilisation via nvidia-smi.
func readNvidiaSMI() (*float64, *string) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=utilization.gpu",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, nil
	}
	text := strings.TrimSpace(string(out))
	var pct float64
	if _, err := fmt.Sscanf(text, "%f", &pct); err == nil {
		label := "nvidia-smi"
		return &pct, &label
	}
	return nil, nil
}

// readSysfs reads GPU utilisation from /sys/class/drm for AMD/Intel GPUs.
func readSysfs() (*float64, *string) {
	// AMD: /sys/class/drm/card0/device/gpu_busy_percent
	matches, _ := filepath.Glob("/sys/class/drm/card*/device/gpu_busy_percent")
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var pct float64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%f", &pct); err == nil {
			label := "sysfs"
			return &pct, &label
		}
	}

	msg := "no GPU monitor available"
	return nil, &msg
}
