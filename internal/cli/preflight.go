package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// InboundAllowState classifies a firewall rule set by whether it contains a
// rule that would admit inbound management traffic once the firewall's DROP
// input policy takes effect. Order matters: lower values are more favorable,
// so combined scans of several rule sets keep the minimum.
type InboundAllowState int

const (
	// InboundAllowFound: an enabled inbound ACCEPT rule or an enabled
	// security-group insertion (whose members cannot be resolved
	// client-side) exists.
	InboundAllowFound InboundAllowState = iota
	// InboundAllowDisabledOnly: inbound ACCEPT or group rules exist but
	// every one of them is disabled. Rules are created disabled unless
	// --enable is passed, so this is an easy state to end up in.
	InboundAllowDisabledOnly
	// InboundAllowMissing: no inbound ACCEPT or group rule at all.
	InboundAllowMissing
)

// ScanInboundAllow classifies one firewall rule list (the raw entries from a
// ListFirewallRules call). Detection is best effort: security groups count as
// potential allows because their members are not resolved here.
func ScanInboundAllow(raws []json.RawMessage) InboundAllowState {
	state := InboundAllowMissing
	for _, raw := range raws {
		var r struct {
			Type   string `json:"type"`
			Action string `json:"action"`
			Enable int64  `json:"enable"`
		}
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		allow := r.Type == "group" || (r.Type == "in" && strings.EqualFold(r.Action, "ACCEPT"))
		if !allow {
			continue
		}
		if r.Enable != 0 {
			return InboundAllowFound
		}
		state = InboundAllowDisabledOnly
	}
	return state
}

// WarnInboundAllow prints the pre-flight lockout warning matching state to w.
// scope names the rule set(s) inspected, e.g. "datacenter". Nothing is printed
// when an enabled allow rule was found.
func WarnInboundAllow(w io.Writer, state InboundAllowState, scope string) {
	switch state {
	case InboundAllowDisabledOnly:
		_, _ = fmt.Fprintf(w, "WARNING: the %s rule set has inbound ACCEPT rules but all of them are disabled; "+
			"enable one first or SSH (22) and GUI (8006) access may be cut off\n", scope)
	case InboundAllowMissing:
		_, _ = fmt.Fprintf(w, "WARNING: no inbound ACCEPT rule found in the %s rule set; "+
			"enabling the firewall may cut off SSH (22) and GUI (8006) access\n", scope)
	}
}

// WarnIfInquorate fetches /cluster/status and warns on w when the cluster
// reports it is not quorate, since op may then fail or be deferred until
// quorum returns. Best effort by design: standalone nodes (no cluster entry)
// and status fetch failures stay silent, and the operation always proceeds.
func WarnIfInquorate(ctx context.Context, deps *Deps, w io.Writer, op string) {
	if deps.API == nil {
		return
	}
	resp, err := deps.API.Cluster.ListStatus(ctx)
	if err != nil || resp == nil {
		return
	}
	for _, raw := range *resp {
		var e struct {
			Type    string `json:"type"`
			Quorate *int   `json:"quorate"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		if e.Type == "cluster" && e.Quorate != nil && *e.Quorate == 0 {
			_, _ = fmt.Fprintf(w, "WARNING: the cluster is not quorate; %s may fail or not take effect until quorum is restored\n", op)
			return
		}
	}
}
