package node

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseNodeACMEConfig(t *testing.T) {
	got := parseNodeACMEConfig(map[string]any{
		"acmedomain10": "ten.example.com",
		"acmedomain2":  "domain=two.example.com,plugin=cf",
		"acmedomainx":  "ignored.example.com",
		"acme":         "domains=a.example.com;b.example.com",
		"description":  "acmedomain lookalike values are not keys",
	})
	require.Equal(t, "default", got.Account)
	var names []string
	for _, d := range got.Domains {
		names = append(names, d.Domain+"/"+d.Plugin)
	}
	require.Equal(t, []string{
		"two.example.com/cf", "ten.example.com/standalone",
		"a.example.com/standalone", "b.example.com/standalone",
	}, names, "acmedomainN in numeric order, then the standalone domains= list")
}

func TestCertCovers(t *testing.T) {
	sans := []string{"DNS:pve1.example.com", "*.lab.example.com"}
	require.True(t, certCovers(sans, "pve1.example.com"))
	require.True(t, certCovers(sans, "PVE1.example.com."))
	require.True(t, certCovers(sans, "node.lab.example.com"))
	require.False(t, certCovers(sans, "a.node.lab.example.com"), "a wildcard covers one label only")
	require.False(t, certCovers(sans, "lab.example.com"))
	require.False(t, certCovers(nil, "pve1.example.com"))
}

func TestParseTimerShow_RejectsUnexpectedOutput(t *testing.T) {
	_, err := parseTimerShow("Failed to connect to bus\n")
	require.Error(t, err)
}
