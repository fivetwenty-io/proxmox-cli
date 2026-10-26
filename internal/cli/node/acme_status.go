package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/remote"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/propstr"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// Node config keys and defaults for ACME, from the PVE schema of
// /nodes/{node}/config.
const (
	nodeConfigACMEKey         = "acme"
	nodeConfigACMEDomainKey   = "acmedomain"
	acmeDefaultAccount        = "default"
	acmeStandalonePlugin      = "standalone"
	acmeRenewalTimer          = "pve-daily-update.timer"
	acmeRenewalWindowDays     = 30
	acmeSelfSignedCertificate = "pve-ssl.pem"
)

// acmeDomain is one domain the node's ACME certificate is ordered for.
type acmeDomain struct {
	Domain string `json:"domain"`
	// Source is the node config key the domain came from: acmedomainN, or
	// acme for the standalone domains= list.
	Source string `json:"source"`
	Plugin string `json:"plugin"`
	Alias  string `json:"alias,omitempty"`
	// PluginType and PluginAPI come from the cluster's plugin list, and are
	// empty when that list could not be read.
	PluginType string `json:"plugin_type,omitempty"`
	PluginAPI  string `json:"plugin_api,omitempty"`
}

// nodeACMEConfig is the ACME part of a node's configuration.
type nodeACMEConfig struct {
	Account string
	Domains []acmeDomain
}

// parseNodeACMEConfig extracts the ACME account and domains from a decoded
// node config. The account defaults to "default" and a domain's plugin to
// "standalone", as PVE does. Domains are returned in acmedomainN index order
// followed by the acme domains= list.
func parseNodeACMEConfig(cfg map[string]any) nodeACMEConfig {
	out := nodeACMEConfig{Account: acmeDefaultAccount}

	var acmeDomains []string
	if raw, ok := cfg[nodeConfigACMEKey].(string); ok {
		props := propstr.Parse(raw, "")
		if v, ok := props.Get("account"); ok && v != "" {
			out.Account = v
		}
		if v, ok := props.Get("domains"); ok {
			for d := range strings.SplitSeq(v, ";") {
				if d = strings.TrimSpace(d); d != "" {
					acmeDomains = append(acmeDomains, d)
				}
			}
		}
	}

	type indexed struct {
		n int
		d acmeDomain
	}
	var numbered []indexed
	for key, val := range cfg {
		suffix, ok := strings.CutPrefix(key, nodeConfigACMEDomainKey)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(suffix)
		if err != nil || n < 0 {
			continue
		}
		raw, ok := val.(string)
		if !ok {
			continue
		}
		props := propstr.Parse(raw, "domain")
		domain, _ := props.Get("domain")
		if domain == "" {
			continue
		}
		plugin, _ := props.Get("plugin")
		if plugin == "" {
			plugin = acmeStandalonePlugin
		}
		alias, _ := props.Get("alias")
		numbered = append(numbered, indexed{n: n, d: acmeDomain{
			Domain: domain, Source: key, Plugin: plugin, Alias: alias,
		}})
	}
	sort.Slice(numbered, func(i, j int) bool { return numbered[i].n < numbered[j].n })
	for _, e := range numbered {
		out.Domains = append(out.Domains, e.d)
	}
	for _, d := range acmeDomains {
		out.Domains = append(out.Domains, acmeDomain{Domain: d, Source: nodeConfigACMEKey, Plugin: acmeStandalonePlugin})
	}

	return out
}

// readNodeACMEConfig reads the node config through the raw API, which keeps
// the acmedomainN keys the generated response struct drops, and parses its
// ACME settings.
func readNodeACMEConfig(ctx context.Context, deps *cli.Deps, node string) (nodeACMEConfig, error) {
	resp, err := deps.API.Raw.GetCtx(ctx, nodeConfigPath(node), nil)
	if err != nil {
		return nodeACMEConfig{}, fmt.Errorf("get config for node %q: %w", node, err)
	}
	cfg, ok := resp.(map[string]any)
	if !ok {
		if resp == nil {
			return nodeACMEConfig{Account: acmeDefaultAccount}, nil
		}
		return nodeACMEConfig{}, fmt.Errorf("get config for node %q: unexpected response shape %T", node, resp)
	}
	return parseNodeACMEConfig(cfg), nil
}

