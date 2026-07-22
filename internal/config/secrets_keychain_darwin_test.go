//go:build darwin

package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notFoundStderr is security(1)'s stderr when no matching item exists.
const notFoundStderr = "SecKeychainSearchCopyNext: The specified item could not be found in the keychain."

// fakeEmptyKeychain returns a keychainRun stub simulating a keychain with no
// matching items: delete-generic-password reports not-found, everything else
// succeeds. Non-delete invocations are appended to calls when it is non-nil.
func fakeEmptyKeychain(calls *[]string, stdinOut *string, argsOut *[]string) func(string, ...string) (string, error) {
	return func(stdin string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "delete-generic-password" {
			return notFoundStderr, errors.New("exit 44")
		}
		if calls != nil {
			*calls = append(*calls, args[0])
		}
		if stdinOut != nil {
			*stdinOut = stdin
		}
		if argsOut != nil {
			*argsOut = args
		}
		return "", nil
	}
}

func TestStoreKeychainSecret_FeedsSecretOnStdinNotArgv(t *testing.T) {
	var gotStdin string
	var gotArgs []string
	orig := keychainRun
	keychainRun = fakeEmptyKeychain(nil, &gotStdin, &gotArgs)
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
	inner := fakeEmptyKeychain(nil, nil, nil)
	keychainRun = func(stdin string, args ...string) (string, error) {
		called = true
		return inner(stdin, args...)
	}
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

func TestStoreKeychainSecret_SurfacesAddError(t *testing.T) {
	orig := keychainRun
	keychainRun = func(_ string, args ...string) (string, error) {
		if args[0] == "delete-generic-password" {
			return notFoundStderr, errors.New("exit 44")
		}
		return "some security failure", errors.New("exit 1")
	}
	defer func() { keychainRun = orig }()

	err := StoreKeychainSecret("svc", "acct", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "some security failure")
}

func TestStoreKeychainSecret_SurfacesPurgeError(t *testing.T) {
	orig := keychainRun
	keychainRun = func(string, ...string) (string, error) {
		return "keychain is locked", errors.New("exit 1")
	}
	defer func() { keychainRun = orig }()

	err := StoreKeychainSecret("svc", "acct", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clear existing items")
	assert.Contains(t, err.Error(), "keychain is locked")
}

// TestStoreKeychainSecret_PurgesDuplicatesBeforeAdd verifies the store path
// deletes every existing (service, account) item — two duplicates here — before
// the single add, so ACL-orphaned items from earlier binary signatures cannot
// accumulate or shadow the fresh value.
func TestStoreKeychainSecret_PurgesDuplicatesBeforeAdd(t *testing.T) {
	var calls []string
	deletes := 0
	orig := keychainRun
	keychainRun = func(_ string, args ...string) (string, error) {
		calls = append(calls, args[0])
		if args[0] == "delete-generic-password" {
			deletes++
			if deletes > 2 {
				return notFoundStderr, errors.New("exit 44")
			}
			return "", nil
		}
		return "", nil
	}
	defer func() { keychainRun = orig }()

	require.NoError(t, StoreKeychainSecret("svc", "acct", "fresh-secret"))
	require.Equal(t, []string{
		"delete-generic-password", "delete-generic-password", "delete-generic-password", "-i",
	}, calls, "must delete until not-found, then add exactly once")
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

// TestDeleteKeychainSecret_RemovesAllDuplicates verifies delete keeps calling
// delete-generic-password (which removes only the first match per invocation)
// until the keychain reports not-found, clearing duplicate items in one call.
func TestDeleteKeychainSecret_RemovesAllDuplicates(t *testing.T) {
	deletes := 0
	orig := keychainRun
	keychainRun = func(_ string, args ...string) (string, error) {
		require.Equal(t, "delete-generic-password", args[0])
		deletes++
		if deletes > 2 {
			return notFoundStderr, errors.New("exit 44")
		}
		return "", nil
	}
	defer func() { keychainRun = orig }()

	require.NoError(t, DeleteKeychainSecret("svc", "acct"))
	require.Equal(t, 3, deletes, "two successful deletes then the terminating not-found")
}

// TestDeleteKeychainSecret_CapsRunawayLoop guards the loop bound: if security
// keeps reporting success without the item count ever reaching zero, delete
// errors out instead of spinning forever.
func TestDeleteKeychainSecret_CapsRunawayLoop(t *testing.T) {
	deletes := 0
	orig := keychainRun
	keychainRun = func(string, ...string) (string, error) {
		deletes++
		return "", nil
	}
	defer func() { keychainRun = orig }()

	err := DeleteKeychainSecret("svc", "acct")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still present")
	require.Equal(t, maxKeychainDuplicates, deletes)
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
