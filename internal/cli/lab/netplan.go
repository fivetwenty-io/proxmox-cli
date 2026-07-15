package lab

import (
	"fmt"
	"net"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// labNetworkPlanIssues checks a lab's network plan for internal coherence and
// returns one message per problem found. Only fields that are actually set
// are checked, so a partially-authored lab (config add leaves network.mgmt
// empty for the operator) passes cleanly. The containment rules matter
// because every lab address lives on one flat vnet: an address plan whose
// pieces fall outside network.cidr produces guests and a node that cannot
// reach each other directly even though they share a broadcast domain.
func labNetworkPlanIssues(n config.LabNetwork) []string {
	var issues []string

	if n.CIDR == "" {
		return issues
	}
	_, cidr, err := net.ParseCIDR(n.CIDR)
	if err != nil {
		return append(issues, fmt.Sprintf("network.cidr %q is invalid: %v", n.CIDR, err))
	}

	if n.Mgmt.Subnet != "" {
		_, mgmt, err := net.ParseCIDR(n.Mgmt.Subnet)
		switch {
		case err != nil:
			issues = append(issues, fmt.Sprintf("network.mgmt.subnet %q is invalid: %v", n.Mgmt.Subnet, err))
		case !cidrContains(cidr, mgmt):
			issues = append(issues, fmt.Sprintf(
				"network.mgmt.subnet %s is not contained in network.cidr %s", n.Mgmt.Subnet, n.CIDR))
		}
	}

	if n.Mgmt.HostIP != "" {
		ip := net.ParseIP(n.Mgmt.HostIP)
		switch {
		case ip == nil:
			issues = append(issues, fmt.Sprintf("network.mgmt.host_ip %q is not a valid IP address", n.Mgmt.HostIP))
		case !cidr.Contains(ip):
			issues = append(issues, fmt.Sprintf(
				"network.mgmt.host_ip %s is not inside network.cidr %s", n.Mgmt.HostIP, n.CIDR))
		}
	}

	if n.Mgmt.Gateway != "" {
		ip := net.ParseIP(n.Mgmt.Gateway)
		switch {
		case ip == nil:
			issues = append(issues, fmt.Sprintf("network.mgmt.gateway %q is not a valid IP address", n.Mgmt.Gateway))
		case !cidr.Contains(ip):
			issues = append(issues, fmt.Sprintf(
				"network.mgmt.gateway %s is not inside network.cidr %s", n.Mgmt.Gateway, n.CIDR))
		}
	}

	if n.BoshBloc != "" {
		_, bosh, err := net.ParseCIDR(n.BoshBloc)
		switch {
		case err != nil:
			issues = append(issues, fmt.Sprintf("network.bosh_bloc %q is invalid: %v", n.BoshBloc, err))
		case !cidrContains(cidr, bosh):
			issues = append(issues, fmt.Sprintf(
				"network.bosh_bloc %s is not contained in network.cidr %s", n.BoshBloc, n.CIDR))
		}
	}

	return issues
}

// cidrContains reports whether outer fully contains inner: inner's base
// address falls inside outer and inner's prefix is at least as long as
// outer's.
func cidrContains(outer, inner *net.IPNet) bool {
	outerOnes, _ := outer.Mask.Size()
	innerOnes, _ := inner.Mask.Size()
	return outer.Contains(inner.IP) && innerOnes >= outerOnes
}

// guestPrefixWarning inspects the guest-agent-reported interface addresses
// for one inside the lab's network.cidr whose reported prefix length is
// narrower (longer) than the cidr's own. Such a node accepts direct-L2
// traffic from any guest in the cidr but routes its replies via the gateway,
// which drops them as out-of-state: TCP from guests to the node times out
// while one-way probes appear healthy. Returns ("", false) when the cidr is
// unset or invalid, no in-cidr address is found, the agent did not report a
// prefix, or the prefix matches.
func guestPrefixWarning(ifaces *agentNetworkInterfaces, labCIDR string) (string, bool) {
	if labCIDR == "" {
		return "", false
	}
	_, cidr, err := net.ParseCIDR(labCIDR)
	if err != nil {
		return "", false
	}
	cidrOnes, _ := cidr.Mask.Size()

	for _, iface := range ifaces.Result {
		if iface.Name == "lo" {
			continue
		}
		for _, addr := range iface.IPAddresses {
			if addr.IPAddressType != "ipv4" || addr.Prefix == nil {
				continue
			}
			ip := net.ParseIP(addr.IPAddress)
			if ip == nil || !cidr.Contains(ip) {
				continue
			}
			if *addr.Prefix > cidrOnes {
				return fmt.Sprintf(
					"guest interface %s has address %s/%d, narrower than network.cidr %s: "+
						"replies to on-link guests in the cidr will hairpin via the gateway and be "+
						"dropped as out-of-state; re-address the interface with the cidr's /%d prefix",
					iface.Name, addr.IPAddress, *addr.Prefix, labCIDR, cidrOnes), true
			}
			return "", false
		}
	}
	return "", false
}
