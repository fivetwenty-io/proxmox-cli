package lxcconf

import (
	"reflect"
	"strings"
	"testing"
)

// confWithSnapshots is a realistic guest config: a mutable head followed by two
// snapshot sections, each of which embeds its own lxc.cap.* lines. The editor
// must never touch anything from the first "[" onward.
const confWithSnapshots = "arch: amd64\n" +
	"hostname: ct101\n" +
	"lxc.cap.drop: sys_module\n" +
	"memory: 512\n" +
	"\n" +
	"[pre-hardening]\n" +
	"arch: amd64\n" +
	"lxc.cap.drop: sys_admin sys_module\n" +
	"snaptime: 1700000000\n" +
	"\n" +
	"[pve:pending]\n" +
	"lxc.cap.keep: chown kill\n"

func snapshotTail(t *testing.T) string {
	t.Helper()
	idx := strings.Index(confWithSnapshots, "[pre-hardening]")
	if idx < 0 {
		t.Fatal("fixture missing section header")
	}
	return confWithSnapshots[idx:]
}

func TestGetCaps(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantMode string
		wantKeep []string
		wantDrop []string
		wantErr  bool
	}{
		{
			name:     "default when no cap lines",
			content:  "arch: amd64\nhostname: ct\n",
			wantMode: ModeDefault,
		},
		{
			name:     "single drop line",
			content:  "lxc.cap.drop: sys_module mac_admin\n",
			wantMode: ModeDrop,
			wantDrop: []string{"sys_module", "mac_admin"},
		},
		{
			name:     "drop accumulates across lines",
			content:  "lxc.cap.drop: sys_module mac_admin\nlxc.cap.drop: sys_time\n",
			wantMode: ModeDrop,
			wantDrop: []string{"sys_module", "mac_admin", "sys_time"},
		},
		{
			name:     "empty drop value resets",
			content:  "lxc.cap.drop: sys_module\nlxc.cap.drop:\nlxc.cap.drop: sys_time\n",
			wantMode: ModeDrop,
			wantDrop: []string{"sys_time"},
		},
		{
			name:     "keep none token resets",
			content:  "lxc.cap.keep: chown kill\nlxc.cap.keep: none setuid\n",
			wantMode: ModeKeep,
			wantKeep: []string{"setuid"},
		},
		{
			name:     "drop dedups",
			content:  "lxc.cap.drop: sys_module\nlxc.cap.drop: sys_module sys_time\n",
			wantMode: ModeDrop,
			wantDrop: []string{"sys_module", "sys_time"},
		},
		{
			name:    "keep and drop coexist is an error",
			content: "lxc.cap.keep: chown\nlxc.cap.drop: sys_module\n",
			wantErr: true,
		},
		{
			name:     "only head is parsed, snapshot cap lines ignored",
			content:  confWithSnapshots,
			wantMode: ModeDrop,
			wantDrop: []string{"sys_module"},
		},
		{
			name:     "comment resembling a cap line is ignored",
			content:  "# lxc.cap.drop: sys_admin\nlxc.cap.drop: sys_module\n",
			wantMode: ModeDrop,
			wantDrop: []string{"sys_module"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GetCaps(tc.content)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("GetCaps() = %+v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetCaps() unexpected error: %v", err)
			}
			if got.Mode != tc.wantMode {
				t.Errorf("Mode = %q, want %q", got.Mode, tc.wantMode)
			}
			if !reflect.DeepEqual(got.Keep, tc.wantKeep) {
				t.Errorf("Keep = %v, want %v", got.Keep, tc.wantKeep)
			}
			if !reflect.DeepEqual(got.Drop, tc.wantDrop) {
				t.Errorf("Drop = %v, want %v", got.Drop, tc.wantDrop)
			}
		})
	}
}

