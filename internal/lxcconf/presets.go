package lxcconf

// Preset names for the named capability whitelists the security commands expose
// via `caps set --preset`.
const (
	PresetMinimal = "minimal"
	PresetSystemd = "systemd"
	PresetNetwork = "network"
)

// presetOrder lists the presets from least to most capable, so callers that
// render them (describe, help text) present a stable, sensible order.
var presetOrder = []string{PresetMinimal, PresetSystemd, PresetNetwork}

// presetMinimal is the smallest keep-mode whitelist that still lets a bare init
// or a single long-running service start and run as a normal daemon.
var presetMinimal = []string{"chown", "dac_override", "fowner", "setuid", "setgid", "kill"}

// presetSystemd adds setpcap so a systemd PID 1 can drop capabilities from the
// services it spawns.
var presetSystemd = append(cloneCaps(presetMinimal), "setpcap")

// presetNetwork adds the capabilities a container needs to bind privileged
// ports and open raw sockets.
var presetNetwork = append(cloneCaps(presetSystemd), "net_bind_service", "net_raw")

// Presets returns the named capability whitelists keyed by preset name. Each
// value is a fresh slice, so callers may mutate it without disturbing the
// catalog.
func Presets() map[string][]string {
	return map[string][]string{
		PresetMinimal: cloneCaps(presetMinimal),
		PresetSystemd: cloneCaps(presetSystemd),
		PresetNetwork: cloneCaps(presetNetwork),
	}
}

// PresetNames returns the preset names in order from least to most capable.
func PresetNames() []string {
	return cloneCaps(presetOrder)
}

// Preset returns the capability list for a named preset and whether it exists.
// The returned slice is a copy.
func Preset(name string) ([]string, bool) {
	caps, ok := Presets()[name]
	return caps, ok
}

// cloneCaps returns a fresh copy of caps so exported accessors never hand out an
// aliased slice backed by a package-level variable.
func cloneCaps(caps []string) []string {
	out := make([]string, len(caps))
	copy(out, caps)
	return out
}
