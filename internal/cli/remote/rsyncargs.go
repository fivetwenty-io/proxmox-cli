package remote

import (
	"fmt"
	"strconv"
	"strings"
)

// pmxFlagValues holds the pmx-owned flag values extracted from the front of a
// `pmx rsync` argv by extractPMXFlags, split by where the caller must apply
// them.
type pmxFlagValues struct {
	// Root holds values destined for cmd.Root().PersistentFlags().Set:
	// "context", "config", "insecure", "debug".
	Root map[string]string
	// SSH holds values destined for the rsync command's own long-only
	// --ssh-*/--no-strict flags via cmd.Flags().Set.
	SSH map[string]string
	// Help is true when -h/--help was seen; the caller must print help and
	// return without building *cli.Deps or running rsync.
	Help bool
}

// pmxFlagSpec describes one pmx-owned flag recognised by extractPMXFlags.
type pmxFlagSpec struct {
	// names lists every token that selects this flag, e.g. {"-c", "--context"}.
	names []string
	// takesValue is true when the flag consumes a following value (either
	// "--flag value" or "--flag=value"); false for boolean flags.
	takesValue bool
	// target is "root" or "ssh", selecting which map in pmxFlagValues the
	// extracted value is stored under.
	target string
	// dest is the flag name used when applying the value (cmd.Root().
	// PersistentFlags().Set(dest, ...) or cmd.Flags().Set(dest, ...)).
	dest string
}

// pmxFlagTable lists every flag extractPMXFlags recognises at the front of a
// `pmx rsync` argv. "-c/--context", "--config", "--insecure", and "--debug"
// apply to the shared root persistent flag set; the "--ssh-*"/"--no-strict"
// flags are long-only because rsync itself owns the short forms -l, -p, -i.
var pmxFlagTable = []pmxFlagSpec{
	{names: []string{"-c", "--context"}, takesValue: true, target: "root", dest: "context"},
	{names: []string{"--config"}, takesValue: true, target: "root", dest: "config"},
	{names: []string{"--insecure"}, takesValue: false, target: "root", dest: "insecure"},
	{names: []string{"--debug"}, takesValue: false, target: "root", dest: "debug"},
	{names: []string{"--ssh-user"}, takesValue: true, target: "ssh", dest: "ssh-user"},
	{names: []string{"--ssh-port"}, takesValue: true, target: "ssh", dest: "ssh-port"},
	{names: []string{"--ssh-identity"}, takesValue: true, target: "ssh", dest: "ssh-identity"},
	{names: []string{"--ssh-agent"}, takesValue: false, target: "ssh", dest: "ssh-agent"},
	{names: []string{"--no-strict"}, takesValue: false, target: "ssh", dest: "no-strict"},
}

// extractPMXFlags scans args from the front for pmx-owned flags (context,
// config, insecure, debug, help, and the long-only ssh-* connection flags),
// stopping at the first token that is not one of them — including a bare
// "--", which extractPMXFlags leaves untouched in rest for rsync itself to
// interpret. Recognised value-taking flags accept both "--flag value" and
// "--flag=value" forms; "-c" (the only short flag here) only accepts the
// separate-token form, since pmx does not use short flags with "=value".
// Boolean flags (--insecure, --debug) also accept an inline "=true"/"=false"
// form, parsed via strconv.ParseBool; any other inline value is a parse
// error naming the flag. "-h"/"--help" abort extraction immediately: Help is
// set and no further tokens are consumed or classified.
func extractPMXFlags(args []string) (pmxFlagValues, []string, error) {
	vals := pmxFlagValues{Root: map[string]string{}, SSH: map[string]string{}}

	i := 0
	for i < len(args) {
		tok := args[i]

		if tok == "-h" || tok == "--help" {
			vals.Help = true
			return vals, args[i:], nil
		}

		name, inlineValue, hasInline := splitEquals(tok)
		spec := lookupFlagSpec(name)
		if spec == nil {
			break
		}

		var value string
		switch {
		case spec.takesValue && hasInline:
			value = inlineValue
		case spec.takesValue:
			if i+1 >= len(args) {
				return pmxFlagValues{}, nil, fmt.Errorf("flag %s requires a value", name)
			}
			i++
			value = args[i]
		case hasInline:
			parsed, perr := strconv.ParseBool(inlineValue)
			if perr != nil {
				return pmxFlagValues{}, nil, fmt.Errorf("flag %s expects a boolean value (true/false), got %q", name, inlineValue)
			}
			value = strconv.FormatBool(parsed)
		default:
			value = "true"
		}

		// A value-taking flag's extracted value starting with '-' is almost
		// always a misplaced flag meant for rsync itself (e.g. `pmx rsync -c
		// -av ...` meaning rsync's -a/-v, not a context literally named
		// "-av"), which would otherwise surface only as a baffling later
		// error (e.g. `context "-av" not found`). Reject it here with a
		// message that names the actual mistake.
		if spec.takesValue && strings.HasPrefix(value, "-") {
			return pmxFlagValues{}, nil, valueLooksLikeFlagError(name, value)
		}

		switch spec.target {
		case "root":
			vals.Root[spec.dest] = value
		case "ssh":
			vals.SSH[spec.dest] = value
		}
		i++
	}

	return vals, args[i:], nil
}

