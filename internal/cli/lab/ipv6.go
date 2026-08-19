package lab

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// IPv6 address plan for a lab (default on; `network.ipv6: false` opts out).
//
// Every lab gets one IPv6 block: network.cidr6 when set (/48 or wider), else
// a stable RFC 4193 ULA /48 derived from network.cidr (labULAPrefix), so two
// labs with different IPv4 CIDRs can never collide even without any IPv6
// config at all. Per-role /64s are carved from the block by a fixed 16-bit
// subnet ID (bits 48..64), keeping the whole plan predictable from the lab
// file alone:
//
//	subnet ID 1       management (nodes, QDevice, gateway)
//	subnet ID 2       bosh bloc reservation (documented, never provisioned
//	                  as an SDN subnet — parity with bosh_bloc, which is an
//	                  address-plan reservation, not an SDN object)
//	subnet ID 16+i    network.vnets[i] (skipped for a pure L2 vnet)
//	subnet ID 32+i    inner vlan-zone vnets[i] (sdninner)
//
// The vnet bases bound network.vnets at 16 entries when IPv6 is enabled — a
// 17th (index 16, subnet ID 32) would collide with inner vlan vnet 0 —
// which labIPv6PlanIssues enforces as a validation error.
//
// Host offsets inside the mgmt /64 mirror the IPv4 plan exactly: gateway 1,
// node i at 10+i, QDevice at 15 — rendered in hex (::1, ::a-::e, ::f).
const (
	labV6SubnetMgmt      = 1
	labV6SubnetVnetBase  = 16
	labV6SubnetInnerBase = 32
)

// labV6GatewayOffset is the gateway's host offset inside every carved /64,
// mirroring the IPv4 ".1" convention.
const labV6GatewayOffset = 1

// labV6InterfacePrefixBits is the prefix length a HOST INTERFACE inside the
// lab is addressed with: the whole lab block's /48, not the mgmt /64 — the
// per-role /64s are address-plan reservations, exactly like network.mgmt.
// subnet's /24 inside the /16 (see config.LabMgmt.Subnet's doc comment). An
// interface addressed with the narrower /64 would route replies to on-link
// guests in other /64s via the gateway, which drops them as out-of-state —
// the same hairpin failure guestPrefixWarning documents for IPv4.
const labV6InterfacePrefixBits = 48

// labULAPrefix resolves a lab's IPv6 block: n.CIDR6 verbatim when set (must
// parse as an IPv6 prefix of /48 or wider; its first /48 is used), else a
// deterministic RFC 4193 ULA /48 derived from n.CIDR — fd00::/8 with the
// 40-bit Global ID taken from SHA-256 of the CIDR's canonical (masked
// network-base) form, so the same lab file always derives the same block and
// a CIDR authored with stray host bits derives the same block as its clean
// spelling.
func labULAPrefix(n config.LabNetwork) (netip.Prefix, error) {
	if n.CIDR6 != "" {
		p, err := netip.ParsePrefix(n.CIDR6)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("network.cidr6 %q is invalid: %w", n.CIDR6, err)
		}
		if !p.Addr().Is6() || p.Addr().Is4In6() {
			return netip.Prefix{}, fmt.Errorf("network.cidr6 %q is not an IPv6 prefix", n.CIDR6)
		}
		if p.Bits() > 48 {
			return netip.Prefix{}, fmt.Errorf(
				"network.cidr6 %q is narrower than /48; the lab block must be /48 or wider so per-role "+
					"/64s (mgmt, vnets) can be carved from it — set per-vnet cidr6 overrides instead if "+
					"you only have a single /64", n.CIDR6)
		}
		p48, err := p.Masked().Addr().Prefix(48)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("network.cidr6 %q: %w", n.CIDR6, err)
		}
		return p48, nil
	}

	if n.CIDR == "" {
		return netip.Prefix{}, fmt.Errorf(
			"lab network has neither cidr6 nor cidr set; cannot derive an IPv6 block")
	}
	_, parsed, err := net.ParseCIDR(n.CIDR)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("network.cidr %q is invalid: %w", n.CIDR, err)
	}

	sum := sha256.Sum256([]byte(parsed.String()))
	var addr [16]byte
	addr[0] = 0xfd
	copy(addr[1:6], sum[:5])
	return netip.PrefixFrom(netip.AddrFrom16(addr), 48), nil
}