// acmeAccountStatus is the ACME account the node orders with.
type acmeAccountStatus struct {
	Name       string   `json:"name"`
	Registered bool     `json:"registered"`
	Status     string   `json:"status,omitempty"`
	Directory  string   `json:"directory,omitempty"`
	Contact    []string `json:"contact,omitempty"`
}

// acmeCertStatus is the certificate installed at pveproxy-ssl.pem.
type acmeCertStatus struct {
	File        string   `json:"file"`
	Subject     string   `json:"subject"`
	Issuer      string   `json:"issuer"`
	SAN         []string `json:"san"`
	NotBefore   string   `json:"notbefore,omitempty"`
	NotAfter    string   `json:"notafter,omitempty"`
	DaysLeft    *int     `json:"days_left,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
}

// acmeTimerStatus is pve-daily-update.timer as systemd reports it.
type acmeTimerStatus struct {
	LoadState   string `json:"load_state"`
	ActiveState string `json:"active_state"`
	NextRun     string `json:"next_run"`
	LastRun     string `json:"last_run"`
}

// acmeRenewalStatus says what renews the certificate, and the timer's state
// when --timer read it.
type acmeRenewalStatus struct {
	By     string           `json:"by"`
	Window string           `json:"window"`
	Timer  *acmeTimerStatus `json:"timer,omitempty"`
	Note   string           `json:"note"`
}

// acmeStatus is the whole view rendered by node cert acme status.
type acmeStatus struct {
	Node        string            `json:"node"`
	Account     acmeAccountStatus `json:"account"`
	Domains     []acmeDomain      `json:"domains"`
	Certificate *acmeCertStatus   `json:"certificate"`
	Renewal     acmeRenewalStatus `json:"renewal"`
	// Problems are findings that stop or will stop renewal; Notes are
	// things the view could not read.
	Problems []string `json:"problems"`
	Notes    []string `json:"notes"`
}

// certInfoEntry is the decoded shape of one /nodes/{node}/certificates/info
// entry, without the PEM.
type certInfoEntry struct {
	Filename    string      `json:"filename"`
	Subject     string      `json:"subject"`
	Issuer      string      `json:"issuer"`
	SAN         []string    `json:"san"`
	NotBefore   *pve.PVEInt `json:"notbefore"`
	NotAfter    *pve.PVEInt `json:"notafter"`
	Fingerprint string      `json:"fingerprint"`
}

// newCertAcmeStatusCmd builds `pmx pve node cert acme status`.
func newCertAcmeStatusCmd() *cobra.Command {
	var (
		f     sshFlags
		timer bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report whether the node's ACME certificate is set up to renew",
		Long: "Report the resolved node's ACME setup and certificate: the ACME account and domains " +
			"from the node config, the challenge plugin each domain uses, and the certificate " +
			"installed at " + acmeManagedCertFilename + " with its subject, SANs, issuer, and expiry. " +
			"Problems that stop renewal, such as an unregistered account, a missing plugin, a domain " +
			"the certificate does not cover, or a certificate already inside the renewal window, are " +
			"listed. The view is read-only.\n\n" +
			"Renewal runs on the node from " + acmeRenewalTimer + ", which renews the certificate once " +
			"it is within 30 days of expiry, and logs renewal errors to the node's journal " +
			"(journalctl -u pve-daily-update). The PVE API cannot see systemd timers, so the timer's " +
			"state is reported only when --timer is passed, which reads it over SSH with the same " +
			"connection flags as 'pmx pve node exec'.\n\n" +
			"Reading the plugin list needs Sys.Modify on /, and reading the account details needs " +
			"root@pam with a password; without them the view notes what it could not read.",
		Example: `  pmx pve node cert acme status --node pve1
  pmx pve node cert acme status --node pve1 --timer
  pmx pve node cert acme status --node pve1 -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}

			status, err := collectACMEStatus(cmd.Context(), deps, deps.Node, time.Now())
			if err != nil {
				return err
			}
			if timer {
				t, err := readACMERenewalTimer(cmd, deps, &f, deps.Node)
				if err != nil {
					return err
				}
				status.Renewal.Timer = t
				status.Renewal.Note = "timer state read over ssh"
				status.Problems = append(status.Problems, timerProblems(t)...)
			}

			if err := deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: status.single(), Raw: status}, deps.Format); err != nil {
				return err
			}
			if deps.Format == output.FormatTable || deps.Format == output.FormatPlain {
				status.writeFindings(cmd.OutOrStdout())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&timer, "timer", false,
		"also read "+acmeRenewalTimer+" on the node over ssh")
	registerSSHFlags(cmd, &f)
	return cmd
}

