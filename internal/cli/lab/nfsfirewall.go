package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

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
// (a rule can exist yet be a disabled no-op — the PVE API's default for an
// omitted `enable` on CREATE is 0), the source (verified against the lab's
// mgmt /24 — a comment match alone would silently accept a stale rule
// scoping a previous subnet after a lab re-IP, while the sharenfs half of
// the doubled ACL reconciles, leaving the two layers diverged), and the
// position (only to render an actionable remediation command). Enable and
// Pos are raw: PVE firewall endpoints are inconsistent about numeric
// encoding (the single-rule GET returns pos as a string — see node/
// firewall.go's workaround), so both tolerate a number or a quoted number.
type nfsFirewallRuleView struct {
	Comment string          `json:"comment"`
	Enable  json.RawMessage `json:"enable"`
	Source  string          `json:"source"`
	Pos     json.RawMessage `json:"pos"`
}

// nfsFlexInt parses a PVE JSON field that may arrive as a JSON number or as
// a quoted numeric string. ok is false when raw is absent/empty or neither
// form parses.
func nfsFlexInt(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, perr := strconv.ParseInt(s, 10, 64); perr == nil {
			return v, true
		}
	}
	return 0, false
}

// nfsFirewallRuleEnabled reports whether a listed rule is enabled. An absent
// `enable` field counts as enabled: in pve-firewall's own model, DISABLED is
// the marked state (a '|'-prefixed line in host.fw), so a rule the API lists
// without the flag is live — treating absence as disabled would fail every
// attach against a healthy host whose payloads omit the default.
func nfsFirewallRuleEnabled(v nfsFirewallRuleView) bool {
	n, ok := nfsFlexInt(v.Enable)
	return !ok || n == 1
}

// nfsFirewallRulePos renders a listed rule's position for remediation
// messages, or a placeholder when the payload carried none.
func nfsFirewallRulePos(v nfsFirewallRuleView) string {
	if n, ok := nfsFlexInt(v.Pos); ok {
		return fmt.Sprintf("%d", n)
	}
	return "<pos>"
}

// nfsEnsureFirewallRules ensures the outer node's host firewall carries an
// enabled ACCEPT rule for each of nfsFirewallPorts scoped to plan's mgmt
// /24, via the PVE firewall API against deps.Node (the same node the ZFS
// ensure phase just ran against). Rules are matched by comment, then
// VERIFIED — a matching rule must be enabled and must scope exactly the
// lab's mgmt /24 to count as satisfied. Absent is a create; present but
// disabled or mis-scoped is a loud failure, never a silent skip (that would
// recreate the exact dead-mount symptom this phase exists to kill) and
// never an in-place edit (this package does not mutate a rule it did not
// create — the operator may have changed it deliberately). When several
// rules share the comment, ANY satisfying one counts. Returns one
// STEP/STATUS row per port, in nfsFirewallPorts order.
func nfsEnsureFirewallRules(ctx context.Context, deps *cli.Deps, plan nfsServerEnsurePlan) ([][]string, error) {
	resp, err := deps.API.Nodes.ListFirewallRules(ctx, deps.Node)
	if err != nil {
		return nil, fmt.Errorf("list host firewall rules on node %q: %w", deps.Node, err)
	}

	byComment := make(map[string][]nfsFirewallRuleView)
	if resp != nil {
		for _, raw := range *resp {
			var v nfsFirewallRuleView
			if uerr := json.Unmarshal(raw, &v); uerr != nil {
				return nil, fmt.Errorf("decode host firewall rule on node %q: %w", deps.Node, uerr)
			}
			if v.Comment != "" {
				byComment[v.Comment] = append(byComment[v.Comment], v)
			}
		}
	}

	rows := make([][]string, 0, len(nfsFirewallPorts))
	for _, p := range nfsFirewallPorts {
		comment := nfsFirewallRuleComment(plan.labName, p.svc, p.port)

		if matches := byComment[comment]; len(matches) > 0 {
			satisfied := false
			for _, v := range matches {
				if nfsFirewallRuleEnabled(v) && v.Source == plan.mgmtCIDR {
					satisfied = true
					break
				}
			}
			if !satisfied {
				v := matches[0]
				reason := "is disabled"
				if v.Source != plan.mgmtCIDR {
					reason = fmt.Sprintf("scopes source %q instead of this lab's mgmt %s (stale after a re-IP?)",
						v.Source, plan.mgmtCIDR)
				}
				return nil, fmt.Errorf(
					"host firewall rule %q on node %q exists but %s, so NFS traffic from %s is still "+
						"dropped; this phase never edits a rule it did not create — fix it with "+
						"`pmx pve node firewall rules update %s --enable 1 --source %s --node %s` "+
						"(or delete it and re-run attach)",
					comment, deps.Node, reason, plan.mgmtCIDR, nfsFirewallRulePos(v), plan.mgmtCIDR, deps.Node)
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
