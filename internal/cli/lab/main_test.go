package lab

import (
	"fmt"
	"os"
	"testing"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestMain clears the eight PMX_API_* variables before any test runs, so an
// override exported in the operator's shell cannot change which endpoint,
// proxy, bastion, or timeout a test resolves. A test that needs one sets it
// with t.Setenv.
//
// It also pins labKeychainSupported to true, so the context-cleanup plans the
// destroy and context rm tests assert are the same on every build platform;
// the unsupported-platform plan is covered by a test that overrides it back.
// The keychain store and delete seams default to failing stubs, so a test that
// forgets to fake them fails instead of touching the operator's real keychain.
func TestMain(m *testing.M) {
	labKeychainSupported = func() bool { return true }
	labDeleteSecretFn = func(service, account string) error {
		return fmt.Errorf("test reached the real keychain delete for %s/%s; fake labDeleteSecretFn", service, account)
	}
	labStoreSecretFn = func(_ *cobra.Command, _ *cli.Deps, service, account, _ string) (string, error) {
		return "", fmt.Errorf("test reached the real keychain store for %s/%s; fake labStoreSecretFn", service, account)
	}
	restore := testhelper.UnsetAPIEnv()
	code := m.Run()
	restore()

	os.Exit(code)
}
