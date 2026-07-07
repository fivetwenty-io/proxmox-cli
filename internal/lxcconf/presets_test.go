package lxcconf

import (
	"reflect"
	"testing"
)

func TestPresetComposition(t *testing.T) {
	presets := Presets()

	wantMinimal := []string{"chown", "dac_override", "fowner", "setuid", "setgid", "kill"}
	wantSystemd := append(append([]string{}, wantMinimal...), "setpcap")
	wantNetwork := append(append([]string{}, wantSystemd...), "net_bind_service", "net_raw")

	for _, tc := range []struct {
		name string
		want []string
	}{
		{PresetMinimal, wantMinimal},
		{PresetSystemd, wantSystemd},
		{PresetNetwork, wantNetwork},
	} {
		if !reflect.DeepEqual(presets[tc.name], tc.want) {
			t.Errorf("Presets()[%q] = %v, want %v", tc.name, presets[tc.name], tc.want)
		}
	}
}

func TestPresetNamesOrdered(t *testing.T) {
	want := []string{"minimal", "systemd", "network"}
	if got := PresetNames(); !reflect.DeepEqual(got, want) {
		t.Errorf("PresetNames() = %v, want %v", got, want)
	}
}

func TestPresetAccessor(t *testing.T) {
	caps, ok := Preset(PresetSystemd)
	if !ok {
		t.Fatal("Preset(systemd) not found")
	}
	if !reflect.DeepEqual(caps, []string{"chown", "dac_override", "fowner", "setuid", "setgid", "kill", "setpcap"}) {
		t.Errorf("Preset(systemd) = %v", caps)
	}
	if _, ok := Preset("does-not-exist"); ok {
		t.Error("Preset(unknown) reported ok=true")
	}
}

func TestPresetsAreCopies(t *testing.T) {
	Presets()[PresetMinimal][0] = "mutated"
	if Presets()[PresetMinimal][0] != "chown" {
		t.Fatal("Presets() handed out an aliased slice")
	}
	caps, _ := Preset(PresetNetwork)
	caps[0] = "mutated"
	if fresh, _ := Preset(PresetNetwork); fresh[0] != "chown" {
		t.Fatal("Preset() handed out an aliased slice")
	}
	names := PresetNames()
	names[0] = "mutated"
	if PresetNames()[0] != "minimal" {
		t.Fatal("PresetNames() handed out an aliased slice")
	}
}

// TestPresetCapsAreValid guards against a preset referencing a name the catalog
// does not know (which would make `caps set --preset` fail at normalize time).
func TestPresetCapsAreValid(t *testing.T) {
	for name, caps := range Presets() {
		for _, c := range caps {
			if _, err := Normalize(c); err != nil {
				t.Errorf("preset %q lists invalid capability %q: %v", name, c, err)
			}
		}
	}
}
