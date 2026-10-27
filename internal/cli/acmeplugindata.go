package cli

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// Flag names shared by every ACME challenge-plugin create and update verb.
const (
	FlagACMEPluginData     = "data"
	FlagACMEPluginDataFile = "data-file"
)

// maxACMEPluginDataBytes bounds what --data-file reads. A DNS plugin's
// credential block is a handful of KEY=VALUE lines; the bound keeps a
// mistaken path such as /dev/zero from reading forever.
const maxACMEPluginDataBytes = 64 << 10

// ACMEPluginDataHelp is the paragraph the plugin create and update verbs
// append to their Long help, so all three products describe the two flags
// the same way.
const ACMEPluginDataHelp = "The DNS provider credentials are KEY=VALUE lines named by the provider's " +
	"acme.sh variables, such as CF_Token and CF_Account_ID for Cloudflare. Pass them with " +
	"--data-file PATH, or --data-file - to read standard input; pmx base64-encodes the lines " +
	"before sending them. --data takes the already base64-encoded block, either literally or as " +
	"a reference that pmx resolves: ${ENV_VAR}, $ENV_VAR, or keychain:SERVICE[/ACCOUNT]. pmx removes " +
	"any line wrapping and sends the block as standard padded base64, converting a URL-safe or " +
	"unpadded encoding. A literal " +
	"--data value lands in shell history and the process list, so prefer --data-file or a reference."

// resolveACMESecretRef resolves a ${NAME} or keychain:PATH reference. It is
// config.ResolveSecret, held in a variable so tests can drive the keychain
// branch without touching a real keychain.
var resolveACMESecretRef = config.ResolveSecret

// RegisterACMEPluginDataFlags binds --data and --data-file onto cmd and marks
// them mutually exclusive. When required is true, exactly one of the two must
// be passed.
func RegisterACMEPluginDataFlags(cmd *cobra.Command, data, dataFile *string, required bool) {
	f := cmd.Flags()
	f.StringVar(data, FlagACMEPluginData, "",
		"base64-encoded DNS plugin credentials, or a ${ENV_VAR} or keychain:PATH reference to them (sensitive)")
	f.StringVar(dataFile, FlagACMEPluginDataFile, "",
		"file of plaintext KEY=VALUE credential lines, or - for standard input; pmx base64-encodes it")
	cmd.MarkFlagsMutuallyExclusive(FlagACMEPluginData, FlagACMEPluginDataFile)
	if required {
		cmd.MarkFlagsOneRequired(FlagACMEPluginData, FlagACMEPluginDataFile)
	}
}

// ResolveACMEPluginData returns the base64-encoded plugin data to send, and
// whether either flag was passed at all. It reads only the flag that was set:
//
//   - --data-file reads plaintext KEY=VALUE lines from a file or standard
//     input and base64-encodes them (see EncodeACMEPluginDataLines).
//   - --data holding ${NAME}, $NAME, or keychain:PATH is resolved through
//     config.ResolveSecret, the resolver context secrets use. A bare $NAME
//     whose variable is unset is an error here, never a literal, because a
//     literal "$NAME" could not be valid base64 anyway.
//   - --data holding anything else is the literal value, and a warning goes
//     to stderr because the credential is now in shell history.
//
// A --data value must decode as base64, so plaintext passed to it fails
// before the API stores credentials no plugin can read. It is sent as
// standard padded base64 with no whitespace (see normalizeACMEPluginData).
// No error text ever quotes the value.
func ResolveACMEPluginData(cmd *cobra.Command, data, dataFile string) (string, bool, error) {
	fl := cmd.Flags()
	switch {
	case fl.Changed(FlagACMEPluginDataFile):
		value, err := readACMEPluginDataFile(cmd.InOrStdin(), dataFile)
		if err != nil {
			return "", true, err
		}
		return value, true, nil

	case fl.Changed(FlagACMEPluginData):
		value, err := resolveACMEPluginDataValue(cmd.ErrOrStderr(), data)
		if err != nil {
			return "", true, err
		}
		return value, true, nil

	default:
		return "", false, nil
	}
}

// resolveACMEPluginDataValue resolves a --data value and checks it is base64.
func resolveACMEPluginDataValue(stderr io.Writer, data string) (string, error) {
	value := data
	if config.IsSecretReference(data) {
		ref := data
		// Rewrite $NAME as ${NAME} so ResolveSecret reports an unset
		// variable instead of falling back to the literal "$NAME".
		if strings.HasPrefix(ref, "$") && !strings.HasPrefix(ref, "${") {
			ref = "${" + ref[1:] + "}"
		}
		resolved, err := resolveACMESecretRef(ref)
		if err != nil {
			return "", fmt.Errorf("--%s: %w", FlagACMEPluginData, err)
		}
		value = resolved
	} else if data != "" {
		_, _ = fmt.Fprintf(stderr, "WARN: --%s carries the plugin credentials inline, where shell history "+
			"and the process list can see them; prefer --%s, ${ENV_VAR}, or keychain:PATH\n",
			FlagACMEPluginData, FlagACMEPluginDataFile)
	}

	value = stripWhitespace(value)
	if value == "" {
		return "", fmt.Errorf("--%s is empty", FlagACMEPluginData)
	}
	normalized, ok := normalizeACMEPluginData(value)
	if !ok {
		return "", fmt.Errorf("--%s is not base64-encoded; pass the plaintext KEY=VALUE lines with --%s "+
			"instead, which encodes them", FlagACMEPluginData, FlagACMEPluginDataFile)
	}
	return normalized, nil
}