// valueLooksLikeFlagError builds the error extractPMXFlags returns when a
// value-taking flag's extracted value itself starts with '-'. "-c"/"--context"
// gets a targeted message steering the user toward rsync's own equivalent
// flag (since --checksum/-c is a common source of this exact confusion);
// every other value-taking flag gets a generic "expects a value" message.
func valueLooksLikeFlagError(name, value string) error {
	if name == "-c" || name == "--context" {
		return fmt.Errorf(
			"-c/--context expects a context name, got %q (for rsync's checksum option use --checksum, "+
				"or pass it after the first rsync argument)", value)
	}
	return fmt.Errorf("%s expects a value, got %q", name, value)
}

// splitEquals splits a "--flag=value" token into its name and value. Short
// flags (single leading "-") never use the attached form here, so name is
// tok unchanged and hasValue is false for anything not starting with "--".
func splitEquals(tok string) (name, value string, hasValue bool) {
	if !strings.HasPrefix(tok, "--") {
		return tok, "", false
	}
	if idx := strings.IndexByte(tok, '='); idx >= 0 {
		return tok[:idx], tok[idx+1:], true
	}
	return tok, "", false
}

// lookupFlagSpec returns the pmxFlagSpec matching name, or nil if name is not
// a recognised pmx-owned flag.
func lookupFlagSpec(name string) *pmxFlagSpec {
	for i := range pmxFlagTable {
		for _, n := range pmxFlagTable[i].names {
			if n == name {
				return &pmxFlagTable[i]
			}
		}
	}
	return nil
}

// rsyncOperand describes one non-flag token from a rsync argv, classified as
// either a local path or a "[user@]node:path" remote spec.
type rsyncOperand struct {
	// Index is the token's position in the tokens slice passed to
	// classifyRsyncArgs, so the caller can rewrite that slice in place.
	Index int
	// Remote is true when the operand matched the host:path detection rule.
	Remote bool
	// User is the explicit "user@" prefix, or "" if the operand had none
	// (the caller then applies its own default login user).
	User string
	// Node is the node name/address portion, brackets stripped for a
	// bracketed IPv6 literal. Only meaningful when Remote is true.
	Node string
	// Path is the remainder after the split colon when Remote, or the token
	// unchanged (including any misdetected "user@" prefix) when not.
	Path string
}

// rsyncShortValueOpts lists the single-letter rsync(1) options that consume a
// SEPARATE following argv token as their value, taken from `rsync --help`
// (rsync 3.4.4, https://rsync.samba.org/): -B/--block-size, -e/--rsh,
// -f/--filter, -M/--remote-option, -T/--temp-dir, -@/--modify-window. -e is
// listed for documentation fidelity only: isReservedRemoteShellFlag already
// rejects every -e/--rsh occurrence (standalone or clustered) before
// rsyncOptionValueSkip is ever consulted, so its entry here is never actually
// exercised.
const rsyncShortValueOpts = "BefMT@"

