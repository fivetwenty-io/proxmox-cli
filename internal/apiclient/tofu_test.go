package apiclient_test

import (
	"bytes"
	"io"
	"testing"

	pve "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/client"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
)

// blockingReader fails the test if Read is ever called, so a test using it
// proves the code under test never attempts to read from stdin. A real
// blocking os.Stdin.Read would hang forever in a non-TTY invocation with no
// piped data; this stands in for that scenario without risking a hung test.
type blockingReader struct {
	t *testing.T
}

func (r *blockingReader) Read(_ []byte) (int, error) {
	r.t.Helper()
	r.t.Fatal("unexpected Read call: non-TTY path must not read from input")
	return 0, io.EOF
}

func sampleRequest() pve.FingerprintVerificationRequest {
	return pve.FingerprintVerificationRequest{
		Fingerprint: "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99",
		Host:        "pve.example.com",
	}
}

// ---------------------------------------------------------------------------
// NewManualVerifyCallback — non-TTY path (RK-03 fail-closed guarantee)
// ---------------------------------------------------------------------------

func TestNewManualVerifyCallback_NonTTY_RejectsWithoutReadingOrPrompting(t *testing.T) {
	t.Parallel()

	var promptOut bytes.Buffer
	in := &blockingReader{t: t}

	cb := apiclient.NewManualVerifyCallback(&promptOut, in, func() bool { return false })

	// If this call ever reaches in.Read, blockingReader fails the test instead
	// of hanging, proving the non-TTY path cannot block on stdin.
	got := cb(sampleRequest())

	require.False(t, got, "non-TTY path must reject the certificate")
	require.Empty(t, promptOut.String(), "non-TTY path must not write a prompt")
}

func TestNewManualVerifyCallback_NilIsTTY_TreatedAsNonTTY(t *testing.T) {
	t.Parallel()

	var promptOut bytes.Buffer
	in := &blockingReader{t: t}

	cb := apiclient.NewManualVerifyCallback(&promptOut, in, nil)

	got := cb(sampleRequest())

	require.False(t, got, "a nil isTTY func must be treated as non-interactive and reject")
	require.Empty(t, promptOut.String())
}

// ---------------------------------------------------------------------------
// NewManualVerifyCallback — TTY path
// ---------------------------------------------------------------------------

func TestNewManualVerifyCallback_TTY_PromptsAndDecides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{name: "lowercase y", answer: "y\n", want: true},
		{name: "lowercase yes", answer: "yes\n", want: true},
		{name: "uppercase Y", answer: "Y\n", want: true},
		{name: "mixed case Yes", answer: "Yes\n", want: true},
		{name: "uppercase YES", answer: "YES\n", want: true},
		{name: "padded with whitespace", answer: "  y  \n", want: true},
		{name: "lowercase n", answer: "n\n", want: false},
		{name: "no rejects", answer: "no\n", want: false},
		{name: "empty line", answer: "\n", want: false},
		{name: "garbage input", answer: "sure\n", want: false},
		{name: "no trailing newline (EOF)", answer: "y", want: true},
		{name: "immediate EOF, no data at all", answer: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var promptOut bytes.Buffer
			in := bytes.NewBufferString(tc.answer)

			cb := apiclient.NewManualVerifyCallback(&promptOut, in, func() bool { return true })

			got := cb(sampleRequest())

			require.Equal(t, tc.want, got)
			require.Contains(t, promptOut.String(), "pve.example.com",
				"prompt must include the host")
			require.Contains(t, promptOut.String(), "AA:BB:CC:DD:EE:FF",
				"prompt must include the fingerprint")
		})
	}
}

func TestNewManualVerifyCallback_TTY_ReadsOnlyOneLine(t *testing.T) {
	t.Parallel()

	// Two lines are available; only the first is consumed by a single
	// invocation of the callback, leaving the rest for a subsequent prompt
	// (e.g. a retry) rather than silently draining stdin.
	var promptOut bytes.Buffer
	in := bytes.NewBufferString("y\nsecond-line-untouched\n")

	cb := apiclient.NewManualVerifyCallback(&promptOut, in, func() bool { return true })

	got := cb(sampleRequest())
	require.True(t, got)
}

// ---------------------------------------------------------------------------
// FingerprintCachePath
// ---------------------------------------------------------------------------

func TestFingerprintCachePath_PerContextUnderConfigDir(t *testing.T) {
	t.Parallel()

	got := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", "prod")

	require.Equal(t, "/home/user/.config/pve/fingerprints/prod.json", got)
}

func TestFingerprintCachePath_DistinctContextsDistinctFiles(t *testing.T) {
	t.Parallel()

	a := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", "prod")
	b := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", "staging")

	require.NotEqual(t, a, b)
}

func TestFingerprintCachePath_SanitizesUnsafeContextNameCharacters(t *testing.T) {
	t.Parallel()

	got := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", "../../etc/passwd")

	// The sanitized file name must stay a single path component directly under
	// the fingerprints directory: no path separators survive, so the result
	// cannot escape that directory regardless of how the context name is spelled.
	require.Equal(t, "/home/user/.config/pve/fingerprints/______etc_passwd.json", got)
}

func TestFingerprintCachePath_EmptyContextNameCollapsesToPlaceholder(t *testing.T) {
	t.Parallel()

	got := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", "")

	require.Equal(t, "/home/user/.config/pve/fingerprints/_.json", got)
}
