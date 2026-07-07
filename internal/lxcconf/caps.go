// Package lxcconf edits the guest-config text format of
// /etc/pve/lxc/<vmid>.conf and carries the Linux capability catalog the LXC
// security commands validate against. It is pure (stdlib only): no ssh, no
// cobra, no cli.Deps, so anything in the tree may import it and it is trivially
// table-testable.
package lxcconf

import (
	"fmt"
	"math/big"
	"strings"
)

// Capability describes one Linux capability in the LXC config spelling used by
// lxc.cap.keep/lxc.cap.drop: lowercase, without the CAP_ prefix.
type Capability struct {
	Name      string // canonical LXC spelling, e.g. "sys_admin"
	Bit       int    // kernel capability bit index (CAP_* value)
	Note      string // one-line description of what the capability grants
	Dangerous bool   // whether granting it materially weakens container isolation
}

// dangerousCaps is the set the security commands refuse to grant without
// --force: each one lets a container reach across its isolation boundary.
var dangerousCaps = map[string]struct{}{
	"sys_admin":  {},
	"sys_module": {},
	"sys_rawio":  {},
	"sys_boot":   {},
	"sys_time":   {},
}

// catalog lists every Linux capability in kernel bit order, from CAP_CHOWN (0)
// through CAP_CHECKPOINT_RESTORE (40, CAP_LAST_CAP on 6.x kernels). The slice
// index equals the kernel bit index, which is what DecodeMask relies on to turn
// a CapBnd/CapEff hex bitmask into names; do not reorder.
var catalog = []Capability{
	{"chown", 0, "change file ownership and group", false},
	{"dac_override", 1, "bypass file read, write, and execute permission checks", false},
	{"dac_read_search", 2, "bypass file read and directory search permission checks", false},
	{"fowner", 3, "bypass permission checks on operations that normally require file ownership", false},
	{"fsetid", 4, "keep set-user-ID and set-group-ID bits when a file is modified", false},
	{"kill", 5, "send signals to arbitrary processes", false},
	{"setgid", 6, "make arbitrary manipulations of process and file GIDs", false},
	{"setuid", 7, "make arbitrary manipulations of process and file UIDs", false},
	{"setpcap", 8, "grant or remove capabilities from the permitted set of other processes", false},
	{"linux_immutable", 9, "set the immutable and append-only inode flags", false},
	{"net_bind_service", 10, "bind a socket to Internet ports below 1024", false},
	{"net_broadcast", 11, "make socket broadcasts and listen to multicasts (unused by the kernel)", false},
	{"net_admin", 12, "perform network administration: interfaces, routing, and firewall rules", false},
	{"net_raw", 13, "use RAW and PACKET sockets and bind to any address", false},
	{"ipc_lock", 14, "lock memory (mlock, mlockall, shmctl SHM_LOCK)", false},
	{"ipc_owner", 15, "bypass permission checks for System V IPC operations", false},
	{"sys_module", 16, "load and unload kernel modules", true},
	{"sys_rawio", 17, "perform raw I/O port and memory access", true},
	{"sys_chroot", 18, "use chroot()", false},
	{"sys_ptrace", 19, "trace arbitrary processes with ptrace()", false},
	{"sys_pacct", 20, "use acct() to enable or disable process accounting", false},
	{"sys_admin", 21, "broad system administration: mount, set hostname, and many other syscalls", true},
	{"sys_boot", 22, "reboot the system and load a new kernel for later execution", true},
	{"sys_nice", 23, "raise process priority and set CPU and I/O scheduling", false},
	{"sys_resource", 24, "override resource limits, quotas, and reserved space", false},
	{"sys_time", 25, "set the system clock and the hardware real-time clock", true},
	{"sys_tty_config", 26, "configure tty devices and perform vhangup()", false},
	{"mknod", 27, "create special files with mknod()", false},
	{"lease", 28, "establish leases on arbitrary files", false},
	{"audit_write", 29, "write records to the kernel audit log", false},
	{"audit_control", 30, "enable, disable, and configure kernel auditing", false},
	{"setfcap", 31, "set file capabilities", false},
	{"mac_override", 32, "override Mandatory Access Control (Smack, AppArmor)", false},
	{"mac_admin", 33, "configure or change Mandatory Access Control policy", false},
	{"syslog", 34, "perform privileged syslog(2) operations", false},
	{"wake_alarm", 35, "trigger something that will wake the system", false},
	{"block_suspend", 36, "block system suspend", false},
	{"audit_read", 37, "read the kernel audit log via a multicast netlink socket", false},
	{"perfmon", 38, "access performance monitoring and observability (perf_events)", false},
	{"bpf", 39, "use privileged BPF operations", false},
	{"checkpoint_restore", 40, "checkpoint and restore functionality (CRIU)", false},
}

