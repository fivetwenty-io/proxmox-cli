package cli_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// newTestCmd builds a minimal cobra.Command with one string flag for testing.
func newTestCmd(flagName string) *cobra.Command {
	cmd := &cobra.Command{Use: "test-cmd"}
	cmd.Flags().String(flagName, "", "test flag")
	return cmd
}

// TestMustMarkRequired_DefinedFlag verifies that calling MustMarkRequired on a
// flag that exists does not panic and sets the required annotation on the flag.
func TestMustMarkRequired_DefinedFlag(t *testing.T) {
	cmd := newTestCmd("vmid")

	// Must not panic.
	cli.MustMarkRequired(cmd, "vmid")

	// Confirm the cobra required annotation is present.
	f := cmd.Flags().Lookup("vmid")
	if f == nil {
		t.Fatal("flag 'vmid' not found after registration")
		return
	}
	annotations := f.Annotations
	if annotations == nil {
		t.Fatal("flag annotations map is nil — required annotation not set")
	}
	if _, ok := annotations[cobra.BashCompOneRequiredFlag]; !ok {
		t.Errorf("required annotation %q not set on flag 'vmid'; got annotations: %v",
			cobra.BashCompOneRequiredFlag, annotations)
	}
}

// TestMustMarkRequired_UndefinedFlag verifies that calling MustMarkRequired on
// a flag that does not exist causes a panic whose message contains the flag name.
func TestMustMarkRequired_UndefinedFlag(t *testing.T) {
	cmd := newTestCmd("vmid")

	missingFlag := "nonexistent"
	var recovered interface{}

	func() {
		defer func() {
			recovered = recover()
		}()
		cli.MustMarkRequired(cmd, missingFlag)
	}()

	if recovered == nil {
		t.Fatal("expected panic for undefined flag, but no panic occurred")
	}

	msg, ok := recovered.(string)
	if !ok {
		t.Fatalf("expected panic value to be string, got %T: %v", recovered, recovered)
	}

	if !strings.Contains(msg, missingFlag) {
		t.Errorf("panic message does not contain flag name %q; got: %s", missingFlag, msg)
	}
}
