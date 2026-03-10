// GPU utilisation monitor — macOS implementation via IOKit.
//
// Reads GPU utilisation from IOKit's IOAccelerator performance statistics.
// Works on both Apple Silicon and Intel Macs without requiring sudo.
// Falls back to parsing the "GPU Core Utilization" from powermetrics
// if IOKit data is unavailable (requires running with sudo).

//go:build darwin

package gpu

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ioregGPUUtilRe matches "GPU Core Utilization(%)" or "Device Utilization %" in ioreg output.
var ioregGPUUtilRe = regexp.MustCompile(`(?i)(?:"?(?:GPU\s*Core\s*Utilization|Device\s*Utilization)\s*\(%?\)"?\s*=\s*)(\d+)`)

// gpuNameRe matches the GPU class name from ioreg.
var gpuNameRe = regexp.MustCompile(`"model"\s*=\s*<?"?([^"<>]+)"?>?`)

// readGPU reads GPU utilisation. Tries ioreg first, then powermetrics.
func readGPU() (*float64, *string) {
	if pct, label := readIOReg(); pct != nil {
		return pct, label
	}
	return readPowermetrics()
}

// readIOReg reads GPU utilisation from IOKit via ioreg.
// Queries IOAccelerator classes which expose performance statistics
// on both Apple Silicon and Intel Macs.
func readIOReg() (*float64, *string) {
	out, err := exec.Command("ioreg", "-r", "-c", "IOAccelerator", "-d", "3").Output()
	if err != nil {
		return nil, nil
	}
	output := string(out)

	// Find GPU name
	var gpuName string
	if m := gpuNameRe.FindStringSubmatch(output); len(m) > 1 {
		gpuName = strings.TrimSpace(m[1])
	}

	// Look for PerformanceStatistics section and extract utilisation
	// Apple Silicon reports "Device Utilization %" or "GPU Core Utilization(%)"
	sections := strings.Split(output, "PerformanceStatistics")
	for _, section := range sections[1:] { // skip everything before first PerformanceStatistics
		if m := ioregGPUUtilRe.FindStringSubmatch(section); len(m) > 1 {
			val, err := strconv.ParseFloat(m[1], 64)
			if err == nil {
				label := "ioreg"
				if gpuName != "" {
					label = gpuName
				}
				return &val, &label
			}
		}
	}

	return nil, nil
}

// readPowermetrics reads GPU utilisation via powermetrics (requires sudo).
// This is a fallback — it spawns a single short sample and parses the output.
func readPowermetrics() (*float64, *string) {
	out, err := exec.Command("sudo", "-n", "powermetrics",
		"--samplers", "gpu_power",
		"-n", "1",
		"--sample-rate", "1000").Output()
	if err != nil {
		msg := "no GPU monitor available (ioreg failed, powermetrics requires sudo)"
		return nil, &msg
	}

	// Parse "GPU Active Residency:   42.5%"
	re := regexp.MustCompile(`GPU\s+Active\s+Residency:\s+([\d.]+)%`)
	if m := re.FindStringSubmatch(string(out)); len(m) > 1 {
		val, err := strconv.ParseFloat(m[1], 64)
		if err == nil {
			label := "powermetrics"
			return &val, &label
		}
	}

	msg := fmt.Sprintf("could not parse GPU stats from powermetrics")
	return nil, &msg
}
