package redact_test

import (
	"testing"

	"github.com/fivetwenty-io/pmx-cli/internal/redact"
	"github.com/stretchr/testify/require"
)

func TestPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"non-empty password", "s3cret-test!", "<redacted>"},
		{"empty password", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, redact.Password(tc.in))
		})
	}
}

func TestLine(t *testing.T) {
	t.Parallel()

	const secret = "s3cret-test!"

	tests := []struct {
		name   string
		line   string
		secret string
		want   string
	}{
		{
			name:   "single occurrence",
			line:   "--password " + secret,
			secret: secret,
			want:   "--password <redacted>",
		},
		{
			name:   "multiple occurrences",
			line:   secret + " and again " + secret,
			secret: secret,
			want:   "<redacted> and again <redacted>",
		},
		{
			name:   "secret absent",
			line:   "--password hunter2",
			secret: secret,
			want:   "--password hunter2",
		},
		{
			name:   "empty secret is a no-op",
			line:   "--password " + secret,
			secret: "",
			want:   "--password " + secret,
		},
		{
			name:   "empty line",
			line:   "",
			secret: secret,
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, redact.Line(tc.line, tc.secret))
		})
	}
}
