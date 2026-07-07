package lxcconf

import (
	"fmt"
	"strings"
)

// Config keys for the raw LXC capability whitelist/blacklist lines. PVE writes
// them as low-level passthrough entries in the head of the guest config.
const (
	KeyCapKeep = "lxc.cap.keep"
	KeyCapDrop = "lxc.cap.drop"
)

// Capability edit modes reported by GetCaps and accepted by SetCaps.
const (
	// ModeDefault means the config sets neither lxc.cap.keep nor lxc.cap.drop,
	// so the container runs with PVE's default capability set.
	ModeDefault = "default"
	// ModeKeep means lxc.cap.keep is present: only the listed capabilities are
	// kept, everything else is dropped (an allowlist).
	ModeKeep = "keep"
	// ModeDrop means lxc.cap.drop is present: the listed capabilities are
	// dropped from PVE's default set (a blocklist).
	ModeDrop = "drop"
)

// CapsState is the effective capability configuration of the mutable head
// (pre-section) portion of a guest config.
type CapsState struct {
	Mode string   // ModeDefault, ModeKeep, or ModeDrop
	Keep []string // effective lxc.cap.keep list (accumulated, reset-honoring)
	Drop []string // effective lxc.cap.drop list (accumulated, reset-honoring)
}

// GetCaps parses the head of content (everything before the first section
// header) and reports the effective capability state. Repeated lxc.cap.drop
// lines accumulate; an empty-valued drop line resets the accumulated drop list.
// Repeated lxc.cap.keep lines accumulate; a "none" token resets the accumulated
// keep list (the LXC sentinel for "keep nothing"). Because LXC treats keep and
// drop as mutually exclusive, a head that sets both is reported as an error.
func GetCaps(content string) (CapsState, error) {
	head, _ := splitHead(content)

	var keep, drop []string
	var sawKeep, sawDrop bool
	for _, ln := range strings.Split(head, "\n") {
		key, val, ok := parseConfLine(ln)
		if !ok {
			continue
		}
		switch key {
		case KeyCapKeep:
			sawKeep = true
			keep = accumulateKeep(keep, val)
		case KeyCapDrop:
			sawDrop = true
			drop = accumulateDrop(drop, val)
		}
	}

	if sawKeep && sawDrop {
		return CapsState{}, fmt.Errorf(
			"config sets both %s and %s, which LXC treats as mutually exclusive", KeyCapKeep, KeyCapDrop)
	}

	state := CapsState{Mode: ModeDefault, Keep: keep, Drop: drop}
	switch {
	case sawKeep:
		state.Mode = ModeKeep
	case sawDrop:
		state.Mode = ModeDrop
	}
	return state, nil
}

// SetCaps rewrites the capability configuration in the head of content to a
// single canonical line of the given mode (ModeKeep or ModeDrop) listing caps,
// and removes every existing lxc.cap.keep/lxc.cap.drop line of either mode. The
// new line takes the position of the first capability line it replaces, or is
// appended to the end of the head when none existed. Cap names are normalized
// and validated; duplicates collapse, preserving first-seen order. Everything
// else in content — comments, blank lines, ordering, and every [section] tail —
// is preserved byte-for-byte.
func SetCaps(content, mode string, caps []string) (string, error) {
	var key string
	switch mode {
	case ModeKeep:
		key = KeyCapKeep
	case ModeDrop:
		key = KeyCapDrop
	default:
		return "", fmt.Errorf("invalid capability mode %q (want %q or %q)", mode, ModeKeep, ModeDrop)
	}
	if len(caps) == 0 {
		return "", fmt.Errorf("%s mode needs at least one capability (use ClearCaps to restore defaults)", mode)
	}

	normalized, err := normalizeCaps(caps)
	if err != nil {
		return "", err
	}
	line := key + ": " + strings.Join(normalized, " ")

	head, tail := splitHead(content)
	lines := strings.Split(head, "\n")

	out := make([]string, 0, len(lines)+1)
	insertAt := -1
	for _, ln := range lines {
		if isCapLine(ln) {
			if insertAt < 0 {
				insertAt = len(out)
			}
			continue
		}
		out = append(out, ln)
	}
	out = insertHeadLine(out, insertAt, line)

	return strings.Join(out, "\n") + tail, nil
}

