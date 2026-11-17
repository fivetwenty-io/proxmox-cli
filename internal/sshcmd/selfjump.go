package sshcmd

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// defaultSSHPort is the port ssh dials when a destination names none.
const defaultSSHPort = 22

// SelfJump reports whether the last hop of the ssh -J list jump is the same
// endpoint as the target host on port. OpenSSH refuses such a command with
// "jumphost loop via <host>", so the jump has to be dropped for that call.
//
// The comparison is purely textual. Each side loses any "ssh://" prefix, any
// "user@", and any ":port" (IPv6 brackets included). Two hosts match when they
// are the same name ignoring case, or when both are IP literals for the same
// address. It does not resolve names and does not read ~/.ssh/config, so an
// ssh-config alias for the jump is never judged to be the target. The ports
// must also match, with 22 for a hop that names none. Only the last hop is
// compared, because only the last hop is the one ssh dials the target through.
func SelfJump(jump, host string, port int) bool {
	jump = strings.TrimSpace(jump)
	if jump == "" || strings.TrimSpace(host) == "" {
		return false
	}

	last := jump
	if i := strings.LastIndex(jump, ","); i >= 0 {
		last = jump[i+1:]
	}

	jHost, jPort := splitHop(last)
	tHost, tPort := splitHop(host)
	if jHost == "" || tHost == "" {
		return false
	}

	if jPort == 0 {
		jPort = defaultSSHPort
	}
	// A target address that carries its own port overrides the flag's.
	if tPort == 0 {
		tPort = port
	}
	if tPort <= 0 {
		tPort = defaultSSHPort
	}
	if jPort != tPort {
		return false
	}

	return sameEndpointHost(jHost, tHost)
}

// ForHost returns f, or a copy of it, for a call whose destination is host. When f's jump ends at
// that same host and port, the jump is dropped and NoJump is set, so ssh dials
// the target directly and ~/.ssh/config cannot add the jump back.
func (f *Flags) ForHost(host string) *Flags {
	if f.Jump == "" || !SelfJump(f.Jump, host, f.Port) {
		return f
	}
	c := *f
	c.Jump = ""
	c.NoJump = true
	return &c
}

// splitHop reduces one ssh destination ("[ssh://][user@]host[:port]") to its
// host and port. The port is 0 when the destination names none. The host comes
// back without IPv6 brackets, lowercased.
func splitHop(s string) (host string, port int) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "ssh://")
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}

	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", 0
		}
		host = s[1:end]
		if rest := s[end+1:]; strings.HasPrefix(rest, ":") {
			port, _ = strconv.Atoi(rest[1:])
		}
		return strings.ToLower(host), port
	}

	// More than one colon without brackets is a bare IPv6 address.
	if strings.Count(s, ":") == 1 {
		if h, p, err := net.SplitHostPort(s); err == nil {
			port, _ = strconv.Atoi(p)
			return strings.ToLower(h), port
		}
	}

	return strings.ToLower(s), 0
}

// sameEndpointHost reports whether a and b name the same host: equal names, or
// IP literals for the same address.
func sameEndpointHost(a, b string) bool {
	if a == b {
		return true
	}

	ia, errA := netip.ParseAddr(a)
	ib, errB := netip.ParseAddr(b)
	if errA != nil || errB != nil {
		return false
	}

	return ia.WithZone("").Unmap() == ib.WithZone("").Unmap()
}
