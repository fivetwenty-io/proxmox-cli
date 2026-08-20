package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// yamlQuote renders s as a YAML double-quoted scalar. fmt's %q is close but
// not identical: Go string-literal escaping emits sequences (\x80 for a raw
// invalid byte, Go-style rune forms) whose meaning in YAML's double-quoted
// style differs or does not exist, so a pathological value could parse back
// differently than written. This escaper emits only YAML-defined escapes:
// printable runes pass through raw; C0/C1 controls, DEL, and YAML's special
// line/paragraph separators are escaped by code point.
func yamlQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case 0x85:
			b.WriteString(`\N`)
		case 0x2028:
			b.WriteString(`\L`)
		case 0x2029:
			b.WriteString(`\P`)
		case utf8.RuneError:
			// An invalid UTF-8 byte decodes to RuneError; YAML's double-quoted
			// style cannot carry the raw byte, so it round-trips as U+FFFD.
			b.WriteString(`�`)
		default:
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// LabFileTemplate renders lab as a commented, human-editable YAML document in
// the bare single-lab form ResolveLabs parses via loadLabFile: the returned
// bytes ARE the Lab document (top-level fields), never wrapped in a `labs:`
// map. A nil lab renders a template of a zero-value Lab rather than panicking.
//
// The template never contains a login secret: Lab carries no such field, and
// no comment in this function may reference one either — a lab's owner
// secret lives only in Config.DefaultUserPassword (config.yml), never in a
// per-lab file that may be more widely shared or committed.
func LabFileTemplate(lab *Lab) []byte {
	if lab == nil {
		lab = &Lab{}
	}

	var b strings.Builder

	fmt.Fprintf(&b, "# Lab environment: %s.\n", lab.Name)
	fmt.Fprint(&b, "# Loaded via labs_dir/include and merged into the CLI's resolved lab set.\n")
	fmt.Fprint(&b, "# Edit freely; re-run `pmx lab config show` to confirm it still resolves.\n")
	fmt.Fprint(&b, "# This file never carries the owner's login secret: that is set once, as\n")
	fmt.Fprint(&b, "# a top-level secret field in config.yml, never per-lab.\n\n")

	fmt.Fprintf(&b, "name: %s\n", yamlQuote(lab.Name))
	fmt.Fprintf(&b, "mode: %s\n", yamlQuote(lab.Mode))
	fmt.Fprintf(&b, "owner: %s\n\n", yamlQuote(lab.Owner))

	fmt.Fprint(&b, "# network: SDN zone, vnet, vnet tag, and subnet layout for the lab.\n")
	fmt.Fprint(&b, "network:\n")
	fmt.Fprintf(&b, "  vnet_id: %s\n", yamlQuote(lab.Network.VnetID))
	fmt.Fprintf(&b, "  vnet_alias: %s\n", yamlQuote(lab.Network.VnetAlias))
	appendLabZoneBlock(&b, lab.Network)
	fmt.Fprintf(&b, "  vxlan_tag: %d\n", lab.Network.VxlanTag)
	fmt.Fprintf(&b, "  cidr: %s\n", yamlQuote(lab.Network.CIDR))
	appendLabIPv6Block(&b, lab.Network)
	fmt.Fprint(&b, "  mgmt:\n")
	fmt.Fprintf(&b, "    subnet: %s\n", yamlQuote(lab.Network.Mgmt.Subnet))
	fmt.Fprintf(&b, "    host_ip: %s\n", yamlQuote(lab.Network.Mgmt.HostIP))
	fmt.Fprintf(&b, "    gateway: %s\n", yamlQuote(lab.Network.Mgmt.Gateway))
	fmt.Fprintf(&b, "  bosh_bloc: %s\n", yamlQuote(lab.Network.BoshBloc))
	fmt.Fprintf(&b, "  mtu: %d\n", lab.Network.MTU)
	appendLabVnetsBlock(&b, lab.Network.Vnets)
	appendLabHostNICsBlock(&b, lab.Network.HostNICs)
	appendLabNestedNetworkBlock(&b, lab.Network.NestedNetwork)
	fmt.Fprint(&b, "\n")

	fmt.Fprint(&b, "# compute: CPU and memory sizing for the lab's VM.\n")
	fmt.Fprint(&b, "compute:\n")
	fmt.Fprintf(&b, "  vcpu: %d\n", lab.Compute.VCPU)
	fmt.Fprintf(&b, "  cpu_type: %s\n", yamlQuote(lab.Compute.CPUType))
	fmt.Fprintf(&b, "  numa: %t\n", lab.Compute.NUMA)
	fmt.Fprintf(&b, "  machine: %s\n", yamlQuote(lab.Compute.Machine))
	fmt.Fprintf(&b, "  firmware: %s\n", yamlQuote(lab.Compute.Firmware))
	fmt.Fprint(&b, "  memory:\n")
	fmt.Fprintf(&b, "    min_gb: %d\n", lab.Compute.Memory.MinGB)
	fmt.Fprintf(&b, "    max_gb: %d\n\n", lab.Compute.Memory.MaxGB)

	fmt.Fprint(&b, "# storage: ZFS pool and disk sizing for the lab's VM.\n")
	fmt.Fprint(&b, "storage:\n")
	fmt.Fprintf(&b, "  pool: %s\n", yamlQuote(lab.Storage.Pool))
	fmt.Fprintf(&b, "  os_disk_gb: %d\n", lab.Storage.OSDiskGB)
	fmt.Fprintf(&b, "  data_disk_gb: %d\n", lab.Storage.DataDiskGB)
	fmt.Fprintf(&b, "  refquota_gb: %d\n", lab.Storage.RefquotaGB)
	fmt.Fprintf(&b, "  controller: %s\n", yamlQuote(lab.Storage.Controller))
	fmt.Fprintf(&b, "  iothread: %t\n", lab.Storage.IOThread)
	fmt.Fprintf(&b, "  discard: %t\n", lab.Storage.Discard)
	fmt.Fprintf(&b, "  ssd: %t\n", lab.Storage.SSD)
	appendLabOSDDisksBlock(&b, lab.Storage.OSDDisks)
	appendLabNFSQuotaLine(&b, lab.Storage.NFSQuotaGB)
	fmt.Fprint(&b, "\n")

	appendLabTopologyBlock(&b, lab.Topology)

	fmt.Fprint(&b, "# dns: DNS zone associated with the lab.\n")
	fmt.Fprint(&b, "dns:\n")
	fmt.Fprintf(&b, "  zone: %s\n\n", yamlQuote(lab.DNS.Zone))

	fmt.Fprint(&b, "# provisioning: how the lab's guest is provisioned.\n")
	fmt.Fprint(&b, "provisioning:\n")
	fmt.Fprintf(&b, "  mode: %s\n", yamlQuote(lab.Provisioning.Mode))
	fmt.Fprintf(&b, "  answer_template: %s\n", yamlQuote(lab.Provisioning.AnswerTemplate))
	if len(lab.Provisioning.SSHKeys) == 0 {
		fmt.Fprint(&b, "  ssh_keys: []\n\n")
	} else {
		fmt.Fprint(&b, "  ssh_keys:\n")
		for _, key := range lab.Provisioning.SSHKeys {
			fmt.Fprintf(&b, "    - %s\n", yamlQuote(key))
		}
		fmt.Fprint(&b, "\n")
	}

	fmt.Fprint(&b, "# access: pve realm/pool/role granted to the lab owner.\n")
	fmt.Fprint(&b, "access:\n")
	fmt.Fprintf(&b, "  realm: %s\n", yamlQuote(lab.Access.Realm))
	fmt.Fprintf(&b, "  pool: %s\n", yamlQuote(lab.Access.Pool))
	fmt.Fprintf(&b, "  role: %s\n", yamlQuote(lab.Access.Role))

	return []byte(b.String())
}

