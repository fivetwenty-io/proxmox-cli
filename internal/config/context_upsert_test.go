package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseInput() LabContextInput {
	return LabContextInput{
		Host:        "10.10.1.10",
		Port:        8006,
		Username:    "pmx@pve",
		TokenID:     "pmx",
		Secret:      "keychain:pmx-lab-demo/pmx@pve!pmx",
		Fingerprint: "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99",
		DefaultNode: "lab-demo-0",
		MgmtSubnet:  "10.10.1.0/24",
	}
}

func TestUpsertLabContext_CreatesFreshContext(t *testing.T) {
	cfg := &Config{}
	changed, err := UpsertLabContext(cfg, "lab-demo", baseInput())
	require.NoError(t, err)
	assert.NotEmpty(t, changed)

	ctx := cfg.Contexts["lab-demo"]
	require.NotNil(t, ctx)
	assert.Equal(t, "10.10.1.10", ctx.Host)
	assert.Equal(t, 8006, ctx.Port)
	assert.Equal(t, "https", ctx.Protocol)
	assert.Equal(t, ProductPVE, ctx.Product)
	assert.Equal(t, "token", ctx.Auth.Type)
	assert.Equal(t, "pmx@pve", ctx.Auth.Username)
	assert.Equal(t, "pmx", ctx.Auth.TokenID)
	assert.Equal(t, "keychain:pmx-lab-demo/pmx@pve!pmx", ctx.Auth.Secret)
	assert.Equal(t, "lab-demo-0", ctx.DefaultNode)
	assert.NotEmpty(t, ctx.TLS.Fingerprint)
}

func TestUpsertLabContext_UpdatesCredsPreservesUserEdits(t *testing.T) {
	cfg := &Config{Contexts: map[string]*Context{
		"lab-demo": {
			Host:          "10.10.1.10",
			Port:          8006,
			Protocol:      "https",
			Product:       ProductPVE,
			DefaultNode:   "hand-edited-node",
			DefaultOutput: "json",
			Auth:          AuthBlock{Type: "token", Username: "pmx@pve", TokenID: "pmx", Secret: "old"},
			SSH:           SSHBlock{User: "labuser", Port: 2222},
			TLS:           TLSBlock{Fingerprint: "old", Tofu: true},
		},
	}}

	in := baseInput()
	in.Secret = "keychain:pmx-lab-demo/pmx@pve!pmx"
	changed, err := UpsertLabContext(cfg, "lab-demo", in)
	require.NoError(t, err)

	ctx := cfg.Contexts["lab-demo"]
	// Overwritten:
	assert.Equal(t, "keychain:pmx-lab-demo/pmx@pve!pmx", ctx.Auth.Secret)
	assert.NotEqual(t, "old", ctx.TLS.Fingerprint)
	assert.Contains(t, changed, "secret")
	// Preserved user edits:
	assert.Equal(t, "hand-edited-node", ctx.DefaultNode)
	assert.Equal(t, "json", ctx.DefaultOutput)
	assert.Equal(t, "labuser", ctx.SSH.User)
	assert.Equal(t, 2222, ctx.SSH.Port)
	assert.True(t, ctx.TLS.Tofu)
}

func TestUpsertLabContext_OwnershipGuardRefusesUnrelated(t *testing.T) {
	cfg := &Config{Contexts: map[string]*Context{
		"lab-demo": {
			Host:    "192.0.2.50", // outside 10.10.1.0/24
			Product: ProductPVE,
			Auth:    AuthBlock{Type: "token", Username: "wayne@pve", TokenID: "prod", Secret: "keep"},
		},
	}}

	_, err := UpsertLabContext(cfg, "lab-demo", baseInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not look lab-derived")
	// Unrelated context untouched.
	assert.Equal(t, "keep", cfg.Contexts["lab-demo"].Auth.Secret)
}

func TestUpsertLabContext_OwnedByMgmtSubnetHostMatch(t *testing.T) {
	cfg := &Config{Contexts: map[string]*Context{
		"lab-demo": {
			Host:    "10.10.1.10", // inside 10.10.1.0/24 → owned even though username differs
			Product: ProductPVE,
			Auth:    AuthBlock{Type: "token", Username: "someone@pve", TokenID: "t", Secret: "old"},
		},
	}}

	_, err := UpsertLabContext(cfg, "lab-demo", baseInput())
	require.NoError(t, err)
	assert.Equal(t, "keychain:pmx-lab-demo/pmx@pve!pmx", cfg.Contexts["lab-demo"].Auth.Secret)
}

func TestUpsertLabContext_SkipsFingerprintWhenInsecure(t *testing.T) {
	cfg := &Config{Contexts: map[string]*Context{
		"lab-demo": {
			Host:    "10.10.1.10",
			Product: ProductPVE,
			Auth:    AuthBlock{Type: "token", Username: "pmx@pve", TokenID: "pmx", Secret: "old"},
			TLS:     TLSBlock{Insecure: true},
		},
	}}

	changed, err := UpsertLabContext(cfg, "lab-demo", baseInput())
	require.NoError(t, err)
	assert.Empty(t, cfg.Contexts["lab-demo"].TLS.Fingerprint, "must not pin a fingerprint when Insecure is set")
	assert.NotContains(t, changed, "fingerprint")
}