// collectACMEStatus gathers the API side of the view. The node config, the
// account list, and the certificate list are readable by any user who can
// see the node, so a failure there is returned. The plugin list and the
// account details need more privilege, so a failure there is recorded as a
// note and the view goes on.
func collectACMEStatus(ctx context.Context, deps *cli.Deps, node string, now time.Time) (*acmeStatus, error) {
	cfg, err := readNodeACMEConfig(ctx, deps, node)
	if err != nil {
		return nil, err
	}

	status := &acmeStatus{
		Node:     node,
		Account:  acmeAccountStatus{Name: cfg.Account},
		Domains:  cfg.Domains,
		Problems: []string{},
		Notes:    []string{},
		Renewal: acmeRenewalStatus{
			By:     acmeRenewalTimer,
			Window: fmt.Sprintf("renews within %d days of expiry", acmeRenewalWindowDays),
			Note:   "the PVE API cannot see systemd timers; pass --timer to read it over ssh",
		},
	}
	if status.Domains == nil {
		status.Domains = []acmeDomain{}
	}

	if err := status.addAccount(ctx, deps); err != nil {
		return nil, err
	}
	status.addPlugins(ctx, deps)
	if err := status.addCertificate(ctx, deps, node, now); err != nil {
		return nil, err
	}

	if len(status.Domains) == 0 {
		status.Problems = append(status.Problems, fmt.Sprintf(
			"no ACME domains are configured on node %q, so there is nothing to order or renew; "+
				"add one with 'pmx pve node config set --acme-domain 0=<fqdn>,plugin=<plugin>'", node))
	}

	return status, nil
}

// addAccount checks the configured account is registered and, when the
// caller may read it, fills in its CA directory, status, and contact.
func (s *acmeStatus) addAccount(ctx context.Context, deps *cli.Deps) error {
	resp, err := deps.API.Cluster.ListAcmeAccount(ctx)
	if err != nil {
		return fmt.Errorf("list acme accounts: %w", err)
	}
	for _, raw := range derefRaws(resp) {
		var e struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return fmt.Errorf("decode acme account entry: %w", err)
		}
		if e.Name == s.Account.Name {
			s.Account.Registered = true
			break
		}
	}
	if !s.Account.Registered {
		s.Problems = append(s.Problems, fmt.Sprintf(
			"ACME account %q is not registered, so orders and renewals fail; register it with "+
				"'pmx pve cluster acme account create %s --contact <email>'", s.Account.Name, s.Account.Name))
		return nil
	}

	detail, err := deps.API.Cluster.GetAcmeAccount(ctx, s.Account.Name)
	if err != nil {
		s.Notes = append(s.Notes, accountDetailNote(s.Account.Name, err))
		return nil
	}
	if detail.Directory != nil {
		s.Account.Directory = *detail.Directory
	}
	if len(detail.Account) > 0 {
		var acct struct {
			Status  string   `json:"status"`
			Contact []string `json:"contact"`
		}
		if err := json.Unmarshal(detail.Account, &acct); err != nil {
			s.Notes = append(s.Notes, fmt.Sprintf("ACME account %q details could not be decoded: %v",
				s.Account.Name, err))
			return nil
		}
		s.Account.Status = acct.Status
		s.Account.Contact = acct.Contact
		if acct.Status != "" && acct.Status != "valid" {
			s.Problems = append(s.Problems, fmt.Sprintf(
				"ACME account %q has CA status %q rather than valid, so the CA refuses its orders",
				s.Account.Name, acct.Status))
		}
	}
	return nil
}

// accountDetailNote explains why the account details are missing. Proxmox
// lets only root@pam with a password read them, so a 403 is the usual case
// and gets a specific explanation.
func accountDetailNote(name string, err error) string {
	var permErr *pveerrors.PermissionError
	if errors.Is(err, pveerrors.ErrForbidden) || errors.As(err, &permErr) {
		return fmt.Sprintf("ACME account %q details (CA directory, status, contact) need root@pam "+
			"with a password; this context cannot read them", name)
	}
	return fmt.Sprintf("ACME account %q details could not be read: %v", name, err)
}

