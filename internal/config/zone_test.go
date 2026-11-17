package config_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// TestZoneIDIssue pins the SDN zone ID rule from the PVE API schema
// (minLength 2, maxLength 8, pattern [a-zA-Z][a-zA-Z0-9]*[a-zA-Z0-9]).
func TestZoneIDIssue(t *testing.T) {
	valid := []string{"labs", "labvxlan", "ab", "Z9", "a1b2c3d4", "clivlan", "LABS"}
	for _, id := range valid {
		assert.Empty(t, config.ZoneIDIssue("network.zone_name", id), "%q must be valid", id)
	}

	invalid := []struct {
		id   string
		want string
	}{
		{"labsvxlan", "length 9"},        // the reported case: one past the limit
		{"abcdefghi", "must be 2-8"},     // 9 letters
		{"", "length 0"},                 // empty, when a caller passes one explicitly
		{"a", "length 1"},                // below the minimum length of 2
		{"1abc", "start with a letter"},  // leading digit
		{"lab-vx", "letters and digits"}, // hyphen
		{"lab_vx", "letters and digits"}, // underscore
		{"lab.vx", "letters and digits"}, // dot
		{"lab vx", "letters and digits"}, // space
		{"labé", "letters and digits"},   // non-ASCII letter (5 bytes, still within 8)
	}
	for _, tc := range invalid {
		issue := config.ZoneIDIssue("network.zone_name", tc.id)
		require.NotEmpty(t, issue, "%q must be invalid", tc.id)
		assert.Contains(t, issue, tc.want, "message for %q", tc.id)
		assert.Contains(t, issue, "network.zone_name")
	}
}

// TestValidateNetworkZone covers the lab-level wrapper: an unset zone_name
// resolves to the valid default, so only an explicit bad value is reported,
// and the message names the lab.
func TestValidateNetworkZone(t *testing.T) {
	assert.Empty(t, config.ValidateNetworkZone("demo", config.LabNetwork{}))
	assert.Empty(t, config.ValidateNetworkZone("demo", config.LabNetwork{ZoneName: "labvxlan"}))

	issues := config.ValidateNetworkZone("demo", config.LabNetwork{ZoneName: "labsvxlan"})
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], `lab "demo"`)
	assert.Contains(t, issues[0], "labsvxlan")
	assert.Contains(t, issues[0], "2-8 characters")
}

// TestResolveLabs_ZoneNameTooLong_Errors covers the load-time wiring: every
// verb resolves labs through ResolveLabs, so a nine-character zone_name must
// fail there, naming the lab and the field.
func TestResolveLabs_ZoneNameTooLong_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"demo": {Mode: "nested", Network: config.LabNetwork{ZoneName: "labsvxlan", ZoneType: "vxlan"}},
		},
	}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "demo")
	require.ErrorContains(t, err, "network.zone_name")
	require.ErrorContains(t, err, "labsvxlan")
}

// TestResolveLabs_ZoneNameValid_Passes covers the eight-character zone the
// article's lab uses, and an unset zone_name.
func TestResolveLabs_ZoneNameValid_Passes(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"demo":  {Mode: "nested", Network: config.LabNetwork{ZoneName: "labvxlan", ZoneType: "vxlan"}},
			"other": {Mode: "nested"},
		},
	}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Equal(t, "labvxlan", labs["demo"].Network.ZoneName)
}

// TestValidateNestedNetwork_VlanZoneNameFollowsZoneRule covers the inner zone,
// which PVE creates through the same endpoint: a nine-character name and a
// one-character name are both refused.
func TestValidateNestedNetwork_VlanZoneNameFollowsZoneRule(t *testing.T) {
	nn := func(zone string) config.LabNestedNetwork {
		return config.LabNestedNetwork{
			Bonds: []config.LabNestedBond{{Bridge: "vmbr1", VlanAware: true}},
			VlanZone: &config.LabNestedVlanZone{
				Bridge:   "vmbr1",
				ZoneName: zone,
			},
		}
	}

	for _, zone := range []string{"clivlan9x", "c"} {
		joined := strings.Join(config.ValidateNestedNetwork("demo", nn(zone)), "\n")
		assert.Contains(t, joined, "nested_network.vlan_zone.zone_name", "zone %q must be reported", zone)
		assert.Contains(t, joined, zone)
	}

	for _, issue := range config.ValidateNestedNetwork("demo", nn("clivlan")) {
		assert.NotContains(t, issue, "zone_name")
	}
}
