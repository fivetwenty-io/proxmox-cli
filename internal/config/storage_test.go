package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// --- ValidateStorage -------------------------------------------------------

func TestValidateStorage_OSDDisks(t *testing.T) {
	cases := []struct {
		name    string
		storage config.LabStorage
		wantLen int
		contain string
	}{
		{"nil osd_disks is valid", config.LabStorage{}, 0, ""},
		{
			"count with size is valid",
			config.LabStorage{Controller: "virtio-scsi-single", OSDDisks: &config.LabOSDDisks{Count: 2, SizeGB: 100}},
			0, "",
		},
		{
			"negative count",
			config.LabStorage{OSDDisks: &config.LabOSDDisks{Count: -1, SizeGB: 100}},
			1, "osd_disks.count",
		},
		{
			"count above max",
			config.LabStorage{Controller: "virtio-scsi-single", OSDDisks: &config.LabOSDDisks{Count: 9, SizeGB: 100}},
			1, "at most 8",
		},
		{
			"count without size",
			config.LabStorage{Controller: "virtio-scsi-single", OSDDisks: &config.LabOSDDisks{Count: 2}},
			1, "osd_disks.size_gb",
		},
		{
			"size without count",
			config.LabStorage{OSDDisks: &config.LabOSDDisks{SizeGB: 100}},
			1, "osd_disks.count",
		},
		{
			"count requires virtio-scsi-single controller",
			config.LabStorage{OSDDisks: &config.LabOSDDisks{Count: 2, SizeGB: 100}},
			1, "virtio-scsi-single",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := config.ValidateStorage("ceph", tc.storage)
			require.Len(t, issues, tc.wantLen)
			if tc.contain != "" {
				require.Contains(t, issues[0], tc.contain)
				require.Contains(t, issues[0], `lab "ceph"`)
			}
		})
	}
}

// --- EffectiveNodeSizing (OSD disk overrides) -------------------------------

func TestEffectiveNodeSizing_OSDDiskOverrides(t *testing.T) {
	lab := &config.Lab{
		Name:    "ceph",
		Storage: config.LabStorage{OSDDisks: &config.LabOSDDisks{Count: 2, SizeGB: 100}},
		Topology: config.LabTopology{
			Nodes: 3,
			NodeOverrides: map[int]config.LabNodeOverride{
				1: {OSDDiskCount: 1},
				2: {OSDDiskGB: 50},
			},
		},
	}
	_, st0 := config.EffectiveNodeSizing(lab, 0)
	require.Equal(t, 2, config.OSDDiskCount(st0))
	require.Equal(t, 100, config.OSDDiskSizeGB(st0))
	_, st1 := config.EffectiveNodeSizing(lab, 1)
	require.Equal(t, 1, config.OSDDiskCount(st1))
	require.Equal(t, 100, config.OSDDiskSizeGB(st1))
	_, st2 := config.EffectiveNodeSizing(lab, 2)
	require.Equal(t, 50, config.OSDDiskSizeGB(st2))
	// The override must not alias the lab-level pointer.
	require.Equal(t, 2, config.OSDDiskCount(lab.Storage))
	require.Equal(t, 100, config.OSDDiskSizeGB(lab.Storage))
}
