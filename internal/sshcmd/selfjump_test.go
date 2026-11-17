package sshcmd

import (
	"slices"
	"strings"
	"testing"
)

func TestSelfJump(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		jump string
		host string
		port int
		user string
		want bool
	}{
		{"same host", "root@192.168.1.180", "192.168.1.180", 22, "root", true},
		{"same host no user on jump", "192.168.1.180", "192.168.1.180", 22, "root", true},
		{"no user on either side", "192.168.1.180", "192.168.1.180", 22, "", true},
		{"same host different user", "admin@192.168.1.180", "192.168.1.180", 22, "root", false},
		{"explicit jump user, no target user", "root@192.168.1.180", "192.168.1.180", 22, "", false},
		{"same name different case", "root@PVE-0", "pve-0", 22, "root", true},
		{"explicit default port", "root@10.0.0.1:22", "10.0.0.1", 22, "root", true},
		{"different port on jump", "root@10.0.0.1:2222", "10.0.0.1", 22, "root", false},
		{"different port on target", "root@10.0.0.1", "10.0.0.1", 2222, "root", false},
		{"matching non-default port", "root@10.0.0.1:2222", "10.0.0.1", 2222, "root", true},
		{"ipv6 bracketed", "root@[fd00::1]", "fd00::1", 22, "root", true},
		{"ipv6 bracketed with port", "root@[fd00::1]:2222", "fd00::1", 2222, "root", true},
		{"ipv6 bracketed port mismatch", "root@[fd00::1]:2222", "fd00::1", 22, "root", false},
		{"ipv6 different spelling", "root@[fd00:0:0:0:0:0:0:1]", "fd00::1", 22, "root", true},
		{"ipv6 different host", "root@[fd00::2]", "fd00::1", 22, "root", false},
		{"ipv6 same zone", "root@[fe80::1%en0]", "fe80::1%en0", 22, "root", true},
		{"ipv6 different zone", "root@[fe80::1%en0]", "fe80::1%en1", 22, "root", false},
		{"different host", "root@192.168.1.1", "192.168.1.180", 22, "root", false},
		{"ssh config alias", "bastion", "192.168.1.180", 22, "root", false},
		{"multi-hop last equals target", "root@10.0.0.9,root@192.168.1.180", "192.168.1.180", 22, "root", true},
		{"multi-hop first equals target", "root@192.168.1.180,root@10.0.0.9", "192.168.1.180", 22, "root", false},
		{"multi-hop none equal", "a,b", "192.168.1.180", 22, "root", false},
		{"multi-hop a,b last equals", "a,b", "b", 22, "root", true},
		{"empty jump", "", "192.168.1.180", 22, "root", false},
		{"empty host", "root@x", "", 22, "root", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := SelfJump(tc.jump, tc.host, tc.port, tc.user); got != tc.want {
				t.Fatalf("SelfJump(%q, %q, %d, %q) = %v, want %v", tc.jump, tc.host, tc.port, tc.user, got, tc.want)
			}
		})
	}
}

func TestForHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		jump       string
		host       string
		wantJump   string
		wantNoJump bool
	}{
		{"single self hop dropped", "root@X", "X", "", true},
		{"A,X trims to A", "A,X", "X", "A", false},
		{"X,A kept", "X,A", "X", "X,A", false},
		{"A,X,X trims to A", "A,X,X", "X", "A", false},
		{"X,X dropped", "X,X", "X", "", true},
		{"different host kept", "root@X", "Y", "root@X", false},
		{"alias kept", "bastion", "X", "bastion", false},
		{"different user kept", "admin@X", "X", "admin@X", false},
		{"earlier hops keep order", "a,b,X", "X", "a,b", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &Flags{User: "root", Port: 22, Jump: tc.jump}
			got := f.ForHost(tc.host)
			if got.Jump != tc.wantJump || got.NoJump != tc.wantNoJump {
				t.Fatalf("ForHost(%q) on %q = Jump %q NoJump %v, want %q %v",
					tc.host, tc.jump, got.Jump, got.NoJump, tc.wantJump, tc.wantNoJump)
			}
			if f.Jump != tc.jump || f.NoJump {
				t.Fatalf("ForHost changed its receiver: %+v", f)
			}
		})
	}
}

func TestBaseArgsDropsSelfJump(t *testing.T) {
	t.Parallel()

	f := Flags{User: "root", Port: 22, Jump: "root@192.168.1.180"}
	for _, a := range BaseArgs(&f, "192.168.1.180") {
		if a == "-J" {
			t.Fatalf("self jump kept: %v", BaseArgs(&f, "192.168.1.180"))
		}
	}
}

func TestRemoteShellDropsSelfJump(t *testing.T) {
	t.Parallel()

	f := &Flags{User: "root", Port: 22, Jump: "root@192.168.1.180"}
	if got := RemoteShell(f.ForHost("192.168.1.180")); containsToken(got, "-J") {
		t.Fatalf("rsync shell kept the self jump: %s", got)
	}
	if got := RemoteShell(f.ForHost("192.168.1.181")); !containsToken(got, "-J") {
		t.Fatalf("rsync shell lost a real jump: %s", got)
	}
}

func containsToken(s, tok string) bool {
	return slices.Contains(strings.Fields(s), tok)
}
