package main

import (
	"regexp"
	"strings"
)

// subKeyNamePattern matches a sub-key name as the apidoc spells them: a
// letter followed by letters, digits, underscores, or hyphens.
var subKeyNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// enumWordPattern matches one alternative of an enumerated value placeholder:
// letters, digits, and the punctuation that appears in device paths and
// option names.
var enumWordPattern = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// numericRangePattern matches an alternative such as 8-64, which describes a
// numeric span instead of a literal value.
var numericRangePattern = regexp.MustCompile(`^\d+-\d+$`)

// typetextSubKeys derives sub-key properties from an apidoc typetext string
// such as "latitude=<number> ,longitude=<number> [,name=<name>]". Options whose
// schema names a registered format instead of spelling the sub-keys out carry
// only this human-oriented summary. A component wrapped in square brackets is
// optional and an unwrapped one is required. A component that cannot be
// parsed is skipped, and the result is nil when no component parses.
func typetextSubKeys(typetext string) map[string]property {
	var out map[string]property
	for _, segment := range splitTypetext(typetext) {
		name, p, ok := parseTypetextComponent(segment)
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string]property)
		}
		out[name] = p
	}
	return out
}

// splitTypetext breaks a typetext into its top-level segments. Commas and
// whitespace separate segments only outside square brackets and angle
// brackets, so an optional group such as "[,name=<name>]" and a placeholder
// such as "<storage ID>" each stay in one piece.
func splitTypetext(typetext string) []string {
	var segments []string
	square, angle, start := 0, 0, -1
	flush := func(end int) {
		if start >= 0 {
			segments = append(segments, typetext[start:end])
			start = -1
		}
	}
	for i := 0; i < len(typetext); i++ {
		c := typetext[i]
		if square == 0 && angle == 0 && (c == ',' || c == ' ' || c == '\t' || c == '\n') {
			flush(i)
			continue
		}
		if start < 0 {
			start = i
		}
		switch c {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '[':
			if angle == 0 {
				square++
			}
		case ']':
			if angle == 0 && square > 0 {
				square--
			}
		}
	}
	flush(len(typetext))
	return segments
}

// parseTypetextComponent converts one top-level segment into a sub-key. It
// reports false when the segment has no recognizable "key=value" shape.
func parseTypetextComponent(segment string) (string, property, bool) {
	required := true
	if closeIdx := matchingSquare(segment, 0); closeIdx == len(segment)-1 && closeIdx > 0 {
		required = false
		segment = strings.TrimLeft(segment[1:closeIdx], ", \t")
	}
	key, value, ok := splitKeyValue(segment)
	if !ok || !subKeyNamePattern.MatchString(key) {
		return "", property{}, false
	}
	var p property
	p.Type, p.Enum = placeholderType(value)
	if !required {
		p.Optional = optionalTrue
	}
	return key, p, true
}

// splitKeyValue separates a component into its key and value placeholder. It
// understands both the plain "key=value" form and the default-key form
// "[key=]value", where the key may be omitted when writing the option.
func splitKeyValue(component string) (string, string, bool) {
	if strings.HasPrefix(component, "[") {
		closeIdx := matchingSquare(component, 0)
		if closeIdx < 0 {
			return "", "", false
		}
		inner := component[1:closeIdx]
		if !strings.HasSuffix(inner, "=") {
			return "", "", false
		}
		return strings.TrimSuffix(inner, "="), component[closeIdx+1:], true
	}
	key, value, ok := strings.Cut(component, "=")
	return key, value, ok
}

// matchingSquare returns the index of the "]" closing the "[" at index open,
// or -1 when the bracket is unbalanced. Brackets inside angle-bracketed
// placeholders do not count.
func matchingSquare(s string, open int) int {
	if open >= len(s) || s[open] != '[' {
		return -1
	}
	square, angle := 0, 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '[':
			if angle == 0 {
				square++
			}
		case ']':
			if angle == 0 {
				square--
				if square == 0 {
					return i
				}
			}
		}
	}
	return -1
}

// placeholderType maps a value placeholder to a sub-key type and, for a list
// of literal alternatives, its allowed values in the order written.
func placeholderType(value string) (string, []string) {
	value = strings.TrimSpace(value)
	if value == `\d+` {
		return "integer", nil
	}
	if len(value) < 2 || value[0] != '<' || value[len(value)-1] != '>' {
		return "string", nil
	}
	inner := value[1 : len(value)-1]
	switch inner {
	case "1|0":
		return "boolean", nil
	case "number":
		return "number", nil
	case "integer", "N":
		// PVE writes a count, such as a prune-backups keep-* value, as <N>.
		return "integer", nil
	}
	alternatives := strings.Split(inner, "|")
	if len(alternatives) < 2 {
		return "string", nil
	}
	for _, alt := range alternatives {
		if !enumWordPattern.MatchString(alt) || numericRangePattern.MatchString(alt) {
			return "string", nil
		}
	}
	return "string", alternatives
}