// appendLabNFSQuotaLine documents storage.nfs_quota_gb with a short comment,
// unconditionally, then renders the key only when set (> 0) — the same
// comment-always/key-only-when-set convention appendLabVnetsBlock uses, so a
// lab that leaves it unset round-trips through ResolveLabs at its true zero
// value rather than an explicit "nfs_quota_gb: 0" that would misread as "no
// quota" instead of "use the default".
func appendLabNFSQuotaLine(b *strings.Builder, nfsQuotaGB int) {
	fmt.Fprint(b, "  # nfs_quota_gb: ZFS 'quota' pmx lab nfs attach's server-side\n")
	fmt.Fprint(b, "  # ensure phase sets on this lab's NFS parent dataset\n")
	fmt.Fprint(b, "  # (tank/nfs/labs/<lab>). Omitted means the default (200G).\n")
	if nfsQuotaGB > 0 {
		fmt.Fprintf(b, "  nfs_quota_gb: %d\n", nfsQuotaGB)
	}
}

// appendLabZoneBlock documents network.zone_name, network.zone_type, and
// network.zone_peers with a short comment, unconditionally, then renders
// each key only when set — the same comment-always/key-only-when-set
// convention appendLabVnetsBlock uses, so a lab that leaves the zone at its
// default round-trips through ResolveLabs at its true zero value rather than
// pinning "labs"/"simple" into the file. The rendered defaults quoted in the
// comment are DefaultZoneName/DefaultZoneType, the same constants
// EffectiveZoneName/EffectiveZoneType apply at resolve time.
func appendLabZoneBlock(b *strings.Builder, net LabNetwork) {
	fmt.Fprintf(b, "  # zone_name: SDN zone this lab's vnet joins. Omitted means %q,\n", DefaultZoneName)
	fmt.Fprint(b, "  # the shared outer zone; labs may sit in different zones.\n")
	fmt.Fprint(b, "  # zone_type: PVE zone plugin used when that zone is created (only\n")
	fmt.Fprintf(b, "  # consulted when it does not exist yet). Omitted means %q;\n", DefaultZoneType)
	fmt.Fprint(b, "  # \"vxlan\" is the other type the lab verbs model, and it takes\n")
	fmt.Fprint(b, "  # zone_peers, the underlay peer list (ignored for a simple zone,\n")
	fmt.Fprint(b, "  # which also has no vnet tag: vxlan_tag below is skipped there).\n")
	if net.ZoneName != "" {
		fmt.Fprintf(b, "  zone_name: %s\n", yamlQuote(net.ZoneName))
	}
	if net.ZoneType != "" {
		fmt.Fprintf(b, "  zone_type: %s\n", yamlQuote(net.ZoneType))
	}
	if net.ZonePeers != "" {
		fmt.Fprintf(b, "  zone_peers: %s\n", yamlQuote(net.ZonePeers))
	}
}

