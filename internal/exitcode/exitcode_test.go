package exitcode_test

import (
	"errors"
	"fmt"
	"testing"

	pveerrors "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/errors"
	"github.com/fivetwenty-io/pve-cli/internal/exitcode"
	"github.com/stretchr/testify/require"
)

// sentinel helper — wraps any error in a fmt.Errorf %w chain.
func wrap(err error) error {
	return fmt.Errorf("outer context: %w", err)
}

func TestFromError_Nil(t *testing.T) {
	require.Equal(t, exitcode.OK, exitcode.FromError(nil))
}

func TestFromError_Generic(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"plain error", errors.New("something went wrong")},
		{"wrapped plain", wrap(errors.New("deep error"))},
		{"ErrServer sentinel", pveerrors.ErrServer},
		{"wrapped ErrServer", wrap(pveerrors.ErrServer)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.Generic, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_BadArgs(t *testing.T) {
	base := &pveerrors.ParameterError{}

	tests := []struct {
		name string
		err  error
	}{
		{"direct ParameterError", base},
		{"wrapped ParameterError", wrap(base)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.BadArgs, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_Auth(t *testing.T) {
	authErr := &pveerrors.AuthenticationError{TFA: false}
	permErr := &pveerrors.PermissionError{}

	tests := []struct {
		name string
		err  error
	}{
		{"AuthenticationError no TFA", authErr},
		{"wrapped AuthenticationError no TFA", wrap(authErr)},
		{"PermissionError", permErr},
		{"wrapped PermissionError", wrap(permErr)},
		{"ErrUnauthorized sentinel", pveerrors.ErrUnauthorized},
		{"wrapped ErrUnauthorized", wrap(pveerrors.ErrUnauthorized)},
		{"ErrForbidden sentinel", pveerrors.ErrForbidden},
		{"wrapped ErrForbidden", wrap(pveerrors.ErrForbidden)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.Auth, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_TFARequired(t *testing.T) {
	tfaErr := &pveerrors.TFARequiredError{Ticket: "PVE:root@pam:TICKET", Types: []string{"totp"}}
	authTFA := &pveerrors.AuthenticationError{TFA: true}

	tests := []struct {
		name string
		err  error
	}{
		{"TFARequiredError direct", tfaErr},
		{"wrapped TFARequiredError", wrap(tfaErr)},
		{"AuthenticationError with TFA=true", authTFA},
		{"wrapped AuthenticationError TFA=true", wrap(authTFA)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.TFARequired, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_NotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrNotFound sentinel", pveerrors.ErrNotFound},
		{"wrapped ErrNotFound", wrap(pveerrors.ErrNotFound)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.NotFound, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_Conflict(t *testing.T) {
	// ErrConflict sentinel (HTTP 409)
	tests := []struct {
		name string
		err  error
	}{
		{"ErrConflict sentinel", pveerrors.ErrConflict},
		{"wrapped ErrConflict", wrap(pveerrors.ErrConflict)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.Conflict, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_Conflict_ResourceLocked(t *testing.T) {
	// APIError with HTTP 423 (CodeResourceLocked) must map to Conflict.
	locked := &pveerrors.APIError{}
	// Access the unexported HTTPCode field indirectly — we can't set it directly.
	// Use ParseAPIError to build a proper 423 error.
	body := []byte(`{"message":"resource locked","code":423}`)
	lockedErr := pveerrors.ParseAPIError(pveerrors.CodeResourceLocked, body)
	require.NotNil(t, lockedErr, "ParseAPIError must return non-nil")
	_ = locked // silence unused warning

	require.Equal(t, exitcode.Conflict, exitcode.FromError(lockedErr))
	require.Equal(t, exitcode.Conflict, exitcode.FromError(wrap(lockedErr)))
}

func TestFromError_Infra_Connection(t *testing.T) {
	connErr := &pveerrors.ConnectionError{Host: "pve.example.com", Port: 8006, Message: "refused"}

	tests := []struct {
		name string
		err  error
	}{
		{"ConnectionError direct", connErr},
		{"wrapped ConnectionError", wrap(connErr)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.Infra, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_Infra_SSL(t *testing.T) {
	sslErr := &pveerrors.SSLError{Host: "pve.example.com", Message: "cert mismatch"}

	tests := []struct {
		name string
		err  error
	}{
		{"SSLError direct", sslErr},
		{"wrapped SSLError", wrap(sslErr)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.Infra, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_Infra_Timeout(t *testing.T) {
	timeoutErr := &pveerrors.TimeoutError{Operation: "GET /api/version", Duration: "30s"}

	tests := []struct {
		name string
		err  error
	}{
		{"TimeoutError direct", timeoutErr},
		{"wrapped TimeoutError", wrap(timeoutErr)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, exitcode.Infra, exitcode.FromError(tc.err))
		})
	}
}

func TestFromError_Constants(t *testing.T) {
	// Verify constant values match the A-02 decision table.
	require.Equal(t, 0, exitcode.OK)
	require.Equal(t, 1, exitcode.Generic)
	require.Equal(t, 2, exitcode.BadArgs)
	require.Equal(t, 3, exitcode.Infra)
	require.Equal(t, 4, exitcode.Auth)
	require.Equal(t, 5, exitcode.NotFound)
	require.Equal(t, 6, exitcode.Conflict)
	require.Equal(t, 7, exitcode.TFARequired)
}