// labV6Subnet carves the /64 with the given 16-bit subnet ID (bits 48..64)
// out of block, which must be a /48 in masked form (labULAPrefix's output).
func labV6Subnet(block netip.Prefix, id int) (netip.Prefix, error) {
	if id < 0 || id > 0xffff {
		return netip.Prefix{}, fmt.Errorf("IPv6 subnet ID %d is out of range [0, 65535]", id)
	}
	addr := block.Masked().Addr().As16()
	binary.BigEndian.PutUint16(addr[6:8], uint16(id))
	return netip.PrefixFrom(netip.AddrFrom16(addr), 64), nil
}

// labV6OffsetIP returns the address at the given host offset within the
// subnet cidr6: the subnet base with the offset in its lowest 16 bits — the
// IPv6 counterpart of labVnetOffsetIP's last-octet rule, wide enough for
// every offset the lab plan uses (1, 10-15). The subnet must be /112 or
// wider: on anything narrower the low 16 bits are not all host bits, so the
// overwrite would land the result OUTSIDE the subnet (fd00::100/120's
// "gateway" would come out as ::1) — an error here, never a mis-derivation
// PVE later rejects as "not in subnet".
func labV6OffsetIP(cidr6 string, offset int) (string, error) {
	if offset < 0 || offset > 0xffff {
		return "", fmt.Errorf("IPv6 offset %d is out of range [0, 65535]", offset)
	}
	p, err := netip.ParsePrefix(cidr6)
	if err != nil {
		return "", fmt.Errorf("cidr6 %q is invalid: %w", cidr6, err)
	}
	if !p.Addr().Is6() || p.Addr().Is4In6() {
		return "", fmt.Errorf("cidr6 %q is not an IPv6 prefix", cidr6)
	}
	if p.Bits() > 112 {
		return "", fmt.Errorf(
			"cidr6 %q (/%d) is narrower than /112, so it cannot hold the lab plan's 16-bit host "+
				"offsets (gateway ::1, hosts ::a-::f); use a /112 or wider subnet", cidr6, p.Bits())
	}
	addr := p.Masked().Addr().As16()
	binary.BigEndian.PutUint16(addr[14:16], uint16(offset))
	return netip.AddrFrom16(addr).String(), nil
}

// labMgmtCIDR6 returns the lab's management /64 (subnet ID labV6SubnetMgmt
// of the lab block) as a CIDR string.
func labMgmtCIDR6(n config.LabNetwork) (string, error) {
	block, err := labULAPrefix(n)
	if err != nil {
		return "", err
	}
	subnet, err := labV6Subnet(block, labV6SubnetMgmt)
	if err != nil {
		return "", err
	}
	return subnet.String(), nil
}

// labMgmtGateway6 returns the management /64's gateway address (offset
// labV6GatewayOffset), the IPv6 counterpart of network.mgmt.gateway's ".1"
// convention. Like the IPv4 gateway, it is realized by the SDN subnet's
// Gateway field: PVE addresses the outer host's vnet bridge with it.
func labMgmtGateway6(n config.LabNetwork) (string, error) {
	cidr6, err := labMgmtCIDR6(n)
	if err != nil {
		return "", err
	}
	return labV6OffsetIP(cidr6, labV6GatewayOffset)
}

