package pdm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewConfigCmd_RegistersAllSubcommands asserts that `pdm config` wires
// up every documented subgroup.
func TestNewConfigCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newConfigCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"acme", "certificate", "notes", "view", "webauthn"} {
		require.True(t, names[want], "expected `config %s` to be registered", want)
	}
}
