package node

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestACMETrustPlan_IPv6HostNeedsSwitch(t *testing.T) {
	p := &acmeTrustPlan{prefix: "pmx", ctxName: "lab", contextPin: true, host: "fd00::10", hostIsIP: true}
	require.True(t, p.needsHostSwitch())
	require.Contains(t, p.hint("pve1", false), "The context's host fd00::10 is an IP address")

	covered := &acmeTrustPlan{
		prefix: "pmx", ctxName: "lab", contextPin: true, host: "pve1.example.com",
		domains: []string{"pve1.example.com"}, domainsKnown: true,
	}
	require.False(t, covered.needsHostSwitch())
	require.Contains(t, covered.hint("pve1", false), `pmx context update lab --fingerprint ""`)

	unknown := &acmeTrustPlan{prefix: "pve", ctxName: "lab", contextPin: true, host: "pve1.example.com"}
	require.False(t, unknown.needsHostSwitch(), "a name host is only switched when the domains are known")
	require.Contains(t, unknown.hint("pve1", false), `pve context update lab --fingerprint ""`,
		"the persona prefix is used")
}
