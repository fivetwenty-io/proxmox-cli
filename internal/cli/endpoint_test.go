package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// TestParseEndpoint covers every accepted --api-endpoint shape, both IPv6
// bracket forms and the bare-literal form, the empty-string no-op, and
// every rejection with its exact error text for both the flag and the
// environment-variable source.
func TestParseEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("accepted forms", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			raw  string
			want cli.EndpointOverride
		}{
			{
				name: "host",
				raw:  "pve1",
				want: cli.EndpointOverride{Host: "pve1"},
			},
			{
				name: "host:port",
				raw:  "pve1:8006",
				want: cli.EndpointOverride{Host: "pve1", Port: 8006},
			},
			{
				name: "scheme://host",
				raw:  "https://pve1",
				want: cli.EndpointOverride{Host: "pve1", Protocol: "https"},
			},
			{
				name: "scheme://host:port",
				raw:  "https://pve1:8006",
				want: cli.EndpointOverride{Host: "pve1", Port: 8006, Protocol: "https"},
			},
			{
				name: "http scheme",
				raw:  "http://pve1:8006",
				want: cli.EndpointOverride{Host: "pve1", Port: 8006, Protocol: "http"},
			},
			{
				name: "trailing slash is tolerated",
				raw:  "https://pve1:8006/",
				want: cli.EndpointOverride{Host: "pve1", Port: 8006, Protocol: "https"},
			},
			{
				name: "bracketed IPv6, no port",
				raw:  "[::1]",
				want: cli.EndpointOverride{Host: "[::1]"},
			},
			{
				name: "bracketed IPv6 with port",
				raw:  "[::1]:8006",
				want: cli.EndpointOverride{Host: "[::1]", Port: 8006},
			},
			{
				name: "scheme with bracketed IPv6 and port",
				raw:  "https://[::1]:8006",
				want: cli.EndpointOverride{Host: "[::1]", Port: 8006, Protocol: "https"},
			},
			{
				name: "bare IPv6 literal, no brackets",
				raw:  "::1",
				want: cli.EndpointOverride{Host: "[::1]"},
			},
			{
				name: "scheme is case-insensitive",
				raw:  "HTTPS://pve1",
				want: cli.EndpointOverride{Host: "pve1", Protocol: "https"},
			},
			{
				name: "empty string is the unset zero value",
				raw:  "",
				want: cli.EndpointOverride{},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := cli.ParseEndpoint("--api-endpoint", tc.raw)
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("rejections", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			source  string
			raw     string
			wantErr string
		}{
			{
				name:    "unterminated IPv6 literal",
				source:  "--api-endpoint",
				raw:     "https://[::1",
				wantErr: `invalid --api-endpoint "https://[::1": want [scheme://]host[:port]`,
			},
			{
				name:    "unsupported scheme, flag source",
				source:  "--api-endpoint",
				raw:     "ftp://pve1",
				wantErr: `invalid --api-endpoint "ftp://pve1": scheme must be https or http`,
			},
			{
				name:    "unsupported scheme, environment source",
				source:  "$PMX_API_ENDPOINT",
				raw:     "ftp://pve1",
				wantErr: `invalid $PMX_API_ENDPOINT "ftp://pve1": scheme must be https or http`,
			},
			{
				name:    "port 0 is out of range",
				source:  "--api-endpoint",
				raw:     "pve1:0",
				wantErr: `invalid --api-endpoint "pve1:0": port 0 is out of range [1, 65535]`,
			},
			{
				name:    "port above 65535 is out of range",
				source:  "--api-endpoint",
				raw:     "pve1:65536",
				wantErr: `invalid --api-endpoint "pve1:65536": port 65536 is out of range [1, 65535]`,
			},
			{
				name:    "non-numeric port",
				source:  "--api-endpoint",
				raw:     "pve1:https",
				wantErr: `invalid --api-endpoint "pve1:https": port "https" is not a number`,
			},
			{
				name:    "userinfo password is masked, not echoed",
				source:  "--api-endpoint",
				raw:     "https://root:pw@pve1",
				wantErr: `invalid --api-endpoint "https://root:<redacted>": must not carry credentials`,
			},
			{
				// A Proxmox user id always carries "@realm", so this is the
				// natural way to paste one in with a password: the userinfo
				// ends at the LAST "@" of the authority, not the first.
				name:    "userinfo with a realm-qualified user id",
				source:  "--api-endpoint",
				raw:     "https://root@pam:s3cret@pve1:8006",
				wantErr: `invalid --api-endpoint "https://root@pam:<redacted>": must not carry credentials`,
			},
			{
				// The password itself contains "@", so the text after the
				// first "@" is still password, not host.
				name:    "password containing an at sign",
				source:  "--api-endpoint",
				raw:     "https://root:p@ss@pve1",
				wantErr: `invalid --api-endpoint "https://root:<redacted>": must not carry credentials`,
			},
			{
				name:   "path, query, and fragment are all rejected together",
				source: "--api-endpoint",
				raw:    "https://pve1:8006/api2?x=1",
				wantErr: `invalid --api-endpoint "https://pve1:8006/api2?x=1": ` +
					`must not carry a path, a query, or a fragment`,
			},
			{
				name:    "fragment alone is rejected",
				source:  "--api-endpoint",
				raw:     "https://pve1#frag",
				wantErr: `invalid --api-endpoint "https://pve1#frag": must not carry a path, a query, or a fragment`,
			},
			{
				name:    "empty host",
				source:  "--api-endpoint",
				raw:     "https://:8006",
				wantErr: `invalid --api-endpoint "https://:8006": host is empty`,
			},
			{
				name:    "bare host with two colons is not an IPv6 literal",
				source:  "--api-endpoint",
				raw:     "pve1:80:90",
				wantErr: `invalid --api-endpoint "pve1:80:90": want [scheme://]host[:port]`,
			},
			{
				name:    "double colon typo on a plain hostname",
				source:  "--api-endpoint",
				raw:     "pve1::8006",
				wantErr: `invalid --api-endpoint "pve1::8006": want [scheme://]host[:port]`,
			},
			{
				name:    "bracketed text that is not an IPv6 literal",
				source:  "--api-endpoint",
				raw:     "[pve1]",
				wantErr: `invalid --api-endpoint "[pve1]": want [scheme://]host[:port]`,
			},
			{
				name:    "host with embedded whitespace",
				source:  "--api-endpoint",
				raw:     "pve 1",
				wantErr: `invalid --api-endpoint "pve 1": want [scheme://]host[:port]`,
			},
			{
				name:    "link-local IPv6 literal with a zone id",
				source:  "--api-endpoint",
				raw:     "fe80::1%eth0",
				wantErr: `invalid --api-endpoint "fe80::1%eth0": want [scheme://]host[:port]`,
			},
			{
				name:    "empty port after a colon",
				source:  "--api-endpoint",
				raw:     "https://pve1:",
				wantErr: `invalid --api-endpoint "https://pve1:": want [scheme://]host[:port]`,
			},
			{
				name:    "empty port after a bracketed IPv6 literal",
				source:  "--api-endpoint",
				raw:     "[::1]:",
				wantErr: `invalid --api-endpoint "[::1]:": want [scheme://]host[:port]`,
			},
			{
				name:    "signed port is not a number",
				source:  "--api-endpoint",
				raw:     "pve1:+8006",
				wantErr: `invalid --api-endpoint "pve1:+8006": port "+8006" is not a number`,
			},
			{
				// An "@" anywhere means a password may be present, and a
				// password may itself hold "/", so masking over-reaches
				// into the rejected path rather than risk a leak.
				name:   "an at sign in the path masks from the first colon",
				source: "--api-endpoint",
				raw:    "https://pve1:8006/a:b@c",
				wantErr: `invalid --api-endpoint "https://pve1:<redacted>": ` +
					`must not carry a path, a query, or a fragment`,
			},
			{
				name:    "empty brackets",
				source:  "--api-endpoint",
				raw:     "[]",
				wantErr: `invalid --api-endpoint "[]": host is empty`,
			},
			{
				name:    "empty brackets with a port",
				source:  "--api-endpoint",
				raw:     "https://[]:8006",
				wantErr: `invalid --api-endpoint "https://[]:8006": host is empty`,
			},
			{
				// Text before "://" that is not a scheme is not a prefix to
				// keep: here it holds the password.
				name:    "colon-slash-slash inside the password",
				source:  "--api-endpoint",
				raw:     "root:s3cret://x@pve1",
				wantErr: `invalid --api-endpoint "root:<redacted>": scheme must be https or http`,
			},
			{
				// A username with a percent-encoded colon can hide a
				// password inside the username itself.
				name:    "percent-encoded username",
				source:  "--api-endpoint",
				raw:     "https://root%3As3cret@pve1",
				wantErr: `invalid --api-endpoint "https://<redacted>@pve1": must not carry credentials`,
			},
			{
				name:    "percent-encoded username ahead of a password",
				source:  "--api-endpoint",
				raw:     "https://root%3Ax:s3cret@pve1",
				wantErr: `invalid --api-endpoint "https://<redacted>": must not carry credentials`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := cli.ParseEndpoint(tc.source, tc.raw)
				require.Error(t, err)
				require.Equal(t, tc.wantErr, err.Error())
				require.Equal(t, cli.EndpointOverride{}, got)
			})
		}
	})

	t.Run("no byte of a userinfo password reaches the error", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			raw    string
			secret string
		}{
			{raw: "https://root:pa/s3cret@pve1", secret: "pa/s3cret"},
			{raw: "https://root:pa?s3cret@pve1", secret: "pa?s3cret"},
			{raw: "https://root:pa#s3cret@pve1", secret: "pa#s3cret"},
			{raw: "https://root@pam:secret@pve1:8006", secret: "secret"},
			{raw: "https://root@pam:pa/s3cret@pve1", secret: "pa/s3cret"},
			{raw: "https://root:s3cret/@pve1", secret: "s3cret/"},
			{raw: "https://root:p@ss@pve1", secret: "p@ss"},
			{raw: "https://root:pa s3cret@pve1", secret: "pa s3cret"},
			{raw: "https://root:pa\ns3cret@pve1", secret: "pa\ns3cret"},
			{raw: "https://root@pam:s3cret", secret: "s3cret"},
			{raw: "https://:s3cret@pve1", secret: "s3cret"},
			{raw: "HTTPS://root:pa/s3cret@pve1", secret: "pa/s3cret"},
			{raw: "ftp://root:pa/s3cret@pve1", secret: "pa/s3cret"},
			{raw: "https:/root:pa/s3cret@pve1", secret: "pa/s3cret"},
			{raw: "root:p@ss@pve1", secret: "p@ss"},
			{raw: "root:p@ss", secret: "p@ss"},
			{raw: "root:x/s3cret@pve1", secret: "x/s3cret"},
			{raw: "root:s3cret://x@pve1", secret: "s3cret://x"},
			{raw: "root@pam:s3cret@pve1:8006", secret: "s3cret"},
			{raw: "socks5:pmx:s3cret@proxy:1080", secret: "s3cret"},
			{raw: "socks5://pmx:s3cret@proxy:1080", secret: "s3cret"},
			{raw: "https://root:s3cret@pve1:8006/api2/json", secret: "s3cret"},
			{raw: "https://root:s3cret@pve1/api2?x=1#frag", secret: "s3cret"},
			{raw: "root:s3cret@pve1:8006/api2/json", secret: "s3cret"},
			{raw: "root:pa/s3cret@pve1/api2", secret: "pa/s3cret"},
			{raw: "https://root%3As3cret@pve1", secret: "s3cret"},
			{raw: "https://pve1:8006/a:s3cret@c", secret: "s3cret@c"},
		}

		for _, tc := range tests {
			t.Run(tc.raw, func(t *testing.T) {
				t.Parallel()
				requireNoSecret(t, tc.raw, tc.secret)
			})
		}
	})

	t.Run("a password holding any delimiter never reaches the error", func(t *testing.T) {
		t.Parallel()

		for _, delim := range []string{"/", "?", "#", "://", " ", "@", ":", "[", "]", "%40"} {
			for _, pw := range []string{delim + "s3cret", "pa" + delim + "s3cret", "s3cret" + delim + "zq9"} {
				for _, form := range []string{"https://root:%s@pve1", "root:%s@pve1", "https://root@pam:%s@pve1:8006/x"} {
					raw := fmt.Sprintf(form, pw)
					requireNoSecret(t, raw, pw)
				}
			}
		}
	})

	t.Run("userinfo password never appears in the error, even bare", func(t *testing.T) {
		t.Parallel()

		const password = "s3cret-pw"
		_, err := cli.ParseEndpoint("--api-endpoint", "https://root:"+password+"@pve1")
		require.Error(t, err)
		require.NotContains(t, err.Error(), password)
		require.Contains(t, err.Error(), "<redacted>", "error should quote the redacted form")
	})
}

// requireNoSecret asserts that ParseEndpoint rejects raw and that its error
// holds neither secret nor secret's last three bytes, so a mask that stops
// short of the end of a password fails too.
func requireNoSecret(t *testing.T, raw, secret string) {
	t.Helper()

	_, err := cli.ParseEndpoint("--api-endpoint", raw)
	require.Error(t, err, "input %q", raw)

	out := err.Error()
	require.False(t, strings.Contains(out, secret), "error %q holds the password %q", out, secret)

	tail := secret[max(len(secret)-3, 0):]
	require.False(t, strings.Contains(out, tail), "error %q holds the password tail %q", out, tail)
}
