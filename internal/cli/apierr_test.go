package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsEmptyDataResponse(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"binding message",
			fmt.Errorf("config.UpdateAccessPam: empty data in response (code=200)"),
			true,
		},
		{
			"wrapped binding message",
			fmt.Errorf("update PAM realm: %w",
				fmt.Errorf("config.UpdateAccessPam: empty data in response (code=200)")),
			true,
		},
		{"permission failure", errors.New("permission check failed"), false},
		{"empty-sounding but unrelated", errors.New("empty datastore"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, IsEmptyDataResponse(tc.err))
		})
	}
}
