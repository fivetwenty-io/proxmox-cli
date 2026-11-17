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
// endpoint as the target host, port, and user. OpenSSH refuses such a command
// with "jumphost loop via <host>".
//
// The comparison is purely textual. Each side loses any "ssh://" prefix and
// ":port" (IPv6 brackets included). Two hosts match when they are the same
// name ignoring case, or when both are IP literals for the same address, zone
// included. It does not resolve names and does not read ~/.ssh/config, so an
// ssh-config alias for the jump is never judged to be the target. The ports
// must match, with 22 for a side that names none. The jump hop's user must be
// empty or equal to user, as OpenSSH's own loop check has it, so a hop that
// logs in as a different user is not a loop. Only the last hop is compared,
// because only the last hop is the one ssh dials the target through.
func SelfJump(jump, host string, port int, user string) bool {
	hops := splitChain(jump)
	if len(hops) == 0 {
		return false
	}

	return hopIsTarget(hops[len(hops)-1], host, port, user)
}

// ForHost returns f, or a copy of it, for a call whose destination is host.
// Trailing hops of f's jump chain that are the target itself are trimmed, since
// ssh refuses them as a jumphost loop, while the earlier hops stay in order, so
// "a,x" to x becomes "a". When no hop is left, the jump is dropped and NoJump
// is set, so ssh dials the target directly and ~/.ssh/config cannot add the
// jump back.
func (f *Flags) ForHost(host string) *Flags {
	hops := splitChain(f.Jump)
	n := len(hops)
	for n > 0 && hopIsTarget(hops[n-1], host, f.Port, f.User) {
		n--
	}
	if n == len(hops) {
		return f
	}

	c := *f
	c.Jump = strings.Join(hops[:n], ",")
	if n == 0 {
		c.NoJump = true
	}
	return &c
}

// splitChain splits an ssh -J list into its trimmed, non-empty hops.
func splitChain(jump string) []string {
	var hops []string
	for h := range strings.SplitSeq(jump, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hops = append(hops, h)
		}
	}
	return hops
}

// hopIsTarget reports whether one jump hop names the target host, port, and
// user.
func hopIsTarget(hop, host string, port int, user string) bool {
	if strings.TrimSpace(host) == "" {
		return false
	}

	jUser, jHost, jPort := splitHop(hop)
	_, tHost, tPort := splitHop(host)
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

	if jUser != "" && jUser != user {
		return false
	}

	return sameEndpointHost(jHost, tHost)
}

// splitHop reduces one ssh destination ("[ssh://][user@]host[:port]") to its
// user, host, and port. The port is 0 when the destination names none. The host comes
// back without IPv6 brackets, lowercased.
func splitHop(s string) (user, host string, port int) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "ssh://")
	if i := strings.LastIndex(s, "@"); i >= 0 {
		user, s = s[:i], s[i+1:]
	}

	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return user, "", 0
		}
		host = s[1:end]
		if rest := s[end+1:]; strings.HasPrefix(rest, ":") {
			port, _ = strconv.Atoi(rest[1:])
		}
		return user, strings.ToLower(host), port
	}

	// More than one colon without brackets is a bare IPv6 address.
	if strings.Count(s, ":") == 1 {
		if h, p, err := net.SplitHostPort(s); err == nil {
			port, _ = strconv.Atoi(p)
			return user, strings.ToLower(h), port
		}
	}

	return user, strings.ToLower(s), 0
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

	return ia.Unmap() == ib.Unmap()
}
