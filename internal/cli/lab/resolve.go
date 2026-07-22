package lab

import (
	"fmt"
	"net"
	"regexp"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/peppi"
)

// labNameCharsetRE is the strict charset a lab name must match before any
// mutating verb acts on it: lowercase letters and digits, with internal
// hyphens allowed, starting and ending on a letter or digit — the same
// "hostname rules: alphanumeric + hyphen" convention the multi-node lab plan
// (§3.2) documents for a nested cluster's own name, and a superset-safe
// bound for every existing lab name in the fleet (e.g. "wayneeseguin",
// "pve-cpi"). This matters beyond cosmetics: several mutating verbs
// (cluster init/join, qdevice add, sdn apply, nfs attach/detach, quota set)
// interpolate lab.Name — or values derived from it (the cluster name passed
// to `pvecm create`, dataset paths) — directly into a remote shell command
// line run as root over ssh, rather than through a typed API parameter a
// server-side decoder would reject on its own; a name containing a space or
// shell metacharacter would otherwise reach that command line unvalidated.
var labNameCharsetRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validateLabNameCharset returns an error when name does not match
// labNameCharsetRE.
func validateLabNameCharset(name string) error {
	if !labNameCharsetRE.MatchString(name) {
		return fmt.Errorf(
			"lab name %q contains characters outside the allowed charset (lowercase letters, "+
				"digits, and internal hyphens only; must start and end with a letter or digit) — "+
				"refusing before it can reach any remote command line", name)
	}
	return nil
}

// resolveLab loads the active config via cli.GetDeps(cmd), resolves every
// configured lab (inline cfg.Labs plus cfg.Include/cfg.LabsDir includes, see
// config.ResolveLabs), and returns the one named name. Every read-only lab
// verb (list, status) calls this directly; every mutating verb calls
// resolveLabForMutate instead, which additionally peppi-guards the result.
func resolveLab(cmd *cobra.Command, name string) (*config.Lab, error) {
	deps := cli.GetDeps(cmd)

	labs, err := config.ResolveLabs(deps.Cfg, deps.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("resolve labs: %w", err)
	}

	lab, ok := labs[name]
	if !ok {
		return nil, fmt.Errorf("lab %q not found; available: %s", name, availableLabNames(labs))
	}

	return lab, nil
}

// availableLabNames returns the sorted, comma-joined names of labs, for use
// in the "lab not found" error's helpful listing. It returns "(none
// configured)" when labs is empty, since "available: " with nothing after it
// reads like a truncated message rather than an empty set.
func availableLabNames(labs map[string]*config.Lab) string {
	if len(labs) == 0 {
		return "(none configured)"
	}

	names := make([]string, 0, len(labs))
	for name := range labs {
		names = append(names, name)
	}
	sort.Strings(names)

	joined := names[0]
	for _, n := range names[1:] {
		joined += ", " + n
	}
	return joined
}

// resolveLabForMutate resolves the named lab exactly as resolveLab does,
// then builds a peppi.Target from every identifier the lab's resolved
// definition exposes (vnet ID, access pool, storage ID, DNS zone, and VM
// name) and calls peppi.Guard before returning. Every mutating verb
// (create, destroy, net apply, access grant, quota set, start, stop) must
// call this instead of resolveLab, so no mutating code path can reach the
// PVE API or a shell-out without first clearing the guard. VMID is passed as
// 0 here since the lab's VM ID is not known at config-resolution time; call
// sites that have since resolved a VMID (e.g. destroy, after a list lookup)
// must call peppi.Guard themselves a second time with that VMID before
// mutating.
func resolveLabForMutate(cmd *cobra.Command, name string) (*config.Lab, error) {
	lab, err := resolveLab(cmd, name)
	if err != nil {
		return nil, err
	}

	if err := validateLabNameCharset(lab.Name); err != nil {
		return nil, err
	}

	target := peppi.Target{
		VMID: 0,
		Names: []string{
			lab.Network.VnetID,
			labPoolID(lab),
			storageID(lab),
			lab.DNS.Zone,
			lab.Name,
		},
	}

	if err := peppi.Guard(target); err != nil {
		return nil, err
	}

	return lab, nil
}

// zfsBasePool returns the base ZFS pool name a lab's storage identifiers are
// derived from: lab.Storage.Pool verbatim when the operator set it (e.g.
// "tank", or a non-default pool such as "othertank"), else "tank" (the
// schema's documented default). This is purely the base pool name, never a
// full PVE storage-ID or dataset path; storageID and zfsDatasetPath both
// build on top of it, so every lab verb derives the same storage-ID and
// dataset path from the same base pool.
func zfsBasePool(lab *config.Lab) string {
	if lab.Storage.Pool != "" {
		return lab.Storage.Pool
	}
	return "tank"
}

