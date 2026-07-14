package peppi_test

import (
	"fmt"
	"testing"

	"github.com/fivetwenty-io/pmx-cli/internal/peppi"
	"github.com/stretchr/testify/require"
)

func TestGuard_ProtectedVMID(t *testing.T) {
	t.Parallel()

	for _, vmid := range []int{50000, 50001, 50010, 50020} {
		vmid := vmid
		t.Run(fmt.Sprintf("vmid=%d", vmid), func(t *testing.T) {
			t.Parallel()
			err := peppi.Guard(peppi.Target{VMID: vmid})
			require.Error(t, err)
			require.Contains(t, err.Error(), fmt.Sprintf("%d", vmid))
		})
	}
}

// TestGuard_ProtectedNamePattern verifies that a protected substring is
// refused regardless of which Names slot it appears in (vnet, pool,
// storage id, zone, vm name), regardless of surrounding characters, and
// regardless of case.
func TestGuard_ProtectedNamePattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		names   []string
		matched string
	}{
		{"vnet id exact", []string{"peppivn0"}, "peppivn0"},
		{"pool name substring", []string{"pool-peppiprd-lab"}, "pool-peppiprd-lab"},
		{"storage id substring", []string{"tank-peppiprd-data"}, "tank-peppiprd-data"},
		{"dns zone substring", []string{"peppiprd.internal"}, "peppiprd.internal"},
		{"vm name substring", []string{"web01-peppivn0"}, "web01-peppivn0"},
		{"match in second position", []string{"clean-name", "peppiprd"}, "peppiprd"},
		{"match in third position", []string{"clean-pool", "clean-vnet", "peppivn0-uplink"}, "peppivn0-uplink"},
		{"case-insensitive upper", []string{"PEPPIPRD"}, "PEPPIPRD"},
		{"case-insensitive mixed", []string{"Peppiprd0"}, "Peppiprd0"},
		{"case-insensitive mixed vnet", []string{"PeppiVn0-storage"}, "PeppiVn0-storage"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := peppi.Guard(peppi.Target{Names: tc.names})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.matched)
		})
	}
}

func TestGuard_CleanTargetPasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target peppi.Target
	}{
		{
			"zero vmid with clean names",
			peppi.Target{VMID: 0, Names: []string{"lab-wayne", "vnet-lab0", "tank-lab-wayne", "lab.internal", "web01"}},
		},
		{
			"empty target",
			peppi.Target{},
		},
		{
			"unrelated vmid with clean names",
			peppi.Target{VMID: 100, Names: []string{"lab-drgao"}},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, peppi.Guard(tc.target))
		})
	}
}
