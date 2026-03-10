//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/magefile/mage/sh"
)

const (
	app  = "stream_monitor"
	dist = "dist"
)

// binary returns the output path for a given GOOS/GOARCH.
func binary(goos, goarch string) string {
	name := fmt.Sprintf("%s-%s-%s", app, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(dist, name)
}

// currentBinary returns the output path for the current platform.
func currentBinary() string {
	name := app
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dist, name)
}

// ensureDist creates the dist directory if it doesn't exist.
func ensureDist() error {
	return os.MkdirAll(dist, 0o755)
}

// Build compiles for the current platform.
func Build() error {
	if err := ensureDist(); err != nil {
		return err
	}
	out := currentBinary()
	os.Remove(out)
	fmt.Println("building", out)
	return sh.RunV("go", "build", "-o", out, ".")
}

// Run builds and runs the application.
func Run() error {
	if err := Build(); err != nil {
		return err
	}
	return sh.RunV(currentBinary())
}

// Windows cross-compiles for Windows amd64.
func Windows() error {
	if err := ensureDist(); err != nil {
		return err
	}
	out := binary("windows", "amd64")
	os.Remove(out)
	fmt.Println("building", out)
	return sh.RunWith(map[string]string{"GOOS": "windows", "GOARCH": "amd64"}, "go", "build", "-o", out, ".")
}

// Linux cross-compiles for Linux amd64.
func Linux() error {
	if err := ensureDist(); err != nil {
		return err
	}
	out := binary("linux", "amd64")
	os.Remove(out)
	fmt.Println("building", out)
	return sh.RunWith(map[string]string{"GOOS": "linux", "GOARCH": "amd64"}, "go", "build", "-o", out, ".")
}

// Darwin cross-compiles for macOS arm64.
func Darwin() error {
	if err := ensureDist(); err != nil {
		return err
	}
	out := binary("darwin", "arm64")
	os.Remove(out)
	fmt.Println("building", out)
	return sh.RunWith(map[string]string{"GOOS": "darwin", "GOARCH": "arm64"}, "go", "build", "-o", out, ".")
}

// Test runs all tests.
func Test() error {
	return sh.RunV("go", "test", "./...")
}

// Vet runs go vet on all packages.
func Vet() error {
	return sh.RunV("go", "vet", "./...")
}

// Fmt formats all Go source files.
func Fmt() error {
	return sh.RunV("gofmt", "-w", ".")
}

// Lint runs golangci-lint if available, falls back to staticcheck, then go vet.
func Lint() error {
	if _, err := exec.LookPath("golangci-lint"); err == nil {
		return sh.RunV("golangci-lint", "run", "./...")
	}
	fmt.Println("golangci-lint not found, trying staticcheck")
	if _, err := exec.LookPath("staticcheck"); err == nil {
		return sh.RunV("staticcheck", "./...")
	}
	fmt.Println("staticcheck not found, running go vet instead")
	return Vet()
}

// Coverage runs tests with coverage reporting.
func Coverage() error {
	return sh.RunV("go", "test", "-cover", "./...")
}

// Clean removes build artifacts.
func Clean() error {
	fmt.Println("removing", dist)
	return os.RemoveAll(dist)
}
