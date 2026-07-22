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

	issues = append(issues, labVnetsPlanIssues(n, cidr)...)
	issues = append(issues, labHostNICsPlanIssues(n)...)

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

// cidrsOverlap reports whether a and b's address ranges intersect at all.
// CIDR blocks are power-of-two aligned, so any two either are fully disjoint
// or one is fully contained in the other — there is no partial-overlap case
// to special-case — which means checking each network's base address against
// the other's range is a complete test in either direction.
func cidrsOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

// labVnetsPlanIssues checks n.Vnets for internal coherence against n itself:
// every vnet ID (together with the primary n.VnetID) must be unique and fit
// PVE's vnet ID charset/length constraint, and every vnet's CIDR (when set —
// a vnet may be a pure L2 passthrough with no subnet, e.g. a workload vnet)
// must nest inside the lab's overall network.cidr the same way
// network.mgmt.subnet and network.bosh_bloc already must, and must not
// double-book address space already claimed by network.mgmt.subnet,
// network.bosh_bloc, or another network.vnets[] entry. cidr is n.CIDR
// already parsed by the caller (only called once n.CIDR itself is known
// valid).
func labVnetsPlanIssues(n config.LabNetwork, cidr *net.IPNet) []string {
	var issues []string

	seenIDs := make(map[string]bool, len(n.Vnets)+1)
	if n.VnetID != "" {
		seenIDs[n.VnetID] = true
	}

	type parsedVnet struct {
		idx   int
		vnet  config.LabVnet
		block *net.IPNet
	}
	parsed := make([]parsedVnet, 0, len(n.Vnets))

	for i, v := range n.Vnets {
		if v.ID == "" {
			issues = append(issues, fmt.Sprintf("network.vnets[%d].id is required", i))
		} else {
			if !configVnetIDPattern.MatchString(v.ID) {
				issues = append(issues, fmt.Sprintf(
					"network.vnets[%d].id %q must be 1-8 alphanumeric characters with no hyphen", i, v.ID))
			}
			if seenIDs[v.ID] {
				issues = append(issues, fmt.Sprintf(
					"network.vnets[%d].id %q collides with the primary vnet_id or an earlier network.vnets[] entry",
					i, v.ID))
			}
			seenIDs[v.ID] = true
		}

		if v.CIDR == "" {
			continue
		}
		_, block, err := net.ParseCIDR(v.CIDR)
		if err != nil {
			issues = append(issues, fmt.Sprintf("network.vnets[%d].cidr %q is invalid: %v", i, v.CIDR, err))
			continue
		}
		if !cidrContains(cidr, block) {
			issues = append(issues, fmt.Sprintf(
				"network.vnets[%d].cidr %s is not contained in network.cidr %s", i, v.CIDR, n.CIDR))
		}
		parsed = append(parsed, parsedVnet{idx: i, vnet: v, block: block})
	}

	// Overlap checks below only run against fields already confirmed
	// parseable by the caller's own mgmt/bosh_bloc checks above; a mgmt or
	// bosh_bloc field that is itself malformed already produced its own
	// issue there; re-parsing here and silently skipping on error (rather
	// than erroring a second time) avoids a duplicate report for the same
	// root cause.
	var mgmt, bosh *net.IPNet
	if n.Mgmt.Subnet != "" {
		if _, m, err := net.ParseCIDR(n.Mgmt.Subnet); err == nil {
			mgmt = m
		}
	}
	if n.BoshBloc != "" {
		if _, b, err := net.ParseCIDR(n.BoshBloc); err == nil {
			bosh = b
		}
	}

	for i, p := range parsed {
		if mgmt != nil && cidrsOverlap(p.block, mgmt) {
			issues = append(issues, fmt.Sprintf(
				"network.vnets[%d].cidr %s overlaps network.mgmt.subnet %s", p.idx, p.vnet.CIDR, n.Mgmt.Subnet))
		}
		if bosh != nil && cidrsOverlap(p.block, bosh) {
			issues = append(issues, fmt.Sprintf(
				"network.vnets[%d].cidr %s overlaps network.bosh_bloc %s", p.idx, p.vnet.CIDR, n.BoshBloc))
		}
		for j := i + 1; j < len(parsed); j++ {
			q := parsed[j]
			if cidrsOverlap(p.block, q.block) {
				issues = append(issues, fmt.Sprintf(
					"network.vnets[%d].cidr %s overlaps network.vnets[%d].cidr %s",
					p.idx, p.vnet.CIDR, q.idx, q.vnet.CIDR))
			}
		}
	}

	return issues
}

// labHostNICsPlanIssues checks n.HostNICs for internal coherence against n
// itself: every entry's index must be >=1 (net0 is always the primary vnet
// and is never repeated in this list) and unique, and every entry's VnetID
// must resolve to either the primary n.VnetID or one of n.Vnets[].ID — a
// HostNIC naming any other vnet ID would ask createVM/createQdeviceVM to
// attach a qm net line to a vnet this lab never ensures, which PVE would
// reject at VM-create time with a much less actionable error.
func labHostNICsPlanIssues(n config.LabNetwork) []string {
	var issues []string

	validVnetIDs := make(map[string]bool, len(n.Vnets)+1)
	if n.VnetID != "" {
		validVnetIDs[n.VnetID] = true
	}
	for _, v := range n.Vnets {
		if v.ID != "" {
			validVnetIDs[v.ID] = true
		}
	}

	seenIndex := make(map[int]int, len(n.HostNICs))
	for i, hn := range n.HostNICs {
		switch {
		case hn.Index < 1:
			issues = append(issues, fmt.Sprintf(
				"network.host_nics[%d].index %d must be >= 1 (net0 is reserved for the primary vnet)",
				i, hn.Index))
		default:
			if prior, dup := seenIndex[hn.Index]; dup {
				issues = append(issues, fmt.Sprintf(
					"network.host_nics[%d].index %d collides with network.host_nics[%d].index "+
						"(every netN index must be unique)", i, hn.Index, prior))
			} else {
				seenIndex[hn.Index] = i
			}
		}

		switch {
		case hn.VnetID == "":
			issues = append(issues, fmt.Sprintf("network.host_nics[%d].vnet_id is required", i))
		case !validVnetIDs[hn.VnetID]:
			issues = append(issues, fmt.Sprintf(
				"network.host_nics[%d].vnet_id %q does not resolve to the primary vnet_id %q "+
					"or any network.vnets[] entry", i, hn.VnetID, n.VnetID))
		}
	}

	return issues
}

// labNestedNetworkPlanIssues checks a lab's nested in-guest bonding/bridging
// and inner SDN vlan-zone plan for internal coherence. It wraps
// config.ValidateNestedNetwork verbatim — including that function's own
// VlanZone.Bridge-vs-Bonds[].Bridge+VlanAware cross-reference check — rather
// than reimplementing it, so this file has one canonical place a caller
// (config add, create, scale) reaches for the "is this lab's network plan
// coherent" gate that already covers both the outer SDN plan
// (labNetworkPlanIssues) and the inner nested-node plan. name is the lab's
// name, threaded straight through to config.ValidateNestedNetwork's own
// per-lab message prefix. A zero-value nn (no Bonds, no VlanZone) always
// returns nil, matching config.ValidateNestedNetwork's own contract.
func labNestedNetworkPlanIssues(name string, nn config.LabNestedNetwork) []string {
	return config.ValidateNestedNetwork(name, nn)
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