func TestSetCaps(t *testing.T) {
	tests := []struct {
		name    string
		content string
		mode    string
		caps    []string
		want    string
		wantErr bool
	}{
		{
			name:    "replace drop with keep at first cap position",
			content: "arch: amd64\nlxc.cap.drop: sys_module\nmemory: 512\n",
			mode:    ModeKeep,
			caps:    []string{"chown", "kill"},
			want:    "arch: amd64\nlxc.cap.keep: chown kill\nmemory: 512\n",
		},
		{
			name:    "collapse multiple drop lines into one",
			content: "arch: amd64\nlxc.cap.drop: sys_module\nmemory: 512\nlxc.cap.drop: sys_time\n",
			mode:    ModeDrop,
			caps:    []string{"sys_module", "sys_time"},
			want:    "arch: amd64\nlxc.cap.drop: sys_module sys_time\nmemory: 512\n",
		},
		{
			name:    "append when no cap line exists",
			content: "arch: amd64\nmemory: 512\n",
			mode:    ModeDrop,
			caps:    []string{"sys_module"},
			want:    "arch: amd64\nmemory: 512\nlxc.cap.drop: sys_module\n",
		},
		{
			name:    "normalize and dedup input",
			content: "arch: amd64\n",
			mode:    ModeKeep,
			caps:    []string{"CAP_CHOWN", "net_admin", "chown"},
			want:    "arch: amd64\nlxc.cap.keep: chown net_admin\n",
		},
		{
			name:    "empty file gains the line",
			content: "",
			mode:    ModeKeep,
			caps:    []string{"chown"},
			want:    "lxc.cap.keep: chown\n",
		},
		{
			name:    "no trailing newline is preserved",
			content: "arch: amd64\nlxc.cap.drop: sys_module",
			mode:    ModeKeep,
			caps:    []string{"chown"},
			want:    "arch: amd64\nlxc.cap.keep: chown",
		},
		{name: "invalid mode errors", content: "arch: amd64\n", mode: "block", caps: []string{"chown"}, wantErr: true},
		{name: "empty caps errors", content: "arch: amd64\n", mode: ModeKeep, caps: nil, wantErr: true},
		{name: "unknown cap errors", content: "arch: amd64\n", mode: ModeKeep, caps: []string{"bogus_cap"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SetCaps(tc.content, tc.mode, tc.caps)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SetCaps() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SetCaps() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("SetCaps()\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestSetCapsPreservesSnapshotTail is the byte-identity guarantee: editing the
// head of a config with snapshot sections must leave every byte from the first
// section header onward untouched, and must replace the head's cap line in
// place regardless of the cap lines living inside the snapshots.
func TestSetCapsPreservesSnapshotTail(t *testing.T) {
	tail := snapshotTail(t)

	got, err := SetCaps(confWithSnapshots, ModeKeep, []string{"chown", "kill"})
	if err != nil {
		t.Fatalf("SetCaps() error: %v", err)
	}
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("snapshot tail was modified.\n got %q\nwant suffix %q", got, tail)
	}
	wantHead := "arch: amd64\nhostname: ct101\nlxc.cap.keep: chown kill\nmemory: 512\n\n"
	if got != wantHead+tail {
		t.Errorf("SetCaps()\n got %q\nwant %q", got, wantHead+tail)
	}
}

func TestClearCaps(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		want        string
		wantChanged bool
	}{
		{
			name:        "removes single drop line",
			content:     "arch: amd64\nlxc.cap.drop: sys_module\nmemory: 512\n",
			want:        "arch: amd64\nmemory: 512\n",
			wantChanged: true,
		},
		{
			name:        "removes multiple cap lines of both modes",
			content:     "lxc.cap.drop: sys_module\narch: amd64\nlxc.cap.keep: chown\n",
			want:        "arch: amd64\n",
			wantChanged: true,
		},
		{
			name:        "no cap lines is a no-op",
			content:     "arch: amd64\nmemory: 512\n",
			want:        "arch: amd64\nmemory: 512\n",
			wantChanged: false,
		},
		{
			name:        "empty content is a no-op",
			content:     "",
			want:        "",
			wantChanged: false,
		},
		{
			name:        "no trailing newline",
			content:     "arch: amd64\nlxc.cap.drop: sys_module",
			want:        "arch: amd64",
			wantChanged: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := ClearCaps(tc.content)
			if got != tc.want {
				t.Errorf("ClearCaps()\n got %q\nwant %q", got, tc.want)
			}
			if changed != tc.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}
}

func TestClearCapsPreservesSnapshotTail(t *testing.T) {
	tail := snapshotTail(t)
	got, changed := ClearCaps(confWithSnapshots)
	if !changed {
		t.Fatal("ClearCaps() reported no change, want changed")
	}
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("snapshot tail was modified.\n got %q\nwant suffix %q", got, tail)
	}
	wantHead := "arch: amd64\nhostname: ct101\nmemory: 512\n\n"
	if got != wantHead+tail {
		t.Errorf("ClearCaps()\n got %q\nwant %q", got, wantHead+tail)
	}
}

// TestSetCapsRemovesOppositeMode confirms switching modes strips the other
// mode's lines so the result never leaves keep and drop coexisting.
func TestSetCapsRemovesOppositeMode(t *testing.T) {
	content := "arch: amd64\nlxc.cap.keep: chown kill\nmemory: 512\n"
	got, err := SetCaps(content, ModeDrop, []string{"sys_module"})
	if err != nil {
		t.Fatalf("SetCaps() error: %v", err)
	}
	want := "arch: amd64\nlxc.cap.drop: sys_module\nmemory: 512\n"
	if got != want {
		t.Errorf("SetCaps()\n got %q\nwant %q", got, want)
	}
	// The result must parse cleanly (no coexistence error).
	state, err := GetCaps(got)
	if err != nil {
		t.Fatalf("GetCaps(result) error: %v", err)
	}
	if state.Mode != ModeDrop {
		t.Errorf("result mode = %q, want drop", state.Mode)
	}
}