// addPlugins fills in each domain's plugin type and DNS API from the
// cluster's plugin list, and reports domains whose plugin does not exist.
// Only the id, type, and api fields are decoded, so plugin credentials
// never reach the view.
func (s *acmeStatus) addPlugins(ctx context.Context, deps *cli.Deps) {
	needsLookup := false
	for _, d := range s.Domains {
		if d.Plugin != acmeStandalonePlugin {
			needsLookup = true
		}
	}

	type pluginInfo struct{ typ, api string }
	plugins := map[string]pluginInfo{}
	readable := false
	if needsLookup {
		resp, err := deps.API.Cluster.ListAcmePlugins(ctx, &pvecluster.ListAcmePluginsParams{})
		if err != nil {
			s.Notes = append(s.Notes, fmt.Sprintf("the ACME plugin list could not be read, so plugin "+
				"types and DNS APIs are not shown (it needs Sys.Modify on /): %v", err))
		} else {
			readable = true
			for _, raw := range derefRaws(resp) {
				var e struct {
					Plugin string `json:"plugin"`
					Type   string `json:"type"`
					API    string `json:"api"`
				}
				if err := json.Unmarshal(raw, &e); err != nil {
					s.Notes = append(s.Notes, fmt.Sprintf("an ACME plugin entry could not be decoded: %v", err))
					continue
				}
				plugins[e.Plugin] = pluginInfo{typ: e.Type, api: e.API}
			}
		}
	}

	for i, d := range s.Domains {
		if d.Plugin == acmeStandalonePlugin {
			s.Domains[i].PluginType = acmeStandalonePlugin
			continue
		}
		if !readable {
			continue
		}
		p, ok := plugins[d.Plugin]
		if !ok {
			s.Problems = append(s.Problems, fmt.Sprintf(
				"domain %s uses ACME plugin %q, which does not exist; create it with "+
					"'pmx pve cluster acme plugin create %s ...'", d.Domain, d.Plugin, d.Plugin))
			continue
		}
		s.Domains[i].PluginType = p.typ
		s.Domains[i].PluginAPI = p.api
	}
}

// addCertificate reads the certificate at pveproxy-ssl.pem and checks its
// expiry and its coverage of the configured domains.
func (s *acmeStatus) addCertificate(ctx context.Context, deps *cli.Deps, node string, now time.Time) error {
	resp, err := deps.API.Nodes.ListCertificatesInfo(ctx, node)
	if err != nil {
		return fmt.Errorf("list certificates on node %q: %w", node, err)
	}

	var found *certInfoEntry
	for _, raw := range derefRaws(resp) {
		var e certInfoEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return fmt.Errorf("decode certificate entry: %w", err)
		}
		if e.Filename == acmeManagedCertFilename {
			found = &e
			break
		}
	}
	if found == nil {
		msg := fmt.Sprintf("no certificate is installed at %s, so the node serves its self-signed %s",
			acmeManagedCertFilename, acmeSelfSignedCertificate)
		if len(s.Domains) > 0 {
			msg += "; order one with 'pmx pve node cert acme order --yes'"
		}
		s.Problems = append(s.Problems, msg)
		return nil
	}

	cert := &acmeCertStatus{
		File:        found.Filename,
		Subject:     found.Subject,
		Issuer:      found.Issuer,
		SAN:         found.SAN,
		Fingerprint: found.Fingerprint,
	}
	if cert.SAN == nil {
		cert.SAN = []string{}
	}
	if found.NotBefore != nil {
		cert.NotBefore = time.Unix(found.NotBefore.Int(), 0).UTC().Format(time.RFC3339)
	}
	if found.NotAfter != nil {
		expiry := time.Unix(found.NotAfter.Int(), 0).UTC()
		cert.NotAfter = expiry.Format(time.RFC3339)
		// Floor, so a certificate expiring in 23 hours has 0 days left and
		// one that expired an hour ago has -1.
		days := int(math.Floor(expiry.Sub(now).Hours() / 24))
		cert.DaysLeft = &days
		s.Problems = append(s.Problems, expiryProblems(expiry, now, days, len(s.Domains) > 0)...)
	}
	s.Certificate = cert

	var uncovered []string
	for _, d := range s.Domains {
		if !certCovers(cert.SAN, d.Domain) {
			uncovered = append(uncovered, d.Domain)
		}
	}
	if len(uncovered) > 0 {
		s.Problems = append(s.Problems, fmt.Sprintf(
			"the installed certificate does not cover %s; order a new one with "+
				"'pmx pve node cert acme order --yes'", strings.Join(uncovered, ", ")))
	}
	return nil
}

