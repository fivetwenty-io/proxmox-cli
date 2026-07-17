package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

func rawRules(t *testing.T, rules ...string) []json.RawMessage {
	t.Helper()
	raws := make([]json.RawMessage, 0, len(rules))
	for _, r := range rules {
		raws = append(raws, json.RawMessage(r))
	}
	return raws
}

func TestScanInboundAllow_EnabledAccept(t *testing.T) {
	raws := rawRules(t,
		`{"type":"out","action":"ACCEPT","enable":1}`,
		`{"type":"in","action":"ACCEPT","enable":1,"dport":"22"}`,
	)
	if got := cli.ScanInboundAllow(raws); got != cli.InboundAllowFound {
		t.Fatalf("got %v, want InboundAllowFound", got)
	}
}

func TestScanInboundAllow_EnabledGroupCountsAsAllow(t *testing.T) {
	raws := rawRules(t, `{"type":"group","action":"management","enable":1}`)
	if got := cli.ScanInboundAllow(raws); got != cli.InboundAllowFound {
		t.Fatalf("got %v, want InboundAllowFound", got)
	}
}

func TestScanInboundAllow_DisabledOnly(t *testing.T) {
	// PVE creates rules disabled unless --enable is passed, so a present but
	// disabled ACCEPT must be reported distinctly from a missing one.
	raws := rawRules(t,
		`{"type":"in","action":"ACCEPT","enable":0,"dport":"22"}`,
		`{"type":"in","action":"DROP","enable":1}`,
	)
	if got := cli.ScanInboundAllow(raws); got != cli.InboundAllowDisabledOnly {
		t.Fatalf("got %v, want InboundAllowDisabledOnly", got)
	}
}

func TestScanInboundAllow_Missing(t *testing.T) {
	raws := rawRules(t,
		`{"type":"in","action":"DROP","enable":1}`,
		`{"type":"out","action":"ACCEPT","enable":1}`,
	)
	if got := cli.ScanInboundAllow(raws); got != cli.InboundAllowMissing {
		t.Fatalf("got %v, want InboundAllowMissing", got)
	}

	if got := cli.ScanInboundAllow(nil); got != cli.InboundAllowMissing {
		t.Fatalf("empty rule set: got %v, want InboundAllowMissing", got)
	}
}

func TestScanInboundAllow_CaseInsensitiveActionAndBadJSON(t *testing.T) {
	raws := rawRules(t,
		`not-json`,
		`{"type":"in","action":"accept","enable":1}`,
	)
	if got := cli.ScanInboundAllow(raws); got != cli.InboundAllowFound {
		t.Fatalf("got %v, want InboundAllowFound", got)
	}
}

func TestWarnInboundAllow(t *testing.T) {
	var b strings.Builder
	cli.WarnInboundAllow(&b, cli.InboundAllowFound, "datacenter")
	if b.Len() != 0 {
		t.Fatalf("InboundAllowFound must stay silent, got %q", b.String())
	}

	b.Reset()
	cli.WarnInboundAllow(&b, cli.InboundAllowDisabledOnly, "datacenter")
	if !strings.Contains(b.String(), "disabled") {
		t.Fatalf("disabled-only warning missing, got %q", b.String())
	}

	b.Reset()
	cli.WarnInboundAllow(&b, cli.InboundAllowMissing, "node and datacenter")
	out := b.String()
	if !strings.Contains(out, "no inbound ACCEPT rule") || !strings.Contains(out, "node and datacenter") {
		t.Fatalf("missing-rule warning malformed, got %q", out)
	}
}
