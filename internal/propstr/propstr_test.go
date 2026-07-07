package propstr_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/propstr"
)

// ── Parse / String round-trip ────────────────────────────────────────────────

func TestParse_Empty(t *testing.T) {
	got := propstr.Parse("", "enabled")
	require.Empty(t, got)
	require.Equal(t, "", got.String())
}

func TestParse_RoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		s          string
		defaultKey string
	}{
		{
			name:       "unknown keys and order preserved",
			s:          "virtio=AA:BB:CC,bridge=vmbr0,firewall=1",
			defaultKey: "",
		},
		{
			name:       "bare default-key head, agent shape",
			s:          "1,fstrim_cloned_disks=1",
			defaultKey: "enabled",
		},
		{
			name:       "bare default-key head with semicolon sub-value untouched",
			s:          "host,flags=+aes;-pcid",
			defaultKey: "cputype",
		},
		{
			name:       "bare default-key head alone",
			s:          "0",
			defaultKey: "enabled",
		},
		{
			name:       "semicolon list value round-trips untouched",
			s:          "sata0=local:vm-100-disk-0,trunks=1;2;3,size=32G",
			defaultKey: "",
		},
		{
			name:       "malformed trailing bare token preserved verbatim",
			s:          "cputype=host,foo",
			defaultKey: "cputype",
		},
		{
			name:       "efidisk0 shape, unknown sub-keys preserved",
			s:          "local-lvm:1,efitype=4m,pre-enrolled-keys=1,size=528K",
			defaultKey: "file",
		},
		{
			name:       "single keyed pair",
			s:          "type=std",
			defaultKey: "type",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := propstr.Parse(tc.s, tc.defaultKey)
			require.Equal(t, tc.s, got.String())
		})
	}
}

func TestParse_SkipsEmptySegments(t *testing.T) {
	got := propstr.Parse(",bridge=vmbr0,,firewall=1,", "")
	require.Equal(t, propstr.List{
		{Key: "bridge", Value: "vmbr0"},
		{Key: "firewall", Value: "1"},
	}, got)
}

// ── Parse: bare-segment semantics ────────────────────────────────────────────

func TestParse_BareHeadGetsDefaultKey(t *testing.T) {
	got := propstr.Parse("1,fstrim_cloned_disks=1", "enabled")
	require.Equal(t, propstr.List{
		{Key: "enabled", Value: "1", Bare: true},
		{Key: "fstrim_cloned_disks", Value: "1"},
	}, got)
}

func TestParse_BareHeadWithEmptyDefaultKeyKeepsEmptyKey(t *testing.T) {
	// net[n]-style properties pass defaultKey == "" because their head
	// segment (<model>=<macaddr>) is always a real keyed pair; but if a
	// caller does hand Parse a bare head against defaultKey "", the pair
	// still parses (Bare true, Key "").
	got := propstr.Parse("host,cputype=x86-64-v2-AES", "")
	require.Equal(t, propstr.List{
		{Key: "", Value: "host", Bare: true},
		{Key: "cputype", Value: "x86-64-v2-AES"},
	}, got)
}

func TestParse_NonLeadingBareSegmentDoesNotGetDefaultKey(t *testing.T) {
	// Only the head of a property string ever carries default-key shorthand.
	// A later '='-less token is malformed input; Parse still preserves it
	// (Key == "", Bare true) so String round-trips it verbatim, but it is not
	// reachable via Get(defaultKey).
	got := propstr.Parse("cputype=host,foo", "cputype")
	require.Equal(t, propstr.List{
		{Key: "cputype", Value: "host"},
		{Key: "", Value: "foo", Bare: true},
	}, got)

	_, ok := got.Get("cputype")
	require.True(t, ok, "keyed head pair must still be reachable")

	_, ok = got.Get("foo")
	require.False(t, ok, "malformed trailing bare token is not addressable by its text")
}

