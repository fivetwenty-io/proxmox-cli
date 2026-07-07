package apiclient_test

import (
	"testing"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
)

// ---------------------------------------------------------------------------
// BuildPBSOptions
// ---------------------------------------------------------------------------

func TestBuildPBSOptions_PortDefaultsWhenZero(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildPBSOptions(
		"pbs.example.com", 0, "https",
		"root", "pam",
		"mytoken=supersecret",
		"", "", "",
		false, "",
	)

	require.Equal(t, pbs.DefaultPort, opts.Port)
}

func TestBuildPBSOptions_ExplicitPortPreserved(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildPBSOptions(
		"pbs.example.com", 8443, "https",
		"root", "pam",
		"mytoken=supersecret",
		"", "", "",
		false, "",
	)

	require.Equal(t, 8443, opts.Port)
}

func TestBuildPBSOptions_CredentialNames(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildPBSOptions(
		"pbs.example.com", 0, "https",
		"root", "pam",
		"mytoken=supersecret",
		"", "", "",
		false, "",
	)

	require.Equal(t, pbs.APITokenName, opts.APITokenName)
	require.Equal(t, pbs.CookieName, opts.CookieName)
}

func TestBuildPBSOptions_TokenAuth(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildPBSOptions(
		"pbs.example.com", 0, "https",
		"root", "pam",
		"mytoken=supersecret",
		"", "", "",
		false, "",
	)

	require.Equal(t, "pbs.example.com", opts.Host)
	require.Equal(t, "https", opts.Protocol)
	require.Equal(t, "root@pam!mytoken=supersecret", opts.APIToken)
	require.Empty(t, opts.Password)
	require.Empty(t, opts.Ticket)
}

func TestBuildPBSOptions_PasswordAuth(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildPBSOptions(
		"192.168.1.20", 0, "https",
		"admin", "pbs",
		"",
		"s3cr3t", "", "",
		false, "",
	)

	require.Equal(t, "admin@pbs", opts.Username)
	require.Equal(t, "s3cr3t", opts.Password)
	require.Empty(t, opts.APIToken)
	require.Empty(t, opts.Ticket)
}

func TestBuildPBSOptions_TicketAuth(t *testing.T) {
	t.Parallel()
	const ticketVal = "PBS:root@pam:66F0A1B2::fakeb64=="
	const csrfVal = "66F0A1B2:fakecsrf"

	opts := apiclient.BuildPBSOptions(
		"pbs.local", 0, "",
		"root", "pam",
		"",
		"",
		ticketVal,
		csrfVal,
		false, "",
	)

	require.Equal(t, ticketVal, opts.Ticket)
	require.Equal(t, csrfVal, opts.CSRFToken)
	require.Empty(t, opts.APIToken)
	require.Empty(t, opts.Password)
}

func TestBuildPBSOptions_TokenPriorityOverPassword(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildPBSOptions(
		"pbs.local", 0, "",
		"root", "pam",
		"tok=secret",
		"pw123",
		"", "",
		false, "",
	)

	require.NotEmpty(t, opts.APIToken)
	require.Empty(t, opts.Password)
}

func TestBuildPBSOptions_InsecureSetsSSLNone(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildPBSOptions(
		"pbs.local", 0, "",
		"root", "pam",
		"tok=s",
		"", "", "",
		true,
		"",
	)

	require.NotNil(t, opts.SSLOptions)
	require.Equal(t, pve.SSLVerifyNone, opts.SSLOptions.VerifyMode)
	require.False(t, opts.SSLOptions.VerifyHostname)
}

func TestBuildPBSOptions_FingerprintAddedToCache(t *testing.T) {
	t.Parallel()
	const fp = "AA:BB:CC:DD:EE:FF"

	opts := apiclient.BuildPBSOptions(
		"pbs.local", 0, "",
		"root", "pam",
		"tok=s",
		"", "", "",
		false,
		fp,
	)

	require.True(t, opts.CachedFingerprints[fp])
}
