package cli_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
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
	var recovered any

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

// TestParseIndexedValues covers the INDEX=VALUE parser shared by the qemu, lxc,
// and node command trees.
func TestParseIndexedValues(t *testing.T) {
	got, err := cli.ParseIndexedValues([]string{"0=local-lvm:8", "2=virtio,bridge=vmbr0"}, "scsi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[int]string{0: "local-lvm:8", 2: "virtio,bridge=vmbr0"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("index %d: got %q, want %q", k, got[k], v)
		}
	}

	if _, err := cli.ParseIndexedValues(nil, "scsi"); err != nil {
		t.Errorf("empty input must not error: %v", err)
	}
}

func TestParseIndexedValues_Errors(t *testing.T) {
	cases := map[string][]string{
		"want INDEX=VALUE":             {"local-lvm:8"},
		"index must be a non-negative": {"-1=x"},
		"non-negative integer":         {"abc=x"},
		"specified more than once":     {"0=a", "0=b"},
	}
	for wantMsg, vals := range cases {
		_, err := cli.ParseIndexedValues(vals, "scsi")
		if err == nil {
			t.Errorf("%q: expected error for %v, got nil", wantMsg, vals)
			continue
		}
		if !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("error %q does not contain %q", err.Error(), wantMsg)
		}
	}
}

func TestStringifyValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		// A value the server never sent must read as absent. Through fmt's %v
		// this arrived in a table cell as the literal "<nil>".
		{name: "nil", in: nil, want: ""},
		{name: "string", in: "local-lvm:vm-102-disk-0", want: "local-lvm:vm-102-disk-0"},
		{name: "empty string", in: "", want: ""},
		{name: "bool true", in: true, want: "true"},
		{name: "bool false", in: false, want: "false"},
		{name: "whole float", in: float64(512), want: "512"},
		// %v renders this one as 1.048576e+06.
		{name: "large whole float", in: float64(1048576), want: "1048576"},
		{name: "fractional float", in: 2.5, want: "2.5"},
		{name: "object", in: map[string]any{"a": float64(1)}, want: `{"a":1}`},
		{name: "array", in: []any{"a", "b"}, want: `["a","b"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, cli.StringifyValue(tc.in))
		})
	}
}
