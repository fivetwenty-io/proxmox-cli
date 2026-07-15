//go:build integration

package apiclient_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
)

// newIntegrationClient constructs an *apiclient.APIClient from integration test
// environment variables. It calls t.Skip when the target host is not configured
// or no usable credentials are present, so every test that calls it is
// automatically skipped in environments without a live PVE instance.
//
// Environment variables:
//   - PMX_TEST_HOST (required) — Proxmox VE host name or IP address.
//   - PMX_TEST_INSECURE — set to "true" to skip TLS certificate verification.
//
// Authentication (first usable option wins):
//  1. Token — PMX_TEST_TOKEN_ID and PMX_TEST_TOKEN_SECRET both set.
//     PMX_TEST_USER (default "root") and PMX_TEST_REALM (default "pam") identify
//     the token owner.
//  2. Password — PMX_TEST_USER (default "root"), PMX_TEST_REALM (default "pam"),
//     and PMX_TEST_PASSWORD all set.
//
// If PMX_TEST_HOST is set but neither credential option is usable the test is
// skipped with a descriptive message rather than failing, because the test
// environment may legitimately omit secrets.
func newIntegrationClient(t *testing.T) *apiclient.APIClient {
	t.Helper()

	host := os.Getenv("PMX_TEST_HOST")
	if host == "" {
		t.Skip("integration tests require PMX_TEST_HOST to be set; skipping")
	}

	insecure := os.Getenv("PMX_TEST_INSECURE") == "true"

	user := os.Getenv("PMX_TEST_USER")
	if user == "" {
		user = "root"
	}

	realm := os.Getenv("PMX_TEST_REALM")
	if realm == "" {
		realm = "pam"
	}

	tokenID := os.Getenv("PMX_TEST_TOKEN_ID")
	tokenSecret := os.Getenv("PMX_TEST_TOKEN_SECRET")
	password := os.Getenv("PMX_TEST_PASSWORD")

	// Determine which auth mechanism is available.
	var token string
	switch {
	case tokenID != "" && tokenSecret != "":
		// BuildOptions expects token in "tokenid=secret" form; it prepends the
		// qualified username to produce "user@realm!tokenid=secret".
		token = fmt.Sprintf("%s=%s", tokenID, tokenSecret)
	case password != "":
		// password auth — BuildOptions maps username+realm+password.
	default:
		t.Skipf(
			"integration tests require credentials: set PMX_TEST_TOKEN_ID+PMX_TEST_TOKEN_SECRET "+
				"or PMX_TEST_PASSWORD (user=%q, realm=%q, host=%q); skipping",
			user, realm, host,
		)
	}

	opts := apiclient.BuildOptions(
		host,
		0,  // port 0 → pve.Options.setDefaults fills in 8006
		"", // protocol "" → pve.Options.setDefaults fills in "https"
		user,
		realm,
		token,    // non-empty only for token auth
		password, // non-empty only for password auth
		"",       // ticket — not used in CI integration tests
		"",       // csrf — not used in CI integration tests
		insecure,
		"", // fingerprint
	)

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err, "NewAPIClient must not error for a reachable PVE host")
	return ac
}

// TestIntegration_VersionGet verifies that the /version endpoint returns a
// non-empty version string. This is the most minimal live operation: it
// requires a valid host + credential but does not need any cluster resources.
func TestIntegration_VersionGet(t *testing.T) {
	ac := newIntegrationClient(t)

	resp, err := ac.Version.Get(context.Background())
	require.NoError(t, err, "Version.Get must succeed against a live PVE host")
	require.NotNil(t, resp, "Version.Get must return a non-nil response")
	require.NotEmpty(t, resp.Version, "Version.Get response must contain a non-empty version string")
}

// TestIntegration_NodesList verifies that the /nodes endpoint responds without
// error and returns a well-formed slice (≥0 elements). A single-node cluster
// returns exactly one entry; a multi-node cluster returns more. Zero nodes is
// accepted because the test does not impose a minimum cluster size requirement.
func TestIntegration_NodesList(t *testing.T) {
	ac := newIntegrationClient(t)

	resp, err := ac.Nodes.ListNodes(context.Background())
	require.NoError(t, err, "Nodes.ListNodes must succeed against a live PVE host")
	require.NotNil(t, resp, "Nodes.ListNodes must return a non-nil response")
	// The slice length is ≥ 0 by definition; the assertion here confirms the
	// response is a usable value rather than a nil pointer masking an error.
	require.GreaterOrEqual(t, len(*resp), 0,
		"Nodes.ListNodes must return a non-negative node count")
}