// expiryProblems reports an expired certificate, or one that has sat inside
// the renewal window for more than a day, which pve-daily-update would have
// renewed had renewal been working. hasDomains is false when no ACME domains
// are configured, in which case nothing renews the certificate at all.
func expiryProblems(expiry, now time.Time, days int, hasDomains bool) []string {
	switch {
	case !expiry.After(now):
		return []string{fmt.Sprintf("the certificate expired on %s", expiry.Format(time.RFC3339))}
	case hasDomains && days < acmeRenewalWindowDays-1:
		return []string{fmt.Sprintf(
			"the certificate expires in %d days, inside the %d-day renewal window, so %s should "+
				"already have renewed it; check the timer with --timer and the node's journal with "+
				"'journalctl -u pve-daily-update'", days, acmeRenewalWindowDays, acmeRenewalTimer)}
	default:
		return nil
	}
}

// certCovers reports whether a certificate with the given SAN entries is
// valid for domain. Entries may carry an OpenSSL-style "DNS:" prefix, and a
// wildcard entry covers exactly one leftmost label.
func certCovers(sans []string, domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	for _, san := range sans {
		san = strings.ToLower(strings.TrimSpace(san))
		san = strings.TrimPrefix(san, "dns:")
		san = strings.TrimSuffix(san, ".")
		if san == domain {
			return true
		}
		if rest, ok := strings.CutPrefix(san, "*."); ok {
			if _, parent, found := strings.Cut(domain, "."); found && parent == rest {
				return true
			}
		}
	}
	return false
}

// acmeTimerProperties are the systemd properties read from the renewal
// timer.
const acmeTimerProperties = "LoadState,ActiveState,NextElapseUSecRealtime,LastTriggerUSec"

// readACMERenewalTimer reads pve-daily-update.timer on node over ssh, using
// the shared ssh flags and the context's ssh defaults, in batch mode so a
// missing key fails instead of prompting.
func readACMERenewalTimer(cmd *cobra.Command, deps *cli.Deps, f *sshFlags, node string) (*acmeTimerStatus, error) {
	remote.ApplyContextSSHDefaults(cmd, deps, f, "user", "port", "identity", "jump")
	host, err := resolveHost(cmd, deps, node)
	if err != nil {
		return nil, err
	}

	argv := append(sshcmd.BatchOptionArgs(f), sshcmd.Dest(f, host),
		"systemctl", "show", acmeRenewalTimer, "--property="+acmeTimerProperties)
	var stdout, stderr bytes.Buffer
	if err := deps.Runner.Run("ssh", argv, nil, nil, &stdout, &stderr); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("read %s on node %q over ssh: %w: %s (run without --timer to skip it)",
				acmeRenewalTimer, node, err, detail)
		}
		return nil, fmt.Errorf("read %s on node %q over ssh: %w (run without --timer to skip it)",
			acmeRenewalTimer, node, err)
	}
	return parseTimerShow(stdout.String())
}

// parseTimerShow parses the KEY=VALUE lines of `systemctl show`. An empty
// timestamp, which systemd prints for a timer that never ran or has no next
// run, becomes "n/a".
func parseTimerShow(out string) (*acmeTimerStatus, error) {
	props := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			props[key] = strings.TrimSpace(val)
		}
	}
	if props["LoadState"] == "" || props["ActiveState"] == "" {
		return nil, fmt.Errorf("read %s: systemctl show printed no LoadState or ActiveState", acmeRenewalTimer)
	}
	orNA := func(v string) string {
		if v == "" {
			return "n/a"
		}
		return v
	}
	return &acmeTimerStatus{
		LoadState:   props["LoadState"],
		ActiveState: props["ActiveState"],
		NextRun:     orNA(props["NextElapseUSecRealtime"]),
		LastRun:     orNA(props["LastTriggerUSec"]),
	}, nil
}