// storageID returns the PVE storage.cfg identifier a lab's disks are
// expected to live on: "<base>-lab-<name>", where base is zfsBasePool(lab).
func storageID(lab *config.Lab) string {
	return fmt.Sprintf("%s-lab-%s", zfsBasePool(lab), lab.Name)
}

// zfsDatasetPath returns the raw ZFS dataset path backing a lab's storage:
// "<base>/labs/<name>", where base is zfsBasePool(lab). This is distinct
// from storageID's PVE storage.cfg identifier: it is the dataset path the
// zfspool storage definition points at, and the same path `quota set`
// targets directly over ssh, so both must derive from the same base pool.
func zfsDatasetPath(lab *config.Lab) string {
	return fmt.Sprintf("%s/labs/%s", zfsBasePool(lab), lab.Name)
}

// labPoolID returns the PVE resource pool a lab's VM is expected to be a
// member of: lab.Access.Pool verbatim when the operator set it explicitly,
// else the conventional "lab-<name>" derived from the lab's name. Every
// mutating lab verb that resolves the lab's pool (create, destroy, access
// grant, start, stop) calls this same helper, so a lab that omits
// access.pool resolves to the identical pool everywhere.
func labPoolID(lab *config.Lab) string {
	if lab.Access.Pool != "" {
		return lab.Access.Pool
	}
	return fmt.Sprintf("lab-%s", lab.Name)
}

// maxLabNodeIndex is the highest valid node index (topology.nodes maxes out
// at config.MaxTopologyNodes, indexes 0..4).
const maxLabNodeIndex = config.MaxTopologyNodes - 1

// labNodeVMName returns the VM name for node index i (0-based, 0..4) of a
// lab named name: "lab-<name>-<i>" (multi-node lab plan §3.2). i is not
// range-checked here — callers loop i over 0..config.EffectiveTopologyNodes(...)-1,
// which is already bounded to [0, maxLabNodeIndex] by ValidateTopology at
// config-load time.
func labNodeVMName(name string, i int) string {
	return fmt.Sprintf("lab-%s-%d", name, i)
}

// labQdeviceVMName returns the QDevice tie-breaker VM name for a lab named
// name: "lab-<name>-q" (multi-node lab plan §3.2).
func labQdeviceVMName(name string) string {
	return fmt.Sprintf("lab-%s-q", name)
}

// legacyLabVMName returns the pre-multi-node VM name convention for a lab
// named name: "lab-<name>", with no node-index suffix. lifecycle.go's
// findLabVMs treats a live VM with this exact name, found in the lab's pool,
// as node 0 for back-compat with labs created before topology.nodes existed
// (multi-node lab plan §3.2, decision D3's safety-net case).
func legacyLabVMName(name string) string {
	return fmt.Sprintf("lab-%s", name)
}

// labNodeMgmtIP returns the management IP address for node index i (0-based)
// of a lab's mgmt /24: the subnet's network address plus 10+i (".10"-".14"
// for i in 0..4). i must be in [0, maxLabNodeIndex]; any other value is a
// caller error, not a config error, since every call site loops i over an
// already-validated topology's node range. This is a one-line wrapper around
// the vnet-generalized labVnetNodeIP, called against the lab's resolved
// primary mgmt CIDR (labMgmtCIDR) — byte-identical output to before this
// helper was generalized.
func labNodeMgmtIP(n config.LabNetwork, i int) (string, error) {
	cidr, err := labMgmtCIDR(n)
	if err != nil {
		return "", err
	}
	return labVnetNodeIP(cidr, i)
}

// labQdeviceMgmtIP returns the QDevice VM's management IP address: the lab's
// mgmt /24 base plus ".15" (multi-node lab plan §3.3).
func labQdeviceMgmtIP(n config.LabNetwork) (string, error) {
	cidr, err := labMgmtCIDR(n)
	if err != nil {
		return "", err
	}
	return labVnetOffsetIP(cidr, 15)
}

// labMgmtOffsetIP returns the IPv4 address at offset (the last octet) within
// a lab's mgmt /24, derived from labMgmtCIDR(n). Kept as a thin wrapper
// (rather than removed outright) since nfs.go's NFS-gateway addressing
// (offset 1, not a node index) still calls it directly.
func labMgmtOffsetIP(n config.LabNetwork, offset int) (string, error) {
	cidr, err := labMgmtCIDR(n)
	if err != nil {
		return "", err
	}
	return labVnetOffsetIP(cidr, offset)
}

