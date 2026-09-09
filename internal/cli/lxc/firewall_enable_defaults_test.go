package lxc

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
	"github.com/stretchr/testify/require"
)

func TestFirewallRuleEnableWireDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		update bool
		flags  []string
		want   string
		reject bool
	}{
		{name: "create_default", want: "1"},
		{name: "unsupported_create_position", flags: []string{"--pos", "2"}, reject: true},
		{name: "create_disabled", flags: []string{"--enable", "0"}, want: "0"},
		{name: "create_enabled", flags: []string{"--enable", "1"}, want: "1"},
		{name: "update_omitted", update: true},
		{name: "update_disabled", update: true, flags: []string{"--enable", "0"}, want: "0"},
		{name: "update_enabled", update: true, flags: []string{"--enable", "1"}, want: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			method, path := http.MethodPost, "/api2/json/nodes/pve1/lxc/100/firewall/rules"
			args := []string{"firewall", "rules", "create", "100", "--type", "in", "--action", "ACCEPT"}
			if tc.update {
				method = http.MethodPut
				path += "/0"
				args = []string{"firewall", "rules", "update", "100", "0", "--comment", "keep activation unchanged"}
			}
			args = append(args, tc.flags...)
			var got url.Values
			f.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseForm())
				got = r.Form
				testhelper.WriteData(w, nil)
			})
			deps := newDeps(t, f, output.FormatJSON, "pve1", false)
			var buf bytes.Buffer
			err := newTestCmd(t, deps, &buf, args...)()
			if tc.reject {
				require.ErrorContains(t, err, "unknown flag: --pos")
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			require.NotContains(t, got, "pos")
			if tc.want == "" {
				require.NotContains(t, got, "enable")
			} else {
				require.Equal(t, []string{tc.want}, got["enable"])
			}
		})
	}
}
