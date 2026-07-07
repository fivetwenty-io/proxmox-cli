package cli_test

import (
	"errors"
	"fmt"
	"testing"

	pveerrors "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

func TestAuthHint_Unauthorized(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", pveerrors.ErrUnauthorized},
		{"wrapped sentinel", fmt.Errorf("cluster status: %w", pveerrors.ErrUnauthorized)},
		{"typed auth error", &pveerrors.AuthenticationError{Realm: "pam"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := cli.AuthHint(tc.err)
			require.Contains(t, hint, "HTTP 401")
			require.Contains(t, hint, "USER@REALM!TOKENID=SECRET")
		})
	}
}

func TestAuthHint_Forbidden(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", pveerrors.ErrForbidden},
		{"wrapped sentinel", fmt.Errorf("node status: %w", pveerrors.ErrForbidden)},
		{"typed permission error", &pveerrors.PermissionError{What: "/nodes/pve1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := cli.AuthHint(tc.err)
			require.Contains(t, hint, "HTTP 403")
			require.Contains(t, hint, "Privilege Separation")
		})
	}
}

func TestAuthHint_NonAuthErrorsReturnEmpty(t *testing.T) {
	cases := []error{
		nil,
		errors.New("boom"),
		pveerrors.ErrNotFound,
		&pveerrors.ParameterError{},
	}
	for _, err := range cases {
		require.Empty(t, cli.AuthHint(err))
	}
}