func TestParse_NoDefaultKeyModeAllKeyed(t *testing.T) {
	// net-style property: every segment is already keyed, so defaultKey ""
	// never gets used.
	got := propstr.Parse("virtio=AA:BB:CC,bridge=vmbr0,firewall=1", "")
	require.Equal(t, propstr.List{
		{Key: "virtio", Value: "AA:BB:CC"},
		{Key: "bridge", Value: "vmbr0"},
		{Key: "firewall", Value: "1"},
	}, got)
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestGet(t *testing.T) {
	l := propstr.Parse("1,fstrim_cloned_disks=1,type=virtio", "enabled")

	v, ok := l.Get("enabled")
	require.True(t, ok)
	require.Equal(t, "1", v)

	v, ok = l.Get("type")
	require.True(t, ok)
	require.Equal(t, "virtio", v)

	_, ok = l.Get("missing")
	require.False(t, ok)
}

func TestGet_FirstMatchWins(t *testing.T) {
	l := propstr.List{
		{Key: "tag", Value: "first"},
		{Key: "tag", Value: "second"},
	}
	v, ok := l.Get("tag")
	require.True(t, ok)
	require.Equal(t, "first", v)
}

func TestGet_EmptyList(t *testing.T) {
	var l propstr.List
	_, ok := l.Get("anything")
	require.False(t, ok)
}

// ── Set ───────────────────────────────────────────────────────────────────────

func TestSet_ReplaceInPlaceKeepsPositionAndBare(t *testing.T) {
	l := propstr.Parse("host,flags=+aes;-pcid,hidden=1", "cputype")

	l.Set("flags", "+aes;-pcid;+ibpb")

	require.Equal(t, propstr.List{
		{Key: "cputype", Value: "host", Bare: true},
		{Key: "flags", Value: "+aes;-pcid;+ibpb"},
		{Key: "hidden", Value: "1"},
	}, l)
	require.Equal(t, "host,flags=+aes;-pcid;+ibpb,hidden=1", l.String())
}

func TestSet_OnBarePairRerendersBare(t *testing.T) {
	l := propstr.Parse("1", "enabled")
	require.Equal(t, "1", l.String())

	l.Set("enabled", "0")

	require.Equal(t, propstr.List{{Key: "enabled", Value: "0", Bare: true}}, l)
	require.Equal(t, "0", l.String())
}

func TestSet_AppendsWhenKeyAbsent(t *testing.T) {
	l := propstr.Parse("bridge=vmbr0", "")

	l.Set("firewall", "1")

	require.Equal(t, propstr.List{
		{Key: "bridge", Value: "vmbr0"},
		{Key: "firewall", Value: "1"},
	}, l)
	require.Equal(t, "bridge=vmbr0,firewall=1", l.String())
	require.False(t, l[1].Bare, "appended pair must not be bare")
}

func TestSet_OnEmptyList(t *testing.T) {
	var l propstr.List
	l.Set("type", "virtio")
	require.Equal(t, propstr.List{{Key: "type", Value: "virtio"}}, l)
	require.Equal(t, "type=virtio", l.String())
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestDelete(t *testing.T) {
	l := propstr.Parse("virtio=AA:BB:CC,bridge=vmbr0,firewall=1", "")

	l.Delete("firewall")

	require.Equal(t, propstr.List{
		{Key: "virtio", Value: "AA:BB:CC"},
		{Key: "bridge", Value: "vmbr0"},
	}, l)
	require.Equal(t, "virtio=AA:BB:CC,bridge=vmbr0", l.String())
}

func TestDelete_RemovesAllMatches(t *testing.T) {
	l := propstr.List{
		{Key: "trunk", Value: "1"},
		{Key: "keep", Value: "yes"},
		{Key: "trunk", Value: "2"},
	}
	l.Delete("trunk")
	require.Equal(t, propstr.List{{Key: "keep", Value: "yes"}}, l)
}

func TestDelete_MissingKeyIsNoOp(t *testing.T) {
	l := propstr.Parse("bridge=vmbr0", "")
	l.Delete("nope")
	require.Equal(t, propstr.List{{Key: "bridge", Value: "vmbr0"}}, l)
}

func TestDelete_OnEmptyList(t *testing.T) {
	var l propstr.List
	l.Delete("anything")
	require.Empty(t, l)
}
