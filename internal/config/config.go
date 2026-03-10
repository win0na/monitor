// Package config handles persistence of user settings for the Stream Monitor.
//
// Saves and loads OBS password and YouTube channel handle to a JSON file
// next to the executable so they survive restarts.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// configPath returns the path to the config JSON file,
// located next to the running executable.
func configPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "stream_monitor_config.json"
	}
	return filepath.Join(filepath.Dir(exe), "stream_monitor_config.json")
}

// Load reads saved configuration from disk, returning an empty map on failure.
func Load() map[string]string {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return map[string]string{}
	}
	var cfg map[string]string
	if err := json.Unmarshal(data, &cfg); err != nil {
		return map[string]string{}
	}
	return cfg
}

// Save writes configuration to disk as formatted JSON.
func Save(cfg map[string]string) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	p := configPath()
	if err := os.WriteFile(p, data, 0o600); err == nil {
		fmt.Printf("  ✓ Settings saved to %s\n", p)
	}
}

// Prompt asks the user for a config value, showing the saved default.
// If secret is true the saved value is masked. Pressing Enter keeps the saved value.
func Prompt(label, saved string, secret bool) string {
	if saved != "" {
		display := saved
		if secret {
			n := len(saved)
			if n > 6 {
				n = 6
			}
			display = strings.Repeat("*", n) + "..."
		}
		fmt.Printf("  %s [%s]: ", label, display)
	} else {
		fmt.Printf("  %s: ", label)
	}

	var line string
	_, _ = fmt.Scanln(&line)
	line = strings.TrimSpace(line)
	if line == "" {
		return saved
	}
	return line
}
