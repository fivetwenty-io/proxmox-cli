package lab

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// This file implements the host-firewall half of `pmx lab nfs attach`'s
// server-side ensure phase: the per-lab ACCEPT rules on the outer PVE node
// hosting tank/nfs, without which every host-terminated NFS packet from the
// lab's mgmt subnet is dropped (the node firewall runs an explicit
// allow-list with default-drop input policy — lab repo
// docs/spec/nfs-service.md). The rules mirror scripts/60-nfs-service's
// `firewall` group byte-for-byte — same ports, same comment key, same
// idempotency-by-comment convention — so a lab attached through pmx and one
// built by the script converge to identical rule state, and neither path
// ever duplicates the other's rules.
//
// The firewall ACL is the second half of a deliberately-doubled enforcement:
// nfsserver.go's sharenfs `rw=@<mgmt-/24>` clause scopes the export, and
// these rules scope the host's network path, so a misconfiguration in either
// layer is still caught by the other. Attach has always ensured the sharenfs
// half unconditionally; ensuring only one half in practice left every first
// attach dead on arrival (working ssh, hanging mount) until the rules were
// added by hand.

// nfsFirewallPorts are the two host-firewall ports the shared NFS service
// needs open per lab mgmt subnet: 2049/tcp (NFSv4 itself) and 111/tcp (the
// portmapper — PVE's NFSv4 storage online-probe is `rpcinfo -T tcp <server>
// nfs 4`, and with 111 silently dropped each probe hangs ~10s, stalling
// pvestatd/pvesm status cycles badly enough to trip pveproxy's client
// timeout mid-upload; see scripts/60-nfs-service's group_firewall header).
var nfsFirewallPorts = []struct {
	port string
	svc  string
}{
	{port: "2049", svc: "NFS"},
	{port: "111", svc: "rpcbind"},
}

// nfsFirewallRuleComment renders the comment key identifying one lab's
// per-port firewall rule — byte-identical to scripts/60-nfs-service's
// group_firewall comment, which is the idempotency key both paths match
// rules on (never position or field-by-field comparison).
func nfsFirewallRuleComment(labName, svc, port string) string {
	return fmt.Sprintf("tank/nfs: %s mgmt subnet -> %s (%s/tcp)", labName, svc, port)
}

// nfsFirewallDryRunSteps renders the firewall-ensure sub-phase as STEP/STATUS
// preview rows, computable entirely from plan (no API call, matching the
// dry-run convention of nfsServerDryRunSteps: preview without probing).
func nfsFirewallDryRunSteps(plan nfsServerEnsurePlan) [][]string {
	rows := make([][]string, 0, len(nfsFirewallPorts))
	for _, p := range nfsFirewallPorts {
		rows = append(rows, []string{
			fmt.Sprintf("node firewall: ACCEPT tcp dport %s from %s (%s)",
				p.port, plan.mgmtCIDR, nfsFirewallRuleComment(plan.labName, p.svc, p.port)),
			"would run (if missing)",
		})
	}
	return rows
}

// nfsFirewallRuleView is the subset of a node firewall rule this phase reads
// from the list response: the comment (the idempotency key), the enable flag
// (PVE's API default for an omitted `enable` is 0, so a rule can exist yet
// be a disabled no-op — see the explicit Enable below), and the position
// (only to render an actionable remediation command).
type nfsFirewallRuleView struct {
	Comment string `json:"comment"`
	Enable  *int64 `json:"enable"`
	Pos     *int64 `json:"pos"`
}

// nfsEnsureFirewallRules ensures the outer node's host firewall carries an
// enabled ACCEPT rule for each of nfsFirewallPorts scoped to plan's mgmt
// /24, via the PVE firewall API against deps.Node (the same node the ZFS
// ensure phase just ran against). Rules are matched by comment: present and
// enabled is a skip, absent is a create, and present-but-DISABLED is a loud
// failure — silently skipping a disabled rule would recreate the exact
// dead-mount symptom this phase exists to kill, and this package never
// mutates a rule it did not create (the operator may have disabled it
// deliberately). Returns one STEP/STATUS row per port, in nfsFirewallPorts
// order.
func nfsEnsureFirewallRules(ctx context.Context, deps *cli.Deps, plan nfsServerEnsurePlan) ([][]string, error) {
	resp, err := deps.API.Nodes.ListFirewallRules(ctx, deps.Node)
	if err != nil {
		return nil, fmt.Errorf("list host firewall rules on node %q: %w", deps.Node, err)
	}

	byComment := make(map[string]nfsFirewallRuleView)
	if resp != nil {
		for _, raw := range *resp {
			var v nfsFirewallRuleView
			if uerr := json.Unmarshal(raw, &v); uerr != nil {
				return nil, fmt.Errorf("decode host firewall rule on node %q: %w", deps.Node, uerr)
			}
			if v.Comment != "" {
				byComment[v.Comment] = v
			}
		}
	}

	rows := make([][]string, 0, len(nfsFirewallPorts))
	for _, p := range nfsFirewallPorts {
		comment := nfsFirewallRuleComment(plan.labName, p.svc, p.port)

		if v, ok := byComment[comment]; ok {
			if v.Enable == nil || *v.Enable != 1 {
				pos := "<pos>"
				if v.Pos != nil {
					pos = fmt.Sprintf("%d", *v.Pos)
				}
				return nil, fmt.Errorf(
					"host firewall rule %q on node %q exists but is disabled, so NFS traffic from %s "+
						"is still dropped; this phase never force-enables a rule it did not create — "+
						"re-enable it with `pmx pve node firewall rules update %s --enable 1 --node %s` "+
						"(or delete it and re-run attach)",
					comment, deps.Node, plan.mgmtCIDR, pos, deps.Node)
			}
			rows = append(rows, []string{comment, "skip (already present)"})
			continue
		}

		// --enable 1 must be explicit: the PVE API's default for an omitted
		// `enable` is 0, which would create a disabled, no-op rule (the same
		// documented trap scripts/60-nfs-service's group_firewall guards).
		params := &nodes.CreateFirewallRulesParams{
			Type:    "in",
			Action:  "ACCEPT",
			Proto:   createPtr("tcp"),
			Dport:   createPtr(p.port),
			Source:  createPtr(plan.mgmtCIDR),
			Enable:  createPtr(int64(1)),
			Log:     createPtr("nolog"),
			Comment: createPtr(comment),
		}
		if cerr := deps.API.Nodes.CreateFirewallRules(ctx, deps.Node, params); cerr != nil {
			return nil, fmt.Errorf("create host firewall rule %q on node %q: %w", comment, deps.Node, cerr)
		}
		rows = append(rows, []string{comment, fmt.Sprintf("created (ACCEPT tcp/%s from %s)", p.port, plan.mgmtCIDR)})
	}

	return rows, nil
}
