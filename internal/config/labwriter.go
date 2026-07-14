package config

import (
	"fmt"
	"os"
	"path/filepath"
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

	fmt.Fprint(&b, "# network: SDN vnet, VXLAN tag, and subnet layout for the lab.\n")
	fmt.Fprint(&b, "network:\n")
	fmt.Fprintf(&b, "  vnet_id: %s\n", yamlQuote(lab.Network.VnetID))
	fmt.Fprintf(&b, "  vnet_alias: %s\n", yamlQuote(lab.Network.VnetAlias))
	fmt.Fprintf(&b, "  vxlan_tag: %d\n", lab.Network.VxlanTag)
	fmt.Fprintf(&b, "  cidr: %s\n", yamlQuote(lab.Network.CIDR))
	fmt.Fprint(&b, "  mgmt:\n")
	fmt.Fprintf(&b, "    subnet: %s\n", yamlQuote(lab.Network.Mgmt.Subnet))
	fmt.Fprintf(&b, "    host_ip: %s\n", yamlQuote(lab.Network.Mgmt.HostIP))
	fmt.Fprintf(&b, "    gateway: %s\n", yamlQuote(lab.Network.Mgmt.Gateway))
	fmt.Fprintf(&b, "  bosh_bloc: %s\n", yamlQuote(lab.Network.BoshBloc))
	fmt.Fprintf(&b, "  mtu: %d\n\n", lab.Network.MTU)

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
	fmt.Fprintf(&b, "  ssd: %t\n\n", lab.Storage.SSD)

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