// labNodeMgmtIP6 returns node index i's (0-based) management IPv6 address:
// offset 10+i inside the mgmt /64, mirroring labNodeMgmtIP's IPv4 offsets
// exactly (nodes ::a-::e for i in 0..4). The index bound mirrors
// labVnetNodeIP's too: without it, index 5 would silently derive offset 15 —
// the QDevice's own ::f — as a duplicate address.
func labNodeMgmtIP6(n config.LabNetwork, i int) (string, error) {
	if i < 0 || i > maxLabNodeIndex {
		return "", fmt.Errorf("node index %d is out of range [0, %d]", i, maxLabNodeIndex)
	}
	cidr6, err := labMgmtCIDR6(n)
	if err != nil {
		return "", err
	}
	return labV6OffsetIP(cidr6, 10+i)
}

// labQdeviceMgmtIP6 returns the QDevice VM's management IPv6 address: offset
// 15 (::f) inside the mgmt /64, mirroring labQdeviceMgmtIP.
func labQdeviceMgmtIP6(n config.LabNetwork) (string, error) {
	cidr6, err := labMgmtCIDR6(n)
	if err != nil {
		return "", err
	}
	return labV6OffsetIP(cidr6, 15)
}

// labVnetCIDR6 returns the IPv6 subnet for network.vnets[i]: the entry's own
// CIDR6 override when set, else the /64 with subnet ID labV6SubnetVnetBase+i
// carved from the lab block. A pure L2 vnet (no IPv4 CIDR) returns "" — it
// gets no IPv6 subnet either, matching ensureLabSdnVnetSubnet's
// skip-if-cidr-empty rule — even when it carries a cidr6 override, a
// contradiction labIPv6PlanIssues rejects at validation.
func labVnetCIDR6(n config.LabNetwork, i int) (string, error) {
	if i < 0 || i >= len(n.Vnets) {
		return "", fmt.Errorf("vnet index %d is out of range [0, %d]", i, len(n.Vnets)-1)
	}
	v := n.Vnets[i]
	if v.CIDR == "" {
		return "", nil
	}
	if v.CIDR6 != "" {
		return v.CIDR6, nil
	}
	block, err := labULAPrefix(n)
	if err != nil {
		return "", err
	}
	subnet, err := labV6Subnet(block, labV6SubnetVnetBase+i)
	if err != nil {
		return "", err
	}
	return subnet.String(), nil
}

// labInnerVnetCIDR6 returns the IPv6 subnet for the nested vlan zone's
// vnets[i]: the /64 with subnet ID labV6SubnetInnerBase+i carved from the
// lab block. Unlike the outer vnets (labVnetCIDR6) there is no per-vnet
// override — the inner client-VLAN plan is fully derived.
func labInnerVnetCIDR6(n config.LabNetwork, i int) (string, error) {
	if i < 0 {
		return "", fmt.Errorf("inner vnet index %d is negative", i)
	}
	block, err := labULAPrefix(n)
	if err != nil {
		return "", err
	}
	subnet, err := labV6Subnet(block, labV6SubnetInnerBase+i)
	if err != nil {
		return "", err
	}
	return subnet.String(), nil
}

// labInnerVnetV6Subnet returns the nested vlan zone vnets[i]'s carved IPv6
// /64 and its ::1 gateway together — the shape sdninner's ensure path
// consumes, mirroring net.go's labVnetV6Subnet for the outer vnets.
func labInnerVnetV6Subnet(n config.LabNetwork, i int) (cidr6, gateway6 string, err error) {
	cidr6, err = labInnerVnetCIDR6(n, i)
	if err != nil {
		return "", "", err
	}
	gateway6, err = labV6Gateway(cidr6)
	if err != nil {
		return "", "", err
	}
	return cidr6, gateway6, nil
}

// labV6Gateway returns the gateway address (offset labV6GatewayOffset) of an
// arbitrary IPv6 subnet cidr6 — the vnet-agnostic counterpart of the ".1"
// rule, usable against any carved or overridden /64.
func labV6Gateway(cidr6 string) (string, error) {
	return labV6OffsetIP(cidr6, labV6GatewayOffset)
}
