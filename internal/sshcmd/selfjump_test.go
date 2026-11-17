package sshcmd

import "testing"

func TestSelfJump(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		jump string
		host string
		port int
		want bool
	}{
		{"same host", "root@192.168.1.180", "192.168.1.180", 22, true},
		{"same host no user", "192.168.1.180", "192.168.1.180", 22, true},
		{"same host different user", "admin@192.168.1.180", "192.168.1.180", 22, true},
		{"same name different case", "root@PVE-0", "pve-0", 22, true},
		{"explicit default port", "root@10.0.0.1:22", "10.0.0.1", 22, true},
		{"different port on jump", "root@10.0.0.1:2222", "10.0.0.1", 22, false},
		{"different port on target", "root@10.0.0.1", "10.0.0.1", 2222, false},
		{"matching non-default port", "root@10.0.0.1:2222", "10.0.0.1", 2222, true},
		{"ipv6 bracketed", "root@[fd00::1]", "fd00::1", 22, true},
		{"ipv6 bracketed with port", "root@[fd00::1]:2222", "fd00::1", 2222, true},
		{"ipv6 bracketed port mismatch", "root@[fd00::1]:2222", "fd00::1", 22, false},
		{"ipv6 different spelling", "root@[fd00:0:0:0:0:0:0:1]", "fd00::1", 22, true},
		{"ipv6 different host", "root@[fd00::2]", "fd00::1", 22, false},
		{"different host", "root@192.168.1.1", "192.168.1.180", 22, false},
		{"ssh config alias", "bastion", "192.168.1.180", 22, false},
		{"multi-hop last equals target", "root@10.0.0.9,root@192.168.1.180", "192.168.1.180", 22, true},
		{"multi-hop first equals target", "root@192.168.1.180,root@10.0.0.9", "192.168.1.180", 22, false},
		{"multi-hop none equal", "a,b", "192.168.1.180", 22, false},
		{"multi-hop a,b last equals", "a,b", "b", 22, true},
		{"empty jump", "", "192.168.1.180", 22, false},
		{"empty host", "root@x", "", 22, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := SelfJump(tc.jump, tc.host, tc.port); got != tc.want {
				t.Fatalf("SelfJump(%q, %q, %d) = %v, want %v", tc.jump, tc.host, tc.port, got, tc.want)
			}
		})
	}
}

func TestForHostDropsSelfJump(t *testing.T) {
	t.Parallel()

	f := Flags{User: "root", Port: 22, Jump: "root@192.168.1.180"}
	got := BaseArgs(&f, "192.168.1.180")
	for _, a := range got {
		if a == "-J" {
			t.Fatalf("self jump kept: %v", got)
		}
	}

	kept := f.ForHost("192.168.1.181")
	if kept.Jump != f.Jump || kept.NoJump {
		t.Fatalf("different host changed flags: %+v", kept)
	}
}