// readACMEPluginDataFile reads --data-file, from stdin when path is "-", and
// returns the encoded block.
func readACMEPluginDataFile(stdin io.Reader, path string) (string, error) {
	var (
		src    io.Reader
		source string
	)
	switch path {
	case "":
		return "", fmt.Errorf("--%s needs a path, or - for standard input", FlagACMEPluginDataFile)
	case "-":
		if stdin == nil {
			return "", fmt.Errorf("--%s -: no standard input to read", FlagACMEPluginDataFile)
		}
		src, source = stdin, "standard input"
	default:
		f, err := os.Open(path) //nolint:gosec // operator-supplied credential file
		if err != nil {
			return "", fmt.Errorf("--%s: %w", FlagACMEPluginDataFile, err)
		}
		defer func() { _ = f.Close() }()
		src, source = f, path
	}

	raw, err := io.ReadAll(io.LimitReader(src, maxACMEPluginDataBytes+1))
	if err != nil {
		return "", fmt.Errorf("--%s: read %s: %w", FlagACMEPluginDataFile, source, err)
	}
	if len(raw) > maxACMEPluginDataBytes {
		return "", fmt.Errorf("--%s: %s is larger than %d bytes, which is far more than a DNS plugin's "+
			"credentials; check the path", FlagACMEPluginDataFile, source, maxACMEPluginDataBytes)
	}

	encoded, err := EncodeACMEPluginDataLines(string(raw))
	if err != nil {
		return "", fmt.Errorf("--%s: %s: %w", FlagACMEPluginDataFile, source, err)
	}
	return encoded, nil
}

// errACMEDataAlreadyEncoded explains the one input shape the line check
// cannot tell apart on its own: a base64 block whose padding reads as
// KEY=VALUE.
var errACMEDataAlreadyEncoded = errors.New("the content is already base64-encoded; " +
	"pass it with --data instead, or put the plaintext KEY=VALUE lines in the file")

// EncodeACMEPluginDataLines validates plaintext DNS plugin credentials and
// returns them base64-encoded with padding, the form PVE, PBS, and PDM all
// accept. Line endings are normalised to "\n", blank lines and lines starting
// with "#" are dropped, and every other line must be KEY=VALUE with a shell
// variable name for KEY and a non-empty value, since acme.sh reads each one
// as an environment variable. Errors name the line number and never quote
// its content.
func EncodeACMEPluginDataLines(content string) (string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")

	type numbered struct {
		n    int
		text string
	}
	var candidates []numbered
	for i, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		candidates = append(candidates, numbered{n: i + 1, text: line})
	}

	if len(candidates) == 0 {
		return "", errors.New("no KEY=VALUE lines found")
	}
	// Checked before the line rules, because an unpadded block has no "="
	// at all and would otherwise be reported as a malformed line.
	if len(candidates) == 1 && looksBase64Encoded(candidates[0].text) {
		return "", errACMEDataAlreadyEncoded
	}

	lines := make([]string, 0, len(candidates))
	for _, c := range candidates {
		key, value, ok := strings.Cut(c.text, "=")
		if !ok {
			return "", fmt.Errorf("line %d is not KEY=VALUE", c.n)
		}
		if !isShellVarName(key) {
			return "", fmt.Errorf("line %d: the key is not a variable name such as CF_Token", c.n)
		}
		if value == "" {
			return "", fmt.Errorf("line %d: the value is empty", c.n)
		}
		lines = append(lines, c.text)
	}

	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n"))), nil
}

// looksBase64Encoded reports whether line is a base64 block, padded or not,
// whose decoded text holds a KEY=VALUE pair. A real credential line such as
// CF_Token=abc never decodes, because "=" may only appear as trailing
// padding, and an unpadded alphabet has no "=" at all.
func looksBase64Encoded(line string) bool {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(line); err == nil && strings.Contains(string(decoded), "=") {
			return true
		}
	}
	return false
}

// normalizeACMEPluginData decodes s, which holds no whitespace, in any of the
// four base64 alphabets and padding styles, and returns it re-encoded as
// standard padded base64, with ok false when no alphabet decodes it. PBS and
// PDM require standard padded base64. PVE decodes the value with Perl's
// MIME::Base64, which silently skips any character outside the standard
// alphabet, so a URL-safe "-" or "_" would corrupt the credentials there
// rather than fail. Re-encoding gives every product the one form it reads
// correctly, and it loses nothing, because each accepted form decodes to the
// same bytes.
func normalizeACMEPluginData(s string) (string, bool) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(s); err == nil && len(decoded) > 0 {
			return base64.StdEncoding.EncodeToString(decoded), true
		}
	}
	return "", false
}

// stripWhitespace removes every whitespace character from s. A base64 block
// wrapped at 76 columns, as GNU base64 writes it, or copied with stray
// spaces, is still one value, and Go's decoders skip only "\r" and "\n"
// while PBS and PDM reject any whitespace.
func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// isShellVarName reports whether name is a valid shell variable name: ASCII
// letters, digits, and underscores, not starting with a digit.
func isShellVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
		digit := c >= '0' && c <= '9'
		if !letter && (!digit || i == 0) {
			return false
		}
	}
	return true
}
