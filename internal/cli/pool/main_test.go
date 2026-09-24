package pool

import (
	"os"
	"testing"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestMain clears the eight PMX_API_* variables before any test runs, so an
// override exported in the operator's shell cannot change which endpoint,
// proxy, bastion, or timeout a test resolves. A test that needs one sets it
// with t.Setenv.
func TestMain(m *testing.M) {
	restore := testhelper.UnsetAPIEnv()
	code := m.Run()
	restore()

	os.Exit(code)
}