// rsyncLongValueOptsWithSeparateForm lists every rsync(1) long option that
// accepts its value via a SEPARATE following argv token ("--opt value"), i.e.
// every option in `rsync --help` (rsync 3.4.4, https://rsync.samba.org/) shown
// with a "=VALUE" suffix. A "--opt=value" token already carries its value
// inline and needs no entry here to avoid over-consuming a token (see
// rsyncOptionValueSkip). --mkpath is deliberately absent: it takes no value.
//
// Any long option NOT in this map is a known, documented limitation: a value
// like "--some-unlisted-opt pve1:secret" is still scanned as if "pve1:secret"
// were a standalone operand on the next iteration, and gets rewritten if it
// happens to match the host:path shape. Prefer "--opt=value" for such options
// when the value contains ':'.
var rsyncLongValueOptsWithSeparateForm = map[string]bool{
	"--address":          true,
	"--backup-dir":       true,
	"--block-size":       true,
	"--bwlimit":          true,
	"--checksum-choice":  true,
	"--checksum-seed":    true,
	"--chmod":            true,
	"--chown":            true,
	"--compare-dest":     true,
	"--compress-choice":  true,
	"--compress-level":   true,
	"--compress-threads": true,
	"--contimeout":       true,
	"--copy-as":          true,
	"--copy-dest":        true,
	"--debug":            true,
	"--early-input":      true,
	"--exclude":          true,
	"--exclude-from":     true,
	"--files-from":       true,
	"--filter":           true,
	"--groupmap":         true,
	"--iconv":            true,
	"--include":          true,
	"--include-from":     true,
	"--info":             true,
	"--link-dest":        true,
	"--log-file":         true,
	"--log-file-format":  true,
	"--max-alloc":        true,
	"--max-delete":       true,
	"--max-size":         true,
	"--min-size":         true,
	"--modify-window":    true,
	"--only-write-batch": true,
	"--out-format":       true,
	"--outbuf":           true,
	"--partial-dir":      true,
	"--password-file":    true,
	"--port":             true,
	"--protocol":         true,
	"--read-batch":       true,
	"--remote-option":    true,
	"--rsh":              true,
	"--rsync-path":       true,
	"--skip-compress":    true,
	"--sockopts":         true,
	"--stderr":           true,
	"--stop-after":       true,
	"--stop-at":          true,
	"--suffix":           true,
	"--temp-dir":         true,
	"--timeout":          true,
	"--usermap":          true,
	"--write-batch":      true,
}

// rsyncOptionValueSkip reports whether tok is an rsync(1) option token that
// consumes the FOLLOWING argv token as a separate-argument value, so that
// classifyRsyncArgs can skip over that value instead of misclassifying it as
// an operand (the concrete bugs this guards against: "--chown a:b ..." and
// "--exclude pve1:secret ..." rewriting or rejecting the value as if it were
// a node:path operand). A "--opt=value" token already carries its value
// inline (checked via splitEquals) and never consumes a following token. A
// short-option cluster ("-avB") consumes the next token only when its first
// character found in rsyncShortValueOpts is the LAST character of the
// cluster; any trailing characters instead are that letter's attached value
// and the cluster stands alone (e.g. "-avB1024" needs nothing further) — this
// mirrors sshcmd.SplitPassthrough's clustered-option handling.
func rsyncOptionValueSkip(tok string) bool {
	if strings.HasPrefix(tok, "--") {
		name, _, hasInline := splitEquals(tok)
		if hasInline {
			return false
		}
		return rsyncLongValueOptsWithSeparateForm[name]
	}

	body := tok[1:]
	for j := 0; j < len(body); j++ {
		if strings.IndexByte(rsyncShortValueOpts, body[j]) < 0 {
			continue
		}
		return j == len(body)-1
	}
	return false
}

// isReservedRemoteShellFlag reports whether tok is (or attaches a value to)
// rsync's own -e/--rsh remote-shell option, which pmx reserves for its own
// injected "-e ssh ..." (see sshcmd.RemoteShell). "-e" is rsync's only short
// option using that letter, so any short-option cluster containing it (e.g.
// "-ae", attached "-essh") is rejected too, matching rsync's own getopt
// clustering.
func isReservedRemoteShellFlag(tok string) bool {
	if tok == "--rsh" || strings.HasPrefix(tok, "--rsh=") {
		return true
	}
	if strings.HasPrefix(tok, "--") {
		return false
	}
	if !strings.HasPrefix(tok, "-") || tok == "-" {
		return false
	}
	return strings.ContainsRune(tok[1:], 'e')
}

