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

func TestNewManualVerifyCallback_TTY_FirstLineDecidesTrailingDataIgnored(t *testing.T) {
	t.Parallel()

	// Two lines are available; the callback's decision comes from the first
	// line only. bufio.Reader.ReadString stops returning at the first '\n',
	// so the second line plays no part in the true/false result below. Note
	// this does NOT assert the second line survives for a later prompt: a
	// fresh bufio.Reader wraps in on every call, and bufio typically reads
	// ahead into its own internal buffer in one underlying Read, so a
	// single-pass reader like this bytes.Buffer is fully drained by this one
	// call and the second line would not be available to a subsequent call.
	var promptOut bytes.Buffer
	in := bytes.NewBufferString("y\nsecond-line-ignored-for-decision\n")

	cb := apiclient.NewManualVerifyCallback(&promptOut, in, func() bool { return true })

	got := cb(sampleRequest())
	require.True(t, got, "decision must be driven by the first line (\"y\") alone")
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

	// The encoded file name must stay a single path component directly under
	// the fingerprints directory: no path separators survive, so the result
	// cannot escape that directory regardless of how the context name is
	// spelled. Each '.' encodes to "_2E" and each '/' encodes to "_2F".
	require.Equal(t, "/home/user/.config/pve/fingerprints/_2E_2E_2F_2E_2E_2Fetc_2Fpasswd.json", got)
	require.NotContains(t, got[len("/home/user/.config/pve/fingerprints/"):], "/")
	require.NotContains(t, got[len("/home/user/.config/pve/fingerprints/"):], "\\")
}

func TestFingerprintCachePath_EmptyContextNameCollapsesToPlaceholder(t *testing.T) {
	t.Parallel()

	got := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", "")

	require.Equal(t, "/home/user/.config/pve/fingerprints/_.json", got)
}

// ---------------------------------------------------------------------------
// FingerprintCachePath / fingerprintCacheFileName — collision resistance
// ---------------------------------------------------------------------------

func TestFingerprintCachePath_DifferingOnlyByDisallowedChar_NoCollision(t *testing.T) {
	t.Parallel()

	// Prior to the fix, every rune outside [A-Za-z0-9-_] collapsed to a
	// single '_', so all four of these distinct context names mapped to the
	// same cache file ("prod_1.json"), letting one context's accepted TLS
	// fingerprint be silently reused (or overwritten) by another. Each pair
	// must now produce a distinct path.
	names := []string{"prod.1", "prod_1", "prod:1", "prod/1"}

	seen := make(map[string]string, len(names))

	for _, name := range names {
		path := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", name)

		if other, ok := seen[path]; ok {
			t.Fatalf("context names %q and %q collide on cache path %q", other, name, path)
		}

		seen[path] = name
	}

	require.Len(t, seen, len(names))
}

func TestFingerprintCachePath_PassthroughAlphabetUnchanged(t *testing.T) {
	t.Parallel()

	// Context names using only the passthrough alphabet (ASCII letters,
	// digits, and '-') must encode byte-for-byte identically to the pre-fix
	// sanitizer, so existing cache files for these names remain valid after
	// the fix.
	got := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", "Prod-Cluster-01")

	require.Equal(t, "/home/user/.config/pve/fingerprints/Prod-Cluster-01.json", got)
}

func TestFingerprintCachePath_EncodingIsInjectiveForAdversarialInputs(t *testing.T) {
	t.Parallel()

	// These names are deliberately crafted so that a naive or partial escape
	// scheme could produce the same output for more than one of them (e.g. if
	// a literal '_' were left as a passthrough character instead of being
	// escaped itself, "a_5Fb" could collide with the encoded form of "a_b").
	names := []string{"a_b", "a.b", "a__b", "a_5Fb", "a_2Eb"}

	seen := make(map[string]string, len(names))

	for _, name := range names {
		path := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", name)

		if other, ok := seen[path]; ok {
			t.Fatalf("context names %q and %q collide on cache path %q", other, name, path)
		}

		seen[path] = name
	}

	require.Len(t, seen, len(names))
}

func TestFingerprintCachePath_EncodingIsCollisionFreeOverRandomSet(t *testing.T) {
	t.Parallel()

	// A broader, fuzz-flavored set of context names covering reserved
	// characters, hex-digit look-alikes, Unicode, and empty/short strings.
	// The full resulting path set must contain no duplicates.
	names := []string{
		"prod", "prod-1", "prod.1", "prod_1", "prod:1", "prod/1", "prod\\1",
		"prod 1", "prod\t1", "prod\n1", "PROD.1", "Prod_1",
		"a_2E", "a.", "a_5F", "a_",
		"staging.eu", "staging_eu", "staging-eu",
		"..", "../..", "../../etc/passwd", "/etc/passwd", "\\etc\\passwd",
		"ctx-ü", "ctx-ü", "日本語", "emoji-😀",
		"", "_", "-", "__", "--",
		"a", "A", "0", "9",
	}

	seen := make(map[string]string, len(names))

	for _, name := range names {
		path := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", name)

		if other, ok := seen[path]; ok && other != name {
			t.Fatalf("context names %q and %q collide on cache path %q", other, name, path)
		}

		seen[path] = name
	}
}

func TestFingerprintCachePath_MultiByteRuneEncodesPerByteAndStaysFilesystemSafe(t *testing.T) {
	t.Parallel()

	// 'ü' (U+00FC) is a two-byte UTF-8 sequence (0xC3 0xBC). Each byte of the
	// multi-byte rune must be escaped independently, and the resulting file
	// name must contain only ASCII letters, digits, '-', and '_' so it is
	// safe on any filesystem regardless of locale/encoding support.
	got := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", "ctx-ü")

	require.Equal(t, "/home/user/.config/pve/fingerprints/ctx-_C3_BC.json", got)

	fileName := got[len("/home/user/.config/pve/fingerprints/") : len(got)-len(".json")]
	for _, r := range fileName {
		safe := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		require.True(t, safe, "file name %q contains unsafe rune %q", fileName, r)
	}
}

func FuzzFingerprintCacheFileName(f *testing.F) {
	seeds := []string{
		"prod", "prod.1", "prod_1", "prod:1", "prod/1", "../../etc/passwd",
		"", "_", "a_b", "a.b", "ctx-ü", "日本語",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		// Must never panic, and must never produce a path separator that
		// could escape the fingerprints directory.
		got := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", name)

		fileName := got[len("/home/user/.config/pve/fingerprints/") : len(got)-len(".json")]
		require.NotContains(t, fileName, "/")
		require.NotContains(t, fileName, "\\")

		// Encoding a second, different name must not collide with this one.
		other := name + "-distinct-suffix-marker"
		gotOther := apiclient.FingerprintCachePath("/home/user/.config/pve/config.yml", other)
		require.NotEqual(t, got, gotOther)
	})
}