// ClearCaps removes every lxc.cap.keep/lxc.cap.drop line from the head of
// content, restoring PVE's default capability set. It reports whether anything
// was removed. All other bytes, including every [section] tail, are preserved.
func ClearCaps(content string) (out string, changed bool) {
	head, tail := splitHead(content)
	lines := strings.Split(head, "\n")

	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if isCapLine(ln) {
			changed = true
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n") + tail, changed
}

// splitHead divides content at the first line that begins a snapshot or pending
// section (a line starting with "["). head is the mutable portion the editor
// works on; tail is everything from that section header to EOF, carried opaque
// and re-emitted verbatim. When there is no section, head is all of content and
// tail is empty.
func splitHead(content string) (head, tail string) {
	for pos := 0; pos < len(content); {
		end := pos + len(content[pos:])
		if nl := strings.IndexByte(content[pos:], '\n'); nl >= 0 {
			end = pos + nl
		}
		if strings.HasPrefix(content[pos:end], "[") {
			return content[:pos], content[pos:]
		}
		if end >= len(content) {
			break
		}
		pos = end + 1
	}
	return content, ""
}

// parseConfLine splits a PVE "key: value" config line, returning the trimmed
// key, the value with only its leading separator space removed, and whether the
// line is a well-formed key/value pair. Comment and blank lines return ok=false.
func parseConfLine(line string) (key, val string, ok bool) {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:colon])
	if key == "" || strings.HasPrefix(key, "#") {
		return "", "", false
	}
	val = strings.TrimSpace(line[colon+1:])
	return key, val, true
}

// isCapLine reports whether line is an lxc.cap.keep or lxc.cap.drop entry.
func isCapLine(line string) bool {
	key, _, ok := parseConfLine(line)
	return ok && (key == KeyCapKeep || key == KeyCapDrop)
}

// accumulateDrop folds one lxc.cap.drop line's value into the running list. An
// empty value resets the list (the LXC "clear drops so far" sentinel); anything
// else appends its space-separated names, dropping duplicates.
func accumulateDrop(list []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return appendUnique(list, strings.Fields(value)...)
}

// accumulateKeep folds one lxc.cap.keep line's value into the running list. The
// token "none" resets the accumulated list (the LXC sentinel for "keep
// nothing"); other tokens append, dropping duplicates.
func accumulateKeep(list []string, value string) []string {
	for _, tok := range strings.Fields(value) {
		if tok == "none" {
			list = nil
			continue
		}
		list = appendUnique(list, tok)
	}
	return list
}

// appendUnique appends each item not already present, preserving order.
func appendUnique(list []string, items ...string) []string {
	for _, it := range items {
		found := false
		for _, existing := range list {
			if existing == it {
				found = true
				break
			}
		}
		if !found {
			list = append(list, it)
		}
	}
	return list
}

// normalizeCaps canonicalizes and de-duplicates a caller-supplied cap list,
// preserving first-seen order and failing on the first unknown name.
func normalizeCaps(caps []string) ([]string, error) {
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		n, err := Normalize(c)
		if err != nil {
			return nil, err
		}
		out = appendUnique(out, n)
	}
	return out, nil
}

// insertHeadLine places line into the head-line slice at index at (where the
// first replaced capability line was), or appends it to the end of the head
// when at is negative. On append it slots the line before a trailing empty
// element so the head keeps its final newline (and does not fuse with a
// following section header).
func insertHeadLine(lines []string, at int, line string) []string {
	if at >= 0 {
		lines = append(lines, "")
		copy(lines[at+1:], lines[at:])
		lines[at] = line
		return lines
	}
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = append(lines, "")
		copy(lines[n:], lines[n-1:])
		lines[n-1] = line
		return lines
	}
	return append(lines, line)
}
