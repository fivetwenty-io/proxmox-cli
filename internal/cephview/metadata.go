package cephview

import (
	"fmt"
	"strings"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// clusterMetadata is GET /cluster/ceph/metadata: one entry per daemon, keyed
// by "name@host", each carrying forty or more fields describing the machine
// it runs on. Rendered generically that is a wall; what an operator reads it
// for is which daemons exist, where, and on what version.
type clusterMetadata struct {
	Mon  map[string]daemonMetadata `json:"mon"`
	Mgr  map[string]daemonMetadata `json:"mgr"`
	Mds  map[string]daemonMetadata `json:"mds"`
	Osd  []daemonMetadata          `json:"osd"`
	Node map[string]struct {
		Buildcommit string `json:"buildcommit"`
		Version     struct {
			Str string `json:"str"`
		} `json:"version"`
	} `json:"node"`
}

// daemonMetadata is the handful of fields every Ceph daemon reports, whatever
// its kind.
type daemonMetadata struct {
	ID                pve.PVEInt `json:"id"`
	Name              string     `json:"name"`
	Hostname          string     `json:"hostname"`
	Host              string     `json:"host"`
	CephVersion       string     `json:"ceph_version"`
	CephVersionShort  string     `json:"ceph_version_short"`
	CephRelease       string     `json:"ceph_release"`
	DistroDescription string     `json:"distro_description"`
	KernelVersion     string     `json:"kernel_version"`
	Arch              string     `json:"arch"`
}

// clusterMetadataHeaders names one daemon per row.
var clusterMetadataHeaders = []string{
	"DAEMON", "NAME", "HOST", "VERSION", "RELEASE", "OS", "KERNEL", "ARCH",
}

// ClusterMetadata renders the per-daemon metadata as one row per daemon,
// grouped by kind. The full forty-field record per daemon stays in -o json.
func ClusterMetadata(resp any) (output.Result, error) {
	var meta clusterMetadata
	payload, err := decode(resp, &meta)
	if err != nil {
		return output.Result{}, err
	}

	var rows [][]string
	for _, group := range []struct {
		kind    string
		daemons map[string]daemonMetadata
	}{
		{"mon", meta.Mon},
		{"mgr", meta.Mgr},
		{"mds", meta.Mds},
	} {
		for _, key := range sortedKeys(group.daemons) {
			rows = append(rows, daemonRow(group.kind, key, group.daemons[key]))
		}
	}
	for _, d := range meta.Osd {
		rows = append(rows, daemonRow("osd", fmt.Sprintf("osd.%d", pveInt(d.ID)), d))
	}
	for _, name := range sortedKeys(meta.Node) {
		n := meta.Node[name]
		rows = append(rows, []string{"node", name, name, n.Version.Str, "", "", "", ""})
	}

	return output.Result{Headers: clusterMetadataHeaders, Rows: rows, Raw: payload}, nil
}

// daemonRow renders one daemon. The map key is "name@host", which repeats the
// host already in the record, so the name is taken from the key's first half
// when the record itself does not carry one.
func daemonRow(kind, key string, d daemonMetadata) []string {
	name := d.Name
	if name == "" {
		name, _, _ = strings.Cut(key, "@")
	}
	host := firstNonEmpty(d.Hostname, d.Host)
	return []string{
		kind,
		name,
		host,
		shortVersion(d.CephVersion, d.CephVersionShort),
		d.CephRelease,
		d.DistroDescription,
		d.KernelVersion,
		d.Arch,
	}
}

// osdMetadata is GET /nodes/{node}/ceph/osd/{osdid}/metadata: one record for
// the daemon and one per backing device.
type osdMetadata struct {
	Osd struct {
		ID             pve.PVEInt `json:"id"`
		Hostname       string     `json:"hostname"`
		Version        string     `json:"version"`
		OsdObjectstore string     `json:"osd_objectstore"`
		OsdData        string     `json:"osd_data"`
		MemUsage       pve.PVEInt `json:"mem_usage"`
		Pid            pve.PVEInt `json:"pid"`
		Encrypted      pve.PVEInt `json:"encrypted"`
		FrontAddr      string     `json:"front_addr"`
		BackAddr       string     `json:"back_addr"`
	} `json:"osd"`
	Devices []struct {
		Device         string     `json:"device"`
		DevNode        string     `json:"dev_node"`
		PhysicalDevice string     `json:"physical_device"`
		Type           string     `json:"type"`
		Size           pve.PVEInt `json:"size"`
		SupportDiscard bool       `json:"support_discard"`
	} `json:"devices"`
}

// OSDMetadata renders one OSD's metadata: the daemon on top, then a row per
// backing device, which is the pairing the command is asked for and the one a
// nested object hid completely.
func OSDMetadata(resp any) (output.Result, error) {
	var meta osdMetadata
	payload, err := decode(resp, &meta)
	if err != nil {
		return output.Result{}, err
	}

	rows := [][]string{
		{"osd", fmt.Sprintf("osd.%d", pveInt(meta.Osd.ID))},
		{"host", meta.Osd.Hostname},
		{"version", meta.Osd.Version},
		{"objectstore", meta.Osd.OsdObjectstore},
		{"data", meta.Osd.OsdData},
		{"encrypted", yesNo(pveInt(meta.Osd.Encrypted) != 0)},
		{"pid", fmt.Sprintf("%d", pveInt(meta.Osd.Pid))},
		{"memory", bytesCell(pveInt(meta.Osd.MemUsage))},
		{"front addr", meta.Osd.FrontAddr},
		{"back addr", meta.Osd.BackAddr},
	}
	for _, d := range meta.Devices {
		rows = append(rows, []string{
			"device " + d.Device,
			fmt.Sprintf("%s (%s, %s, %s, discard %s)",
				d.DevNode, d.PhysicalDevice, d.Type,
				bytesCell(pveInt(d.Size)), yesNo(d.SupportDiscard)),
		})
	}

	return output.Result{Headers: fieldHeaders, Rows: rows, Raw: payload}, nil
}

// yesNo renders a flag the way the rest of pmx renders one.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
