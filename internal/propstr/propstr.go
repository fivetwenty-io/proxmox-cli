// Package propstr is an order-preserving, unknown-key-preserving codec for
// PVE property strings: comma-separated "key=value" lists that sometimes
// carry a bare default-key value in their head segment (agent's "1" in
// "1,fstrim_cloned_disks=1", cpu's "host" in "host,flags=+aes;-pcid"). It is
// pure (stdlib only) and safe for read-modify-write edits: every sub-key it
// does not know about — file=, size=, format=, macaddr, mtu, and any future
// PVE addition — round-trips through Parse and String verbatim.
//
// Splitting happens on "," only. Semicolon-separated sub-values inside a
// single pair's value (flags=+aes;-pcid, trunks=1;2) are never touched.
package propstr

import "strings"

// Pair is one element of a parsed property string: either a "key=value"
// segment or a bare default-key value (Bare true), such as the "1" in
// agent=1 or the "host" in cpu=host,flags=+aes.
type Pair struct {
	Key   string
	Value string
	Bare  bool
}

// List is a parsed property string: an ordered sequence of Pair, preserving
// both the original order and each pair's bare/keyed form.
type List []Pair

// Parse splits s on "," into a List. Empty segments (from a leading,
// trailing, or doubled comma) are skipped.
//
// A segment without "=" is bare. Only the first segment produced (the head
// of the property string) receives defaultKey as its Key when bare; PVE only
// ever uses default-key shorthand at the head of a property string, so any
// later bare segment is malformed input and is preserved as a Bare pair with
// Key == "" — String still renders it verbatim, it is just not addressable
// by Get/Set under any key. When defaultKey is "", the head bare segment
// keeps Key == "" too (still Bare), matching properties such as net[n] whose
// head segment, <model>=<macaddr>, is a real keyed pair rather than
// shorthand.
//
// Parse("", defaultKey) returns an empty (nil) List.
func Parse(s, defaultKey string) List {
	if s == "" {
		return nil
	}

	var list List
	for seg := range strings.SplitSeq(s, ",") {
		if seg == "" {
			continue
		}
		if k, v, ok := strings.Cut(seg, "="); ok {
			list = append(list, Pair{Key: k, Value: v})
			continue
		}
		key := ""
		if len(list) == 0 {
			key = defaultKey
		}
		list = append(list, Pair{Key: key, Value: seg, Bare: true})
	}
	return list
}

// String re-renders l as a property string, preserving order: a Bare pair
// renders as its Value alone, everything else renders as "Key=Value".
// Parse(s, k).String() == s for any well-formed s — no whitespace
// normalization, no reordering, no default-key stripping.
func (l List) String() string {
	parts := make([]string, len(l))
	for i, p := range l {
		if p.Bare {
			parts[i] = p.Value
		} else {
			parts[i] = p.Key + "=" + p.Value
		}
	}
	return strings.Join(parts, ",")
}

// Get returns the value of the first pair whose Key matches key, and whether
// one was found. Bare pairs are matched like any other: a head bare pair
// carries the property's default key (see Parse), so Get(defaultKey) reaches
// it the same way Get reaches a keyed pair.
func (l List) Get(key string) (string, bool) {
	for _, p := range l {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// Set assigns value to the first pair whose Key matches key, in place: its
// position and Bare flag are left untouched, only Value changes. So editing
// a bare pair (agent's "1", key "enabled") keeps rendering bare after the
// edit ("0"), not "enabled=0". If no pair has that key, Set appends a new
// non-bare {Key: key, Value: value} pair at the end.
func (l *List) Set(key, value string) {
	for i, p := range *l {
		if p.Key == key {
			(*l)[i].Value = value
			return
		}
	}
	*l = append(*l, Pair{Key: key, Value: value})
}

// Delete removes every pair whose Key matches key, preserving the order of
// what remains.
func (l *List) Delete(key string) {
	out := make(List, 0, len(*l))
	for _, p := range *l {
		if p.Key != key {
			out = append(out, p)
		}
	}
	*l = out
}