// appendLabIPv6Block documents network.ipv6 and network.cidr6 with a short
// comment, unconditionally, then renders each key only when set — the same
// comment-always/key-only-when-set convention appendLabZoneBlock uses, so a
// lab that leaves both at their defaults (IPv6 on, ULA auto-derived)
// round-trips through ResolveLabs at its true zero value.
func appendLabIPv6Block(b *strings.Builder, net LabNetwork) {
	fmt.Fprint(b, "  # ipv6: labs are dual-stack by default — every addressed vnet gets\n")
	fmt.Fprint(b, "  # an IPv6 subnet alongside its IPv4 one, and nodes/QDevice get\n")
	fmt.Fprint(b, "  # management IPv6 addresses. Set to false to keep this lab\n")
	fmt.Fprint(b, "  # IPv4-only.\n")
	fmt.Fprint(b, "  # cidr6: the lab's IPv6 block (/48 or wider). Omitted means a\n")
	fmt.Fprint(b, "  # stable RFC 4193 ULA /48 derived from cidr above, unique per lab\n")
	fmt.Fprint(b, "  # with zero config.\n")
	fmt.Fprint(b, "  # snat6: masquerade the lab's IPv6 subnets for egress. Off by\n")
	fmt.Fprint(b, "  # default (ULA-internal); only honored on a simple zone.\n")
	if net.IPv6 != nil {
		fmt.Fprintf(b, "  ipv6: %t\n", *net.IPv6)
	}
	if net.CIDR6 != "" {
		fmt.Fprintf(b, "  cidr6: %s\n", yamlQuote(net.CIDR6))
	}
	if net.Snat6 {
		fmt.Fprint(b, "  snat6: true\n")
	}
}

