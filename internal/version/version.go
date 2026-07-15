// Package version exposes CLI build information (Version, Commit, Date) set via ldflags at build time.
package version

import (
	"fmt"
	"runtime"
)

// Version is the human-readable release string. Override at build time via
// -ldflags "-X github.com/fivetwenty-io/proxmox-cli/internal/version.Version=<ver>".
// It is a var (not a const) so the linker's -X flag can inject the release tag.
var Version = "0.1.0-dev"

// Commit is the VCS commit hash injected at build time via ldflags.
// Falls back to "unknown" when the binary is built without ldflags.
var Commit = "unknown"

// Date is the build timestamp injected at build time via ldflags (RFC 3339 preferred).
// Falls back to "unknown" when the binary is built without ldflags.
var Date = "unknown"

// Info holds the complete set of build and runtime metadata for the CLI binary.
type Info struct {
	// Version is the human-readable release tag (e.g. "0.1.0-dev").
	Version string
	// Commit is the VCS commit hash at build time.
	Commit string
	// Date is the build timestamp.
	Date string
	// GoVersion is the Go toolchain version used to compile the binary.
	GoVersion string
	// OS is the target operating system (GOOS).
	OS string
	// Arch is the target CPU architecture (GOARCH).
	Arch string
}

// GetInfo returns a populated Info struct using the package-level variables and
// runtime build information. It never returns an error; zero values are used
// when metadata is unavailable.
func GetInfo() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// String returns a single human-readable line summarising the build info.
// Format: "pmx version <Version> (commit: <Commit>, built: <Date>, go: <GoVersion>, os/arch: <OS>/<Arch>)"
func String() string {
	i := GetInfo()
	return fmt.Sprintf(
		"pmx version %s (commit: %s, built: %s, go: %s, os/arch: %s/%s)",
		i.Version, i.Commit, i.Date, i.GoVersion, i.OS, i.Arch,
	)
}