// timerProblems reports a renewal timer that is missing or not running.
func timerProblems(t *acmeTimerStatus) []string {
	switch {
	case t.LoadState != "loaded":
		return []string{fmt.Sprintf("%s is %s on the node, so nothing renews the certificate",
			acmeRenewalTimer, t.LoadState)}
	case t.ActiveState != "active":
		return []string{fmt.Sprintf("%s is %s, so nothing renews the certificate; start it on the node "+
			"with 'systemctl enable --now %s'", acmeRenewalTimer, t.ActiveState, acmeRenewalTimer)}
	default:
		return nil
	}
}

// single flattens the view into the key/value table the table and plain
// formats render.
func (s *acmeStatus) single() map[string]string {
	m := map[string]string{
		"node":    s.Node,
		"account": s.Account.Name,
	}
	if s.Account.Registered {
		m["account.registered"] = "yes"
	} else {
		m["account.registered"] = "no"
	}
	setIf := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	setIf("account.status", s.Account.Status)
	setIf("account.directory", s.Account.Directory)
	setIf("account.contact", strings.Join(s.Account.Contact, ", "))

	if len(s.Domains) == 0 {
		m["domains"] = "none configured"
	}
	for _, d := range s.Domains {
		m["domain."+d.Domain] = describeACMEDomain(d)
	}

	if c := s.Certificate; c != nil {
		m["certificate.file"] = c.File
		m["certificate.subject"] = c.Subject
		m["certificate.issuer"] = c.Issuer
		m["certificate.san"] = strings.Join(c.SAN, ", ")
		setIf("certificate.fingerprint", c.Fingerprint)
		if c.NotAfter != "" && c.DaysLeft != nil {
			m["certificate.expires"] = fmt.Sprintf("%s (%d days left)", c.NotAfter, *c.DaysLeft)
		}
	} else {
		m["certificate"] = "none at " + acmeManagedCertFilename + "; the self-signed " +
			acmeSelfSignedCertificate + " is in use"
	}

	m["renewal"] = fmt.Sprintf("%s on the node, within %d days of expiry", acmeRenewalTimer, acmeRenewalWindowDays)
	if t := s.Renewal.Timer; t != nil {
		m["renewal.timer"] = t.LoadState + ", " + t.ActiveState
		m["renewal.timer.next"] = t.NextRun
		m["renewal.timer.last"] = t.LastRun
	} else {
		m["renewal.timer"] = "not visible to the API; pass --timer to read it over ssh"
	}

	m["problems"] = countLabel(len(s.Problems), "none found")
	if len(s.Notes) > 0 {
		m["notes"] = countLabel(len(s.Notes), "")
	}
	return m
}

// countLabel renders a finding count for the table, pointing at the list
// writeFindings prints below it.
func countLabel(n int, none string) string {
	if n == 0 {
		return none
	}
	return fmt.Sprintf("%d, listed below", n)
}

// writeFindings prints the problems and notes as prose after the table. They
// are full sentences with the command that fixes each one, which a table
// cell would truncate; JSON and YAML carry them in the problems and notes
// arrays instead.
func (s *acmeStatus) writeFindings(w io.Writer) {
	for _, section := range []struct {
		title string
		items []string
	}{{"Problems:", s.Problems}, {"Notes:", s.Notes}} {
		if len(section.items) == 0 {
			continue
		}
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, section.title)
		for _, item := range section.items {
			_, _ = fmt.Fprintf(w, "  - %s\n", item)
		}
	}
}

// describeACMEDomain renders a domain's challenge setup in one line.
func describeACMEDomain(d acmeDomain) string {
	var b strings.Builder
	if d.Plugin == acmeStandalonePlugin {
		b.WriteString("standalone http-01 challenge")
	} else {
		fmt.Fprintf(&b, "plugin %s", d.Plugin)
		switch {
		case d.PluginType != "" && d.PluginAPI != "":
			fmt.Fprintf(&b, " (%s, api %s)", d.PluginType, d.PluginAPI)
		case d.PluginType != "":
			fmt.Fprintf(&b, " (%s)", d.PluginType)
		}
	}
	if d.Alias != "" {
		fmt.Fprintf(&b, ", alias %s", d.Alias)
	}
	fmt.Fprintf(&b, " [%s]", d.Source)
	return b.String()
}