// appendLabOSDDisksBlock writes the storage.osd_disks sub-block: the
// explanatory comment always, the keys only when configured, so an unset
// value round-trips as absent (see the goccy empty-collection caveat above).
func appendLabOSDDisksBlock(b *strings.Builder, d *LabOSDDisks) {
	fmt.Fprint(b, "  # storage.osd_disks: extra whole-raw-device disks per node (scsi2..) for nested Ceph OSDs.\n")
	if d == nil {
		return
	}
	fmt.Fprint(b, "  osd_disks:\n")
	fmt.Fprintf(b, "    count: %d\n", d.Count)
	fmt.Fprintf(b, "    size_gb: %d\n", d.SizeGB)
}

// appendLabTopologyBlock writes the topology block: the comment always, the
// keys only when set, node_overrides in ascending node-index order (map
// iteration order is nondeterministic, so the keys are sorted before
// rendering to keep the output stable across runs).
func appendLabTopologyBlock(b *strings.Builder, t LabTopology) {
	fmt.Fprint(b, "# topology: multi-node lab shape — node count, qdevice policy, per-node sizing overrides.\n")
	if t.Nodes == 0 && t.Qdevice == "" && len(t.NodeOverrides) == 0 {
		fmt.Fprint(b, "\n")
		return
	}
	fmt.Fprint(b, "topology:\n")
	if t.Nodes != 0 {
		fmt.Fprintf(b, "  nodes: %d\n", t.Nodes)
	}
	if t.Qdevice != "" {
		fmt.Fprintf(b, "  qdevice: %s\n", yamlQuote(t.Qdevice))
	}
	if len(t.NodeOverrides) > 0 {
		fmt.Fprint(b, "  node_overrides:\n")
		idxs := make([]int, 0, len(t.NodeOverrides))
		for i := range t.NodeOverrides {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		for _, i := range idxs {
			o := t.NodeOverrides[i]
			fmt.Fprintf(b, "    %d:\n", i)
			appendIntWhenSet(b, "      vcpu", o.VCPU)
			appendIntWhenSet(b, "      memory_min_gb", o.MemoryMinGB)
			appendIntWhenSet(b, "      memory_max_gb", o.MemoryMaxGB)
			appendIntWhenSet(b, "      os_disk_gb", o.OSDiskGB)
			appendIntWhenSet(b, "      data_disk_gb", o.DataDiskGB)
			appendIntWhenSet(b, "      osd_disk_count", o.OSDDiskCount)
			appendIntWhenSet(b, "      osd_disk_gb", o.OSDDiskGB)
		}
	}
	fmt.Fprint(b, "\n")
}

// appendIntWhenSet writes "<key>: <v>\n" only when v is non-zero, matching
// the block-helper convention every other optional numeric field in this
// file follows: an unset field round-trips as absent, not as an explicit
// zero that would misread as "set to zero" on reload.
func appendIntWhenSet(b *strings.Builder, key string, v int) {
	if v != 0 {
		fmt.Fprintf(b, "%s: %d\n", key, v)
	}
}

// appendLabVnetsBlock documents network.vnets with a short comment,
// unconditionally, then renders the list only when non-empty. Rendering
// nothing but the comment for an empty/nil vnets keeps the field's
// zero-value round trip through ResolveLabs a nil slice rather than an
// empty-but-non-nil one — goccy/go-yaml unmarshals an explicit `vnets: []`
// to a non-nil empty slice, which would fail a struct-equality comparison
// against a Lab built without setting the field at all.
func appendLabVnetsBlock(b *strings.Builder, vnets []LabVnet) {
	fmt.Fprint(b, "  # vnets: additional outer SDN vnets beyond the primary vnet_id/cidr\n")
	fmt.Fprint(b, "  # pair, e.g. separate storage and workload L2 domains for a\n")
	fmt.Fprint(b, "  # multi-bond nested topology. Omitted entirely means today's\n")
	fmt.Fprint(b, "  # single-vnet shape. An addressed entry also gets an auto-carved\n")
	fmt.Fprint(b, "  # IPv6 /64 (unless ipv6: false); set cidr6 on an entry (/112 or\n")
	fmt.Fprint(b, "  # wider) to override its IPv6 subnet.\n")
	if len(vnets) == 0 {
		return
	}
	fmt.Fprint(b, "  vnets:\n")
	for _, v := range vnets {
		fmt.Fprintf(b, "    - id: %s\n", yamlQuote(v.ID))
		if v.Alias != "" {
			fmt.Fprintf(b, "      alias: %s\n", yamlQuote(v.Alias))
		}
		fmt.Fprintf(b, "      tag: %d\n", v.Tag)
		if v.CIDR != "" {
			fmt.Fprintf(b, "      cidr: %s\n", yamlQuote(v.CIDR))
		}
		if v.CIDR6 != "" {
			fmt.Fprintf(b, "      cidr6: %s\n", yamlQuote(v.CIDR6))
		}
		if v.Gateway != "" {
			fmt.Fprintf(b, "      gateway: %s\n", yamlQuote(v.Gateway))
		}
		if v.Purpose != "" {
			fmt.Fprintf(b, "      purpose: %s\n", yamlQuote(v.Purpose))
		}
	}
}

// appendLabHostNICsBlock documents network.host_nics the same way
// appendLabVnetsBlock documents network.vnets: comment always, list only
// when non-empty, for the same nil-vs-empty-slice round-trip reason.
func appendLabHostNICsBlock(b *strings.Builder, nics []LabHostNIC) {
	fmt.Fprint(b, "  # host_nics: additional outer qm NICs (net1..netN) beyond the\n")
	fmt.Fprint(b, "  # always-present net0; each vnet_id must resolve to the primary\n")
	fmt.Fprint(b, "  # vnet_id above or one of vnets[].id. Omitted entirely means\n")
	fmt.Fprint(b, "  # today's single-NIC shape.\n")
	if len(nics) == 0 {
		return
	}
	fmt.Fprint(b, "  host_nics:\n")
	for _, hn := range nics {
		fmt.Fprintf(b, "    - index: %d\n", hn.Index)
		fmt.Fprintf(b, "      vnet_id: %s\n", yamlQuote(hn.VnetID))
		if hn.MTU != 0 {
			fmt.Fprintf(b, "      mtu: %d\n", hn.MTU)
		}
	}
}

// appendLabNestedNetworkBlock documents network.nested_network the same way
// appendLabVnetsBlock documents network.vnets: comment always, the
// nested_network key (and its bonds/vlan_zone sub-keys) only when at least
// one of Bonds or VlanZone is set, for the same nil-vs-empty-slice
// round-trip reason. Bonds and VlanZone are rendered independently — a lab
// may legitimately set one without the other while iterating on its plan.
func appendLabNestedNetworkBlock(b *strings.Builder, nn LabNestedNetwork) {
	fmt.Fprint(b, "  # nested_network: in-guest bonding/bridging plus an inner SDN\n")
	fmt.Fprint(b, "  # vlan zone for the lab's own nested PVE node OS. Omitted\n")
	fmt.Fprint(b, "  # entirely means no bonding: nested nodes use their NICs\n")
	fmt.Fprint(b, "  # unbonded, today's shape.\n")
	if len(nn.Bonds) == 0 && nn.VlanZone == nil {
		return
	}
	fmt.Fprint(b, "  nested_network:\n")

	if len(nn.Bonds) > 0 {
		fmt.Fprint(b, "    bonds:\n")
		for _, bond := range nn.Bonds {
			fmt.Fprintf(b, "      - name: %s\n", yamlQuote(bond.Name))
			fmt.Fprint(b, "        nics:\n")
			for _, nic := range bond.NICs {
				fmt.Fprintf(b, "          - %s\n", yamlQuote(nic))
			}
			fmt.Fprintf(b, "        mode: %s\n", yamlQuote(bond.Mode))
			if bond.Primary != "" {
				fmt.Fprintf(b, "        primary: %s\n", yamlQuote(bond.Primary))
			}
			fmt.Fprintf(b, "        bridge: %s\n", yamlQuote(bond.Bridge))
			if bond.VlanAware {
				fmt.Fprintf(b, "        vlan_aware: %t\n", bond.VlanAware)
			}
		}
	}

	if nn.VlanZone != nil {
		fmt.Fprint(b, "    vlan_zone:\n")
		fmt.Fprintf(b, "      bridge: %s\n", yamlQuote(nn.VlanZone.Bridge))
		fmt.Fprintf(b, "      zone_name: %s\n", yamlQuote(nn.VlanZone.ZoneName))
		if len(nn.VlanZone.Vnets) > 0 {
			fmt.Fprint(b, "      vnets:\n")
			for _, vv := range nn.VlanZone.Vnets {
				fmt.Fprintf(b, "        - id: %s\n", yamlQuote(vv.ID))
				if vv.Alias != "" {
					fmt.Fprintf(b, "          alias: %s\n", yamlQuote(vv.Alias))
				}
				fmt.Fprintf(b, "          tag: %d\n", vv.Tag)
				fmt.Fprintf(b, "          cidr: %s\n", yamlQuote(vv.CIDR))
				if vv.Gateway != "" {
					fmt.Fprintf(b, "          gateway: %s\n", yamlQuote(vv.Gateway))
				}
			}
		}
	}
}

// WriteLabFile writes lab as a commented YAML document to <dir>/<name>.yaml
// (name from lab.Name) using WriteRaw, so the file lands atomically at mode
// 0600 with its parent directory created 0700 if missing. It refuses to
// overwrite an existing file unless force is true. Returns the written path.
//
// Errors: lab nil, lab.Name empty, lab.Name containing a path separator or
// "." / ".." (which would let a caller-supplied name escape dir), dir empty,
// the target already existing without force, or any WriteRaw failure
// (directory creation, permission, or atomic-rename failure).
func WriteLabFile(dir string, lab *Lab, force bool) (string, error) {
	if lab == nil {
		return "", fmt.Errorf("write lab file: lab is nil")
	}

	if dir == "" {
		return "", fmt.Errorf("write lab file: dir is required")
	}

	name := lab.Name
	if name == "" {
		return "", fmt.Errorf("write lab file: lab.Name is required")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("write lab file: lab name %q is not a valid file name", name)
	}
	// A control character (a newline in particular) would both make a
	// pathological filename and break out of the template's header comment
	// line, corrupting the document.
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", fmt.Errorf("write lab file: lab name %q contains a control character", name)
	}

	path := filepath.Join(dir, name+".yaml")

	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("lab file %s already exists; pass force to overwrite it", path)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat lab file %s: %w", path, err)
		}
	}

	if err := WriteRaw(path, LabFileTemplate(lab), force); err != nil {
		return "", fmt.Errorf("write lab file %s: %w", path, err)
	}

	return path, nil
}