// capByName indexes catalog by canonical name for O(1) validation lookups.
var capByName = func() map[string]Capability {
	m := make(map[string]Capability, len(catalog))
	for _, c := range catalog {
		m[c.Name] = c
	}
	return m
}()

// Catalog returns the capability catalog in kernel bit order. The returned
// slice is a copy; callers may sort or mutate it freely.
func Catalog() []Capability {
	out := make([]Capability, len(catalog))
	copy(out, catalog)
	return out
}

// Normalize canonicalizes a capability name written in any common spelling
// (CAP_NET_ADMIN, NET_ADMIN, net_admin) to its LXC form: lowercase, no CAP_
// prefix. Unknown names return an error that suggests the nearest known name.
func Normalize(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "cap_")
	if _, ok := capByName[n]; ok {
		return n, nil
	}
	if suggestion := nearestCap(n); suggestion != "" {
		return "", fmt.Errorf("unknown capability %q (did you mean %q?)", name, suggestion)
	}
	return "", fmt.Errorf("unknown capability %q", name)
}

// IsDangerous reports whether name (in any accepted spelling) is one of the
// capabilities that materially weaken container isolation. Unknown names are
// reported as not dangerous; callers validate names separately via Normalize.
func IsDangerous(name string) bool {
	n, err := Normalize(name)
	if err != nil {
		return false
	}
	_, ok := dangerousCaps[n]
	return ok
}

// DecodeMask turns a hex capability bitmask, as found in the CapBnd/CapEff
// fields of /proc/<pid>/status (for example "000001ffffffffff"), into the set
// of capability names it selects, ordered by bit index. Bits past the end of
// the catalog render as cap_<bit> placeholders rather than errors, so newer
// kernels do not break the decode.
func DecodeMask(hex string) ([]string, error) {
	s := strings.TrimSpace(hex)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return nil, fmt.Errorf("empty capability mask")
	}
	mask, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return nil, fmt.Errorf("invalid hex capability mask %q", hex)
	}
	var names []string
	for bit := 0; bit < mask.BitLen(); bit++ {
		if mask.Bit(bit) == 0 {
			continue
		}
		if bit < len(catalog) {
			names = append(names, catalog[bit].Name)
		} else {
			names = append(names, fmt.Sprintf("cap_%d", bit))
		}
	}
	return names, nil
}

// nearestCap returns the catalog name closest to n by Levenshtein distance, for
// the "did you mean" hint on an unknown capability. It returns "" when nothing
// is close enough to be a useful suggestion.
func nearestCap(n string) string {
	best := ""
	bestDist := 1 << 30
	for _, c := range catalog {
		d := levenshtein(n, c.Name)
		if d < bestDist {
			bestDist, best = d, c.Name
		}
	}
	// Only suggest when the typo is plausibly the same word: within three
	// edits, or within half its length for longer names.
	threshold := 3
	if half := len(n) / 2; half > threshold {
		threshold = half
	}
	if bestDist <= threshold {
		return best
	}
	return ""
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
