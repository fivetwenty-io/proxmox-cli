package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// acmeCertPath is where a node keeps the certificate its API serves, the
// file an ACME order or renewal replaces.
const acmeCertPath = "/etc/pve/local/" + acmeManagedCertFilename

// sourceContextFingerprint is the FingerprintSource of a pin stored in the
// context itself (see cli.Connection.FingerprintSource).
const sourceContextFingerprint = "tls.fingerprint"

// acmeTrustHelp is appended to the order and renew help.
const acmeTrustHelp = "A context that pins the API certificate's fingerprint, verifies it against a " +
	"private CA bundle, or trusts it on first use rejects the new certificate. When the node " +
	"serves the invoking context's API, a hint on stderr shows the context update command that " +
	"switches the context to the system CAs."

// acmeTrustPlan records, before an ACME order or renewal replaces a node's
// certificate, how the invoking context and command verify that
// certificate, so the verb can warn afterwards when the change breaks them.
// Only trust in one particular certificate or CA gets a plan: an insecure
// context, or one that trusts the system CAs, keeps working.
type acmeTrustPlan struct {
	prefix  string
	ctxName string

	// contextPin, contextCA, and tofu are the context's own trust settings
	// that reject a certificate from a public ACME CA.
	contextPin bool
	contextCA  bool
	tofu       bool

	// overrideSource names the flag or variable that supplied a pin or CA
	// bundle for this invocation only, such as "--api-fingerprint" or
	// "$PMX_API_FINGERPRINT", and is empty when none did.
	overrideSource string

	// servingNode is the node that answers the API for this connection, and
	// is empty when it could not be determined.
	servingNode string
}

// planACMETrust inspects the context's stored trust settings and this
// invocation's trust overrides before the certificate changes, and returns
// nil when the change cannot affect either. The stored settings are read
// even when an override or --insecure replaced them for this one command,
// because the context still breaks on its next use. It also asks the
// cluster which node answers the API, while the current certificate still
// verifies, so the hint is skipped when another node's certificate changes.
func planACMETrust(cmd *cobra.Command, deps *cli.Deps) *acmeTrustPlan {
	plan := &acmeTrustPlan{
		prefix:  cli.CommandPrefix(cmd),
		ctxName: deps.CtxName,
	}

	var storedCA string
	if deps.Ctx != nil && !deps.Ctx.TLS.Insecure {
		tls := deps.Ctx.TLS
		plan.contextPin = tls.Fingerprint != ""
		plan.contextCA = tls.CACert != ""
		plan.tofu = tls.Tofu
		storedCA = tls.CACert
	}

	conn := deps.Route
	switch {
	case conn.Fingerprint != "" && conn.FingerprintSource != sourceContextFingerprint:
		plan.overrideSource = conn.FingerprintSource
	case conn.CACert != "" && conn.CACert != storedCA:
		plan.overrideSource = "$PMX_API_CA_CERT"
		if f := cmd.Flag("api-ca-cert"); f != nil && f.Changed {
			plan.overrideSource = "--api-ca-cert"
		}
	}

	if !plan.contextPin && !plan.contextCA && !plan.tofu && plan.overrideSource == "" {
		return nil
	}

	plan.servingNode = servingNode(cmd.Context(), deps)
	return plan
}

// servingNode returns the name of the node that answers the API, which
// /cluster/status marks with local=1, or "" when the lookup fails. A failure
// only widens the hint to "this may affect you", so it is logged rather than
// returned.
func servingNode(ctx context.Context, deps *cli.Deps) string {
	resp, err := deps.API.Cluster.ListStatus(ctx)
	if err != nil {
		if deps.Log != nil {
			deps.Log.Debug("acme trust hint: cluster status", slog.String("error", err.Error()))
		}
		return ""
	}
	for _, raw := range derefRaws(resp) {
		var e struct {
			Type  string     `json:"type"`
			Name  string     `json:"name"`
			Local pve.PVEInt `json:"local"`
		}
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		if e.Type == "node" && e.Local.Int() == 1 {
			return e.Name
		}
	}
	return ""
}

// hint returns the advice to print after node's certificate changed, or ""
// when the change does not touch this connection. async is true when the
// verb returned before the task finished, so the change is still to come.
func (p *acmeTrustPlan) hint(node string, async bool) string {
	if p == nil || (p.servingNode != "" && p.servingNode != node) {
		return ""
	}

	var b strings.Builder
	change := fmt.Sprintf("node %q now serves a new ACME certificate", node)
	if async {
		change = fmt.Sprintf("node %q will serve a new ACME certificate once the task finishes", node)
	}

	what := p.trustDescription()
	if p.ctxName != "" && what != "" {
		fmt.Fprintf(&b, "hint: context %q %s, and %s,\n", p.ctxName, what, change)
		fmt.Fprintf(&b, "  so the next command through %q will fail to verify it.\n", p.ctxName)
	} else {
		fmt.Fprintf(&b, "hint: %s, so the trust this command used will not verify it.\n", change)
	}

	if p.overrideSource != "" {
		fmt.Fprintf(&b, "  %s\n", overrideAdvice(p.overrideSource))
	}

	if clears := p.systemTrustFlags(); len(clears) > 0 {
		b.WriteString("  Trusting the system CAs keeps working across every renewal:\n")
		fmt.Fprintf(&b, "    %s context update %s %s\n", p.prefix, p.ctxName, strings.Join(clears, " "))
		fmt.Fprintf(&b, "    %s context validate %s --connect\n", p.prefix, p.ctxName)
	}

	if p.contextPin {
		b.WriteString("  To pin the new certificate instead, read its fingerprint on the node and store it;\n")
		b.WriteString("  a pin breaks again at every renewal:\n")
		fmt.Fprintf(&b, "    openssl x509 -in %s -noout -fingerprint -sha256\n", acmeCertPath)
		fmt.Fprintf(&b, "    %s context update %s --fingerprint <fingerprint>\n", p.prefix, p.ctxName)
	}

	return strings.TrimRight(b.String(), "\n")
}

// trustDescription says how the context verifies the API certificate.
func (p *acmeTrustPlan) trustDescription() string {
	switch {
	case p.contextPin:
		return "pins the API certificate by fingerprint"
	case p.contextCA:
		return "verifies the API against its own CA bundle (tls.ca-cert)"
	case p.tofu:
		return "trusts the API certificate it saw first (tls.tofu)"
	default:
		return ""
	}
}

// systemTrustFlags returns the context update flags that switch the context
// to the system CAs, clearing each setting that rejects an ACME certificate.
func (p *acmeTrustPlan) systemTrustFlags() []string {
	var flags []string
	if p.contextPin {
		flags = append(flags, `--fingerprint ""`)
	}
	if p.contextCA {
		flags = append(flags, `--ca-cert ""`)
	}
	if p.tofu {
		flags = append(flags, "--tofu=false")
	}
	if p.ctxName == "" {
		return nil
	}
	return flags
}

// overrideAdvice explains a per-invocation pin or CA bundle, which the
// context cannot fix, so the operator drops it from later commands.
func overrideAdvice(source string) string {
	if name, ok := strings.CutPrefix(source, "$"); ok {
		return fmt.Sprintf("The trust in %s will not verify the new certificate; unset %s before the next command.",
			source, name)
	}
	return fmt.Sprintf("The %s value this command used will not verify the new certificate; "+
		"drop it from later commands.", source)
}
