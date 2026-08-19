package config_test

import (
	"testing"

	yaml "github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// TestLabNetwork_EffectiveIPv6_DefaultsOn pins the default-on rule: a lab
// file that never mentions ipv6 (every fleet lab written before the field
// existed) gets IPv6 enabled, without the loader mutating the struct.
func TestLabNetwork_EffectiveIPv6_DefaultsOn(t *testing.T) {
	var lab config.Lab
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(todaysShapeLabYAML), &lab, yaml.Strict()))

	assert.Nil(t, lab.Network.IPv6, "absent key must stay nil, not be defaulted at load time")
	assert.True(t, lab.Network.EffectiveIPv6(), "absent ipv6 key must mean enabled")
	assert.Empty(t, lab.Network.CIDR6, "absent cidr6 key must stay empty")
}

// TestLabNetwork_EffectiveIPv6_ExplicitValues pins both explicit spellings:
// `ipv6: false` is the documented opt-out, and an explicit `ipv6: true` is a
// no-op restatement of the default.
func TestLabNetwork_EffectiveIPv6_ExplicitValues(t *testing.T) {
	off := false
	on := true

	assert.False(t, config.LabNetwork{IPv6: &off}.EffectiveIPv6())
	assert.True(t, config.LabNetwork{IPv6: &on}.EffectiveIPv6())
}

// TestLabNetwork_IPv6Fields_StrictParse pins the YAML spelling of the new
// keys: network.ipv6 (bool), network.cidr6, and network.vnets[].cidr6 all
// parse under the same yaml.Strict() decode loadLabFile uses.
func TestLabNetwork_IPv6Fields_StrictParse(t *testing.T) {
	const labYAML = `
name: krutten
mode: nested
owner: krutten@pve
network:
  vnet_id: krutten
  cidr: 10.109.0.0/16
  ipv6: false
  cidr6: fd10:9::/48
  vnets:
    - id: kruttenst
      tag: 5003
      cidr: 10.109.32.0/24
      cidr6: fd10:9:0:20::/64
`
	var lab config.Lab
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(labYAML), &lab, yaml.Strict()))

	require.NotNil(t, lab.Network.IPv6)
	assert.False(t, *lab.Network.IPv6)
	assert.False(t, lab.Network.EffectiveIPv6())
	assert.Equal(t, "fd10:9::/48", lab.Network.CIDR6)
	require.Len(t, lab.Network.Vnets, 1)
	assert.Equal(t, "fd10:9:0:20::/64", lab.Network.Vnets[0].CIDR6)
}
