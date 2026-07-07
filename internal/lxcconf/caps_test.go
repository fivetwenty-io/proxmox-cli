package lxcconf

import (
	"reflect"
	"strings"
	"testing"
)

func TestCatalogOrderAndSize(t *testing.T) {
	cat := Catalog()
	if len(cat) != 41 {
		t.Fatalf("catalog size = %d, want 41", len(cat))
	}
	for i, c := range cat {
		if c.Bit != i {
			t.Errorf("catalog[%d] has Bit=%d, want %d (%s)", i, c.Bit, i, c.Name)
		}
		if c.Note == "" {
			t.Errorf("catalog[%d] %s has empty note", i, c.Name)
		}
	}
	if cat[0].Name != "chown" {
		t.Errorf("bit 0 = %q, want chown", cat[0].Name)
	}
	if cat[40].Name != "checkpoint_restore" {
		t.Errorf("bit 40 = %q, want checkpoint_restore", cat[40].Name)
	}
	// Spot-check a few load-bearing bit positions used by DecodeMask.
	for _, tc := range []struct {
		bit  int
		name string
	}{{7, "setuid"}, {12, "net_admin"}, {21, "sys_admin"}, {25, "sys_time"}, {27, "mknod"}} {
		if cat[tc.bit].Name != tc.name {
			t.Errorf("bit %d = %q, want %q", tc.bit, cat[tc.bit].Name, tc.name)
		}
	}
}

func TestCatalogReturnsCopy(t *testing.T) {
	Catalog()[0].Name = "mutated"
	if Catalog()[0].Name != "chown" {
		t.Fatalf("Catalog() handed out an aliased slice")
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
		suggest string // substring expected in the error, when wantErr
	}{
		{in: "CAP_NET_ADMIN", want: "net_admin"},
		{in: "NET_ADMIN", want: "net_admin"},
		{in: "net_admin", want: "net_admin"},
		{in: "  sys_admin  ", want: "sys_admin"},
		{in: "Cap_Sys_Time", want: "sys_time"},
		{in: "net_admn", wantErr: true, suggest: "net_admin"},
		{in: "sys_admni", wantErr: true, suggest: "sys_admin"},
		{in: "zzzzzzzzzzzzzz", wantErr: true},
	}
	for _, tc := range tests {
		got, err := Normalize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Normalize(%q) = %q, want error", tc.in, got)
				continue
			}
			if tc.suggest != "" && !strings.Contains(err.Error(), tc.suggest) {
				t.Errorf("Normalize(%q) error %q, want it to suggest %q", tc.in, err, tc.suggest)
			}
			continue
		}
		if err != nil {
			t.Errorf("Normalize(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsDangerous(t *testing.T) {
	for _, name := range []string{"sys_admin", "CAP_SYS_MODULE", "sys_rawio", "sys_boot", "sys_time"} {
		if !IsDangerous(name) {
			t.Errorf("IsDangerous(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"chown", "net_admin", "setuid", "totally_unknown"} {
		if IsDangerous(name) {
			t.Errorf("IsDangerous(%q) = true, want false", name)
		}
	}
}

func TestDecodeMask(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "full 41-bit mask", in: "000001ffffffffff", want: allCapNames()},
		{name: "single bit chown", in: "0000000000000001", want: []string{"chown"}},
		{name: "sparse sys_admin", in: "0000000000200000", want: []string{"sys_admin"}},
		{name: "multiple bits", in: "0000000000200080", want: []string{"setuid", "sys_admin"}},
		{name: "0x prefix", in: "0x1", want: []string{"chown"}},
		{name: "bit beyond catalog", in: "20000000000", want: []string{"cap_41"}},
		{name: "zero mask", in: "0000000000000000", want: nil},
		{name: "empty", in: "", wantErr: true},
		{name: "not hex", in: "xyz", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeMask(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DecodeMask(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeMask(%q) unexpected error: %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DecodeMask(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func allCapNames() []string {
	out := make([]string, 0, len(Catalog()))
	for _, c := range Catalog() {
		out = append(out, c.Name)
	}
	return out
}
