package lab

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/peppi"
)

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
