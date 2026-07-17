//go:build darwin

package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreKeychainSecret_FeedsSecretOnStdinNotArgv(t *testing.T) {
	var gotStdin string
	var gotArgs []string
	orig := keychainRun
	keychainRun = func(stdin string, args ...string) (string, error) {
		gotStdin, gotArgs = stdin, args
		return "", nil
	}
	defer func() { keychainRun = orig }()

	err := StoreKeychainSecret("pmx-lab-demo", "pmx@pve!pmx", "s3cr3t-value")
	require.NoError(t, err)

	// The security(1) interactive command line (with the secret) is fed on
	// stdin; the secret must never appear in argv (ps-visible).
	assert.Contains(t, gotStdin, "add-generic-password")
	assert.Contains(t, gotStdin, "-U")
	assert.Contains(t, gotStdin, "pmx-lab-demo")
	assert.Contains(t, gotStdin, "pmx@pve!pmx")
	assert.Contains(t, gotStdin, "s3cr3t-value")
	assert.Equal(t, []string{"-i"}, gotArgs)
	for _, a := range gotArgs {
		assert.NotContains(t, a, "s3cr3t-value", "secret must not be on argv")
	}
}

func TestStoreKeychainSecret_RejectsEmptyServiceOrAccount(t *testing.T) {
	require.Error(t, StoreKeychainSecret("", "acct", "x"))
	require.Error(t, StoreKeychainSecret("svc", "", "x"))
}

func TestStoreKeychainSecret_RejectsInjectionChars(t *testing.T) {
	// keychainRun must never be invoked when validation rejects the input.
	orig := keychainRun
	called := false
	keychainRun = func(string, ...string) (string, error) { called = true; return "", nil }
	defer func() { keychainRun = orig }()

	require.Error(t, StoreKeychainSecret("svc", "acct", "line1\nadd-generic-password -s evil"))
	require.Error(t, StoreKeychainSecret("bad svc", "acct", "x"))
	require.Error(t, StoreKeychainSecret("svc", "acct\ninject", "x"))
	require.Error(t, StoreKeychainSecret("svc", "acct", "")) // empty secret now rejected too
	require.False(t, called, "keychainRun must not run when validation fails")

	// A clean UUID-form secret still succeeds (no whitespace/control chars).
	require.NoError(t, StoreKeychainSecret("pmx-lab-demo", "pmx@pve!pmx", "12345678-90ab-cdef-1234-567890abcdef"))
	assert.True(t, called)
}

func TestStoreKeychainSecret_SurfacesSecurityError(t *testing.T) {
	orig := keychainRun
	keychainRun = func(string, ...string) (string, error) {
		return "some security failure", errors.New("exit 1")
	}
	defer func() { keychainRun = orig }()

	err := StoreKeychainSecret("svc", "acct", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "some security failure")
}

func TestDeleteKeychainSecret_NotFoundIsSuccess(t *testing.T) {
	orig := keychainRun
	keychainRun = func(string, ...string) (string, error) {
		return "SecKeychainSearchCopyNext: The specified item could not be found in the keychain.",
			errors.New("exit 44")
	}
	defer func() { keychainRun = orig }()

	require.NoError(t, DeleteKeychainSecret("svc", "acct"))
}

func TestDeleteKeychainSecret_RealErrorPropagates(t *testing.T) {
	orig := keychainRun
	keychainRun = func(string, ...string) (string, error) {
		return "keychain is locked", errors.New("exit 1")
	}
	defer func() { keychainRun = orig }()

	err := DeleteKeychainSecret("svc", "acct")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "keychain is locked"))
}
