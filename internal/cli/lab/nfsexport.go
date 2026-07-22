package lab

import (
	"fmt"
	"sort"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// This file resolves the NFS export-ownership alias feature: a lab's
// storage.nfs_export field may name ANOTHER lab whose ZFS export tree
// (tank/nfs/labs/<owner>/{images,backup}) this lab mounts instead of owning
// its own, so two (or more) clusters can mimic a client environment where
// one export is shared to every member. nfs.go's attach/detach/status verbs
// call resolveNfsExportOwner to find the owner and the full member set
// before touching any server-side state.

// nfsExportOwner is the resolved NFS export ownership for one lab: the
// export-owner lab (itself, when it sets no alias or aliases its own name),
// and every lab across the loaded config — including the owner — whose
// effective export resolves to that same owner, sorted by name for
// deterministic ACL ordering (nfsMemberMgmtCIDRs).
type nfsExportOwner struct {
	owner   *config.Lab
	members []*config.Lab
}

// resolveNfsExportOwner resolves lab's NFS export ownership against labs
// (config.ResolveLabs' full result — every OTHER configured lab must be
// visible too, since a sibling lab may alias the same owner). Three rules
// are enforced here, matching LabStorage.NFSExport's contract:
//
//  1. lab.Storage.NFSExport naming lab's own name (or being empty) is a
//     no-op: lab owns its own export tree, exactly as before this feature
//     existed.
//  2. lab.Storage.NFSExport naming a lab absent from labs is a hard error —
//     there is no dataset to alias.
//  3. The resolved owner must not itself set storage.nfs_export to a THIRD
//     lab (a chained alias): the owner must be the "root" of its own
//     export tree, so every member's server-side ensure phase converges on
//     one single dataset path.
//
// members always includes the owner itself (an owner is trivially a member
// of its own export), sorted by name so nfsMemberMgmtCIDRs' resulting rw=
// list is byte-identical regardless of which member's attach/detach
// triggered the recompute or map-iteration order.
func resolveNfsExportOwner(labs map[string]*config.Lab, lab *config.Lab) (*nfsExportOwner, error) {
	if lab == nil {
		return nil, fmt.Errorf("resolve NFS export owner: lab is nil")
	}

	ownerName := config.EffectiveNFSExport(lab)

	owner := lab
	if ownerName != lab.Name {
		o, ok := labs[ownerName]
		if !ok {
			return nil, fmt.Errorf(
				"lab %q: storage.nfs_export %q names a lab that does not exist in the loaded config; "+
					"available: %s", lab.Name, ownerName, availableLabNames(labs))
		}
		owner = o
	}

	if ownerAlias := config.EffectiveNFSExport(owner); ownerAlias != owner.Name {
		return nil, fmt.Errorf(
			"lab %q: storage.nfs_export owner %q is itself aliased to %q's export; chained nfs_export "+
				"aliases are not supported — point storage.nfs_export directly at %q's ultimate owner instead",
			lab.Name, owner.Name, ownerAlias, ownerAlias)
	}

	var members []*config.Lab
	for _, candidate := range labs {
		if config.EffectiveNFSExport(candidate) == owner.Name {
			members = append(members, candidate)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })

	return &nfsExportOwner{owner: owner, members: members}, nil
}

// nfsExportMemberNames returns members' names in order (members is already
// sorted by resolveNfsExportOwner, so this needs no sorting of its own) —
// used to render `nfs status`'s member list and detach's owner-refusal
// error.
func nfsExportMemberNames(members []*config.Lab) []string {
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.Name
	}
	return names
}

// nfsExportMembersExcluding returns members with the entry named name
// removed (at most one entry is ever removed — resolveNfsExportOwner's
// per-name loop over a map can never produce a duplicate Name), preserving
// every other entry's relative order.
func nfsExportMembersExcluding(members []*config.Lab, name string) []*config.Lab {
	out := make([]*config.Lab, 0, len(members))
	for _, m := range members {
		if m.Name != name {
			out = append(out, m)
		}
	}
	return out
}
