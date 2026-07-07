package version_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/fivetwenty-io/pmx-cli/internal/version"
	"github.com/stretchr/testify/require"
)

func TestGetInfo_Defaults(t *testing.T) {
	info := version.GetInfo()

	require.Equal(t, "0.1.0-dev", info.Version)
	require.Equal(t, "unknown", info.Commit)
	require.Equal(t, "unknown", info.Date)
	require.Equal(t, runtime.Version(), info.GoVersion)
	require.Equal(t, runtime.GOOS, info.OS)
	require.Equal(t, runtime.GOARCH, info.Arch)
}

func TestGetInfo_FieldsNonEmpty(t *testing.T) {
	info := version.GetInfo()

	require.NotEmpty(t, info.Version, "Version must be non-empty")
	require.NotEmpty(t, info.GoVersion, "GoVersion must be non-empty")
	require.NotEmpty(t, info.OS, "OS must be non-empty")
	require.NotEmpty(t, info.Arch, "Arch must be non-empty")
}

func TestString_ContainsVersion(t *testing.T) {
	s := version.String()

	require.Contains(t, s, version.Version)
	require.Contains(t, s, "pmx version")
}

func TestString_ContainsAllFields(t *testing.T) {
	s := version.String()

	require.True(t, strings.Contains(s, "commit:"), "String() must contain commit label")
	require.True(t, strings.Contains(s, "built:"), "String() must contain built label")
	require.True(t, strings.Contains(s, "go:"), "String() must contain go label")
	require.True(t, strings.Contains(s, "os/arch:"), "String() must contain os/arch label")
}

func TestString_ContainsRuntimeFields(t *testing.T) {
	s := version.String()

	require.Contains(t, s, runtime.Version())
	require.Contains(t, s, runtime.GOOS)
	require.Contains(t, s, runtime.GOARCH)
}

func TestVersion_Const(t *testing.T) {
	// Version constant must be non-empty and follow semver convention (starts with digit).
	require.NotEmpty(t, version.Version)
	require.True(t, version.Version[0] >= '0' && version.Version[0] <= '9',
		"Version must start with a digit, got: %s", version.Version)
}

func TestLdflags_CommitAndDate_Injectable(t *testing.T) {
	// The package-level vars are addressable; verify that setting them is reflected
	// in GetInfo() output — simulates what ldflags does at link time.
	orig := version.Commit
	origDate := version.Date

	t.Cleanup(func() {
		version.Commit = orig
		version.Date = origDate
	})

	version.Commit = "abc1234"
	version.Date = "2026-06-02T00:00:00Z"

	info := version.GetInfo()
	require.Equal(t, "abc1234", info.Commit)
	require.Equal(t, "2026-06-02T00:00:00Z", info.Date)

	s := version.String()
	require.Contains(t, s, "abc1234")
	require.Contains(t, s, "2026-06-02T00:00:00Z")
}