// classifyRsyncArgs scans tokens (the rsync argv remainder after
// extractPMXFlags) for the remote "node:path" operand(s) `pmx rsync` expects.
// Every non-flag token is classified via classifyOperand. Tokens starting
// with "-" are assumed to be rsync's own flags; a value-taking flag's
// SEPARATE-token value (per rsyncOptionValueSkip's arity table, mirroring
// sshcmd.SplitPassthrough's approach for ssh) is skipped along with it so it
// is never misclassified as an operand — e.g. "--chown a:b" or "--exclude
// pve1:secret" no longer have their value mistaken for a node:path operand or
// rewritten as one. Any reserved -e/--rsh is rejected outright since pmx
// always injects its own remote-shell command. A bare "--" (rsync/popt's
// unconditional end-of-options marker) switches every remaining token to a
// forced operand, including ones starting with "-": rsync itself treats them
// as literal filenames once "--" has been seen, so no flag or reserved-flag
// classification applies to them here either.
//
// All remote operands found must name the SAME node — rsync allows several
// sources but they must all come from (or go to) one host — and at least one
// remote operand is required, since `pmx rsync` always targets exactly one
// PVE node. Both violations are reported as errors; the caller must not
// attempt to resolve or exec rsync when classifyRsyncArgs fails.
func classifyRsyncArgs(tokens []string) (operands []rsyncOperand, node string, err error) {
	forcedOperand := false

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		if !forcedOperand {
			if tok == "--" {
				forcedOperand = true
				continue
			}

			if isReservedRemoteShellFlag(tok) {
				return nil, "", fmt.Errorf(
					"flag %q is reserved: pmx injects its own remote-shell command; "+
						"use --ssh-user/--ssh-port/--ssh-identity/--ssh-agent/--no-strict instead", tok)
			}

			if strings.HasPrefix(tok, "-") {
				if rsyncOptionValueSkip(tok) {
					i++ // the next token is tok's value, not an operand
				}
				continue
			}
		}

		op := classifyOperand(i, tok)
		operands = append(operands, op)

		if !op.Remote {
			continue
		}
		switch {
		case node == "":
			node = op.Node
		case node != op.Node:
			return nil, "", fmt.Errorf(
				"rsync operands reference different nodes %q and %q; all remote operands must name the same node",
				node, op.Node)
		}
	}

	if node == "" {
		return nil, "", fmt.Errorf("no remote (node:path) operand found; pmx rsync requires exactly one PVE node target")
	}

	return operands, node, nil
}

// classifyOperand classifies a single non-flag token at position index.
func classifyOperand(index int, tok string) rsyncOperand {
	if strings.HasPrefix(tok, "rsync://") {
		// rsync daemon URL syntax; not our node:path rewriting target.
		return rsyncOperand{Index: index, Path: tok}
	}

	rest := tok
	user := ""
	scanLimit := len(rest)
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		scanLimit = slash
	}
	if at := strings.IndexByte(rest[:scanLimit], '@'); at >= 0 {
		user = rest[:at]
		rest = rest[at+1:]
	}

	split, ok := findHostSplit(rest)
	if !ok {
		return rsyncOperand{Index: index, Path: tok}
	}

	node := strings.TrimSuffix(strings.TrimPrefix(rest[:split], "["), "]")
	path := rest[split+1:]

	return rsyncOperand{Index: index, Remote: true, User: user, Node: node, Path: path}
}

// findHostSplit locates the colon that separates a "[user@]host:path" rsync
// operand's host from its path, honouring rsync's own detection rule: a
// bracketed IPv6 literal's split colon is the one immediately following the
// closing "]"; otherwise the split is the first ":" provided it occurs
// before any "/" (a colon after the first slash belongs to the path, not a
// host separator, e.g. local path "./x:y").
func findHostSplit(s string) (idx int, ok bool) {
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end >= 0 && end+1 < len(s) && s[end+1] == ':' {
			return end + 1, true
		}
		return 0, false
	}

	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return 0, false
	}
	if slash := strings.IndexByte(s, '/'); slash >= 0 && slash < colon {
		return 0, false
	}
	return colon, true
}
