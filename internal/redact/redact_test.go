package redact_test

import (
	"testing"

	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
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

// TestQueryParams_MasksCredentialValues covers the disclosure path a GET or
// DELETE opens: the SDK encodes parameters into the request URL, so a command
// taking --password (node scan pbs requires one) put the cleartext credential
// into the logged url field, into the exit record, and onto stderr.
func TestQueryParams_MasksCredentialValues(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "password in a request url",
			in:   "https://h:8006/api2/json/nodes/n1/scan/pbs?password=SUPERSECRET&server=pbs.example.com",
			want: "https://h:8006/api2/json/nodes/n1/scan/pbs?password=<redacted>&server=pbs.example.com",
		},
		{
			name: "embedded in a longer error message",
			in:   `GET request to "https://h/api?token=abc123&node=n1" failed: connection refused`,
			want: `GET request to "https://h/api?token=<redacted>&node=n1" failed: connection refused`,
		},
		{
			name: "several sensitive params",
			in:   "https://h/a?password=p1&ticket=t1&csrf=c1&node=n1",
			want: "https://h/a?password=<redacted>&ticket=<redacted>&csrf=<redacted>&node=n1",
		},
		{
			name: "case-insensitive key match",
			in:   "https://h/a?Password=p1&APIKey=k1",
			want: "https://h/a?Password=<redacted>&APIKey=<redacted>",
		},
		{
			name: "no query string is untouched",
			in:   "https://h:8006/api2/json/nodes/n1/status",
			want: "https://h:8006/api2/json/nodes/n1/status",
		},
		{
			name: "non-sensitive params survive so the message stays useful",
			in:   "https://h/a?node=n1&vmid=100&full=1",
			want: "https://h/a?node=n1&vmid=100&full=1",
		},
		{
			name: "empty value is still masked, never left bare",
			in:   "https://h/a?password=&node=n1",
			want: "https://h/a?password=<redacted>&node=n1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redact.QueryParams(tc.in); got != tc.want {
				t.Fatalf("redact.QueryParams(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestQueryParams_LeavesProseAlone pins the anchoring: only pairs introduced
// by "?" or "&" are query parameters, so ordinary text that happens to
// contain "secret=" is not rewritten.
func TestQueryParams_LeavesProseAlone(t *testing.T) {
	const in = "the secret=value form is not a query parameter here"
	if got := redact.QueryParams(in); got != in {
		t.Fatalf("QueryParams rewrote prose: %q", got)
	}
}

func TestSensitiveKey(t *testing.T) {
	for _, k := range []string{"password", "Password", "api_password", "token", "csrf-token", "apikey"} {
		if !redact.SensitiveKey(k) {
			t.Errorf("redact.SensitiveKey(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"node", "vmid", "server", "username", "full"} {
		if redact.SensitiveKey(k) {
			t.Errorf("redact.SensitiveKey(%q) = true, want false", k)
		}
	}
}
