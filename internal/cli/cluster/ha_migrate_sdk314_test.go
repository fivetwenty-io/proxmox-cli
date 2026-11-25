package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func haMigrateForms(t *testing.T, argSets ...[]string) []url.Values {
	t.Helper()
	f, ac := newFakeClient(t)
	var forms []url.Values
	f.HandleFunc("POST /api2/json/cluster/ha/resources/vm:100/migrate", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms = append(forms, r.Form)
		testhelper.WriteData(w, map[string]any{"sid": "vm:100", "requested-node": "pve2"})
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	for _, args := range argSets {
		var buf bytes.Buffer
		require.NoError(t, run(deps, &buf, args...))
	}
	return forms
}

func TestHaResourceMigrate_ForwardsNewFlags(t *testing.T) {
	forms := haMigrateForms(t, []string{"ha", "resource", "migrate", "vm:100", "--target-node", "pve2",
		"--resource-type", "vm", "--skip-config-check", "--with-conntrack-state"})
	require.Len(t, forms, 1)
	require.Equal(t, "vm", forms[0].Get("resource-type"))
	require.Equal(t, "1", forms[0].Get("skip-config-check"))
	require.Equal(t, "1", forms[0].Get("with-conntrack-state"))
}

// TestHaResourceMigrate_OmitsUnsetFlags covers ruling 10: the resource type is
// sent only when the user gives one.
func TestHaResourceMigrate_OmitsUnsetFlags(t *testing.T) {
	forms := haMigrateForms(t, []string{"ha", "resource", "migrate", "vm:100", "--target-node", "pve2"})
	require.Len(t, forms, 1)
	for _, key := range []string{"resource-type", "skip-config-check", "with-conntrack-state"} {
		require.False(t, forms[0].Has(key), "%q must be omitted when unset", key)
	}
	require.Equal(t, "pve2", forms[0].Get("node"))
}
