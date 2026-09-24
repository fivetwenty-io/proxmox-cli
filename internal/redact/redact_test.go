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

// TestProxyURL covers the three cases the doc comment promises: a password
// masked by concatenation, a URL with none left alone, and every shape
// url.Parse rejects reduced to a scheme-only placeholder or a bare one.
func TestProxyURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "password is masked in place",
			in:   "socks5://pmx:s3cret@proxy.example:1080",
			want: "socks5://pmx:<redacted>@proxy.example:1080",
		},
		{
			name: "password survives a path, query, and fragment",
			in:   "http://pmx:s3cret@proxy.example:8080/path?q=1#frag",
			want: "http://pmx:<redacted>@proxy.example:8080/path?q=1#frag",
		},
		{
			name: "no userinfo at all is unchanged",
			in:   "socks5h://proxy.example:1080",
			want: "socks5h://proxy.example:1080",
		},
		{
			name: "a username with no password is unchanged",
			in:   "socks5://pmx@proxy.example:1080",
			want: "socks5://pmx@proxy.example:1080",
		},
		{
			name: "empty string is unchanged",
			in:   "",
			want: "",
		},
		{
			// The three unparseable proxy.url shapes ValidateProxyBlock must
			// also reject: an unescaped "/" in the password confuses the
			// port parser, an invalid percent-escape in the password, and an
			// unterminated IPv6 literal in the host. url.Parse rejects all
			// three, so each becomes the scheme it could still recover.
			name: "unescaped slash in password is unparseable",
			in:   "socks5://pmx:s3cr3t/x@proxy:1080",
			want: "socks5://<redacted>",
		},
		{
			name: "invalid percent-escape in password is unparseable",
			in:   "socks5://pmx:s3%zzt@proxy:1080",
			want: "socks5://<redacted>",
		},
		{
			name: "unterminated ipv6 literal is unparseable",
			in:   "socks5://u:s3cret@[::1",
			want: "socks5://<redacted>",
		},
		{
			name: "no recoverable scheme falls back to the bare placeholder",
			in:   "%zz",
			want: "<redacted>",
		},
		{
			// A missing "//" makes the whole thing opaque to url.Parse, so
			// there is no userinfo to find, but the password still sits
			// right there in the opaque text.
			name: "missing double slash is opaque, not userinfo-free",
			in:   "socks5:pmx:s3cret@proxy:1080",
			want: "<redacted>",
		},
		{
			// The password starts with digits, so it parses as a port, and
			// the "@" lands in the fragment instead of introducing userinfo.
			name: "password read as a port pushes the credential into the fragment",
			in:   "socks5://pmx:12#s3cret@proxy:1080",
			want: "socks5://<redacted>",
		},
		{
			name: "password read as a port pushes the credential into the query",
			in:   "socks5://pmx:99?s3cret@proxy:1080",
			want: "socks5://<redacted>",
		},
		{
			name: "password read as a port pushes the credential into the path",
			in:   "socks5://pmx:1234/s3cret@proxy:1080",
			want: "socks5://<redacted>",
		},
		{
			// Go decodes this userinfo to the username "pmx:s3cret" with no
			// password, but it is a user:password pair the operator
			// percent-encoded whole.
			name: "percent-encoded colon in the username",
			in:   "socks5://pmx%3As3cret@proxy:1080",
			want: "socks5://<redacted>@proxy:1080",
		},
		{
			name: "percent-encoded colon in the username ahead of a password",
			in:   "socks5://pmx%3As3cret:x@proxy:1080",
			want: "socks5://<redacted>@proxy:1080",
		},
		{
			name: "ordinary userinfo with a numeric-looking password still masks",
			in:   "http://pmx:8080@proxy:3128",
			want: "http://pmx:<redacted>@proxy:3128",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := redact.ProxyURL(tc.in)
			require.Equal(t, tc.want, got)
			require.NotContains(t, got, "s3cret", "must never leak the password")
			require.NotContains(t, got, "%3C", "placeholder must never be percent-encoded")
		})
	}
}

// TestURLUserinfo covers masking a credential embedded inside a longer piece
// of free text, including more than one occurrence.
func TestURLUserinfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single embedded url",
			in:   `dial tcp: connecting to https://user:hunter2@proxy.example:1080 failed`,
			want: `dial tcp: connecting to https://user:<redacted>@proxy.example:1080 failed`,
		},
		{
			name: "two embedded urls",
			in:   "tried socks5://a:p1@proxy1:1080 then socks5://b:p2@proxy2:1080",
			want: "tried socks5://a:<redacted>@proxy1:1080 then socks5://b:<redacted>@proxy2:1080",
		},
		{
			name: "no url present is unchanged",
			in:   "plain error text with no credentials in it",
			want: "plain error text with no credentials in it",
		},
		{
			name: "url with no userinfo is unchanged",
			in:   "reached http://proxy.example:8080 directly",
			want: "reached http://proxy.example:8080 directly",
		},
		{
			// The password itself contains "@", so masking must run to the
			// last "@" in the run, not the first.
			name: "password containing an at sign",
			in:   "socks5://u:p@ss@host:1080",
			want: "socks5://u:<redacted>@host:1080",
		},
		{
			// An empty username still has a password worth masking.
			name: "empty username with a password",
			in:   "socks5://:s3cret@host:1080",
			want: "socks5://:<redacted>@host:1080",
		},
		{
			// The username itself contains "@", as a Proxmox user id
			// (user@realm) pasted directly into a proxy URL would.
			name: "username containing an at sign",
			in:   "socks5://alice@corp:s3cret@host:1080",
			want: "socks5://alice@corp:<redacted>@host:1080",
		},
		{
			// The "//" typed as ":" alone is the likeliest proxy typo.
			name: "missing double slash",
			in:   "proxy socks5:pmx:s3cret@proxy:1080 refused",
			want: "proxy socks5:pmx:<redacted>@proxy:1080 refused",
		},
		{
			name: "missing double slash with an at sign in the password",
			in:   "socks5:u:p@ss@h",
			want: "socks5:u:<redacted>@h",
		},
		{
			// A slash in the username slot, as a mistyped path would put
			// there, must not stop the password from being found.
			name: "slash ahead of the password",
			in:   "socks5://u/x:s3cret@h",
			want: "socks5://u/x:<redacted>@h",
		},
		{
			name: "single slash after the scheme",
			in:   "socks5:/u:s3cret@h",
			want: "socks5:/u:<redacted>@h",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := redact.URLUserinfo(tc.in)
			require.Equal(t, tc.want, got)
		})
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
