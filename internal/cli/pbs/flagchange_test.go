package pbs

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestAnyFlagChanged_IgnoresInheritedPersistentFlags confirms that a flag
// inherited from a parent's persistent flag set never counts as a requested
// change, while a flag local to the command itself still does.
func TestAnyFlagChanged_IgnoresInheritedPersistentFlags(t *testing.T) {
	var out, comment string
	parent := &cobra.Command{Use: "parent"}
	parent.PersistentFlags().StringVarP(&out, "output", "o", "", "")
	var got bool
	child := &cobra.Command{
		Use:  "child",
		RunE: func(cmd *cobra.Command, _ []string) error { got = anyFlagChanged(cmd); return nil },
	}
	child.Flags().StringVar(&comment, "comment", "", "")
	parent.AddCommand(child)

	parent.SetArgs([]string{"child", "-o", "json"})
	require.NoError(t, parent.Execute())
	require.False(t, got, "an inherited --output is not a requested change")

	parent.SetArgs([]string{"child", "--comment", "x"})
	require.NoError(t, parent.Execute())
	require.True(t, got, "a local flag still counts")
}

// TestS3Update_IgnoresInheritedPersistentOutputFlag is the end-to-end version
// of the same guarantee: `s3 update <id> -o json` must reject the call for
// lack of a real change rather than sending an empty PUT for --output alone.
func TestS3Update_IgnoresInheritedPersistentOutputFlag(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+s3ConfigPath+"/"+s3ID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := runWithParent(deps, &buf, newS3Cmd(), "s3", "update", s3ID, "-o", "json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
	require.Empty(t, rec.method, "an inherited --output must not trigger a PUT")
}