// labMgmtCIDR resolves a lab's mgmt subnet to an IPv4 CIDR string (e.g.
// "10.10.1.0/24"): n.Mgmt.Subnet verbatim when it is set (after validating
// it parses as an IPv4 CIDR), else n.Mgmt.HostIP masked to /24 and
// re-expressed as a CIDR (today's convention: HostIP is always node 0's own
// ".10" address, so masking it to /24 yields the same network base an
// explicit Subnet would state). Both fields empty, or set but unparsable, is
// an error: node/QDevice IP derivation has no other source of truth for the
// mgmt subnet's base address.
func labMgmtCIDR(n config.LabNetwork) (string, error) {
	if n.Mgmt.Subnet != "" {
		_, parsed, err := net.ParseCIDR(n.Mgmt.Subnet)
		if err != nil {
			return "", fmt.Errorf("network.mgmt.subnet %q is invalid: %w", n.Mgmt.Subnet, err)
		}
		if parsed.IP.To4() == nil {
			return "", fmt.Errorf("network.mgmt.subnet %q is not an IPv4 subnet", n.Mgmt.Subnet)
		}
		// parsed.String() re-expresses through the masked network base
		// (host bits zeroed), not n.Mgmt.Subnet verbatim: a subnet authored
		// with host bits set (e.g. "10.254.0.5/24") must still agree with
		// the node/QDevice IP derivation below, which always masks to the
		// network base — otherwise the NFS export ACL (nfsLabRwSharenfs)
		// diverges from the actual node subnet.
		return parsed.String(), nil
	}

	if n.Mgmt.HostIP != "" {
		ip := net.ParseIP(n.Mgmt.HostIP)
		if ip == nil {
			return "", fmt.Errorf("network.mgmt.host_ip %q is not a valid IP address", n.Mgmt.HostIP)
		}
		v4 := ip.To4()
		if v4 == nil {
			return "", fmt.Errorf("network.mgmt.host_ip %q is not a valid IPv4 address", n.Mgmt.HostIP)
		}
		base := v4.Mask(net.CIDRMask(24, 32))
		return fmt.Sprintf("%s/24", base.String()), nil
	}

	return "", fmt.Errorf(
		"lab network has neither mgmt.subnet nor mgmt.host_ip set; cannot derive a node management IP")
}

// labVnetBaseIP parses cidr (e.g. "10.254.32.0/24") and returns its IPv4
// network address (all-zero host bits). cidr must be a valid IPv4 CIDR;
// anything else is an error.
func labVnetBaseIP(cidr string) (net.IP, error) {
	_, parsed, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("cidr %q is invalid: %w", cidr, err)
	}
	v4 := parsed.IP.To4()
	if v4 == nil {
		return nil, fmt.Errorf("cidr %q is not an IPv4 subnet", cidr)
	}
	return v4, nil
}

// labVnetOffsetIP returns the IPv4 address at offset (the last octet) within
// the subnet described by cidr: the network address (labVnetBaseIP) plus
// offset. This is the vnet-agnostic form the old mgmt-only offset helper was
// generalized into, usable against any LabVnet.CIDR, not only a lab's mgmt
// subnet.
func labVnetOffsetIP(cidr string, offset int) (string, error) {
	if offset < 0 || offset > 255 {
		return "", fmt.Errorf("IP offset %d is out of range [0, 255]", offset)
	}

	base, err := labVnetBaseIP(cidr)
	if err != nil {
		return "", err
	}

	ip := make(net.IP, len(base))
	copy(ip, base)
	ip[len(ip)-1] = byte(offset)
	return ip.String(), nil
}

// labVnetNodeIP returns node index i's (0-based) address within an arbitrary
// vnet subnet cidr: the network address plus 10+i (".10"-".14" for i in
// 0..4) — the same offset rule labNodeMgmtIP has always applied to a lab's
// mgmt subnet, generalized here to any LabVnet.CIDR (e.g. a storage vnet's
// own per-node addressing, multi-AZ topology plan §2/§4). i must be in [0,
// maxLabNodeIndex]; any other value is a caller error, not a config error,
// mirroring labNodeMgmtIP's own contract.
func labVnetNodeIP(cidr string, i int) (string, error) {
	if i < 0 || i > maxLabNodeIndex {
		return "", fmt.Errorf("node index %d is out of range [0, %d]", i, maxLabNodeIndex)
	}
	return labVnetOffsetIP(cidr, 10+i)
}
