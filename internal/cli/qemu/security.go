package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/propstr"
)

// newSecurityCmd builds `pve qemu security` and its sub-commands: the
// umbrella for reading and hardening a VM's security posture.
func newSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "security",
		Short: "Inspect and harden VM security posture (protection, boot chain, encryption, agent, firewall)",
		Long: "Read and tune the layered security posture of a QEMU VM: the protection flag, " +
			"Secure Boot / EFI and TPM state devices, confidential-computing memory encryption " +
			"(AMD SEV / Intel TDX), CPU mitigation flags, guest-agent configuration, and " +
			"per-NIC firewall coverage.\n\n" +
			"All commands use only the PVE config and firewall APIs (no ssh). Reads need " +
			"VM.Audit; mutations need the matching VM.Config.* privilege. Firewall *rules* " +
			"management lives under 'pve qemu firewall'; the security commands report firewall " +
			"posture but do not edit rules.",
	}
	cmd.AddCommand(
		newSecurityShowCmd(),
		newSecurityListCmd(),
		newSecurityProtectionCmd(),
		newSecurityAgentCmd(),
		newSecuritySecurebootCmd(),
		newSecurityTpmCmd(),
		newSecurityConfidentialCmd(),
		newSecurityCpuFlagsCmd(),
		newSecurityNicCmd(),
	)
	return cmd
}

// readRawConfig fetches a VM's raw config object (the same GET newConfigGetCmd
// uses) and returns it alongside its digest. Every security mutation reads
// through this helper first: guard checks inspect the current values, and the
// digest is auto-attached to the following PUT unless the caller passed an
// explicit --digest (see applyDigest).
func readRawConfig(ctx context.Context, deps *cli.Deps, node, vmid string) (map[string]any, string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%s/config", url.PathEscape(node), url.PathEscape(vmid))
	data, err := deps.API.Raw.GetCtx(ctx, path, nil)
	if err != nil {
		return nil, "", fmt.Errorf("get config for VM %s on node %q: %w", vmid, node, err)
	}
	m, ok := data.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("get config for VM %s: unexpected response shape %T", vmid, data)
	}
	digest, _ := m["digest"].(string)
	return m, digest, nil
}

// rawStr returns the stringified value of key in a raw config map, and
// whether the key was present at all.
func rawStr(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	return stringifyValue(v), true
}

// rawBoolDefault returns the boolean value of key in a raw config map ("1"/
// "true" is true, anything else present is false), or def when the key is
// absent.
func rawBoolDefault(m map[string]any, key string, def bool) bool {
	s, ok := rawStr(m, key)
	if !ok {
		return def
	}
	return s == "1" || strings.EqualFold(s, "true")
}

// boolToStr renders a bool as the "1"/"0" PVE uses inside property strings.
func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// joinSigned renders a list of flag names with a leading sign, space-joined
// (e.g. ["spec-ctrl", "ssbd"] with sign "+" -> "+spec-ctrl +ssbd").
func joinSigned(flags []string, sign string) string {
	if len(flags) == 0 {
		return ""
	}
	parts := make([]string, len(flags))
	for i, f := range flags {
		parts[i] = sign + f
	}
	return strings.Join(parts, " ")
}

// applyDigest sets params.Digest: the user's --digest value when the flag was
// passed, otherwise the digest captured by the same-verb readRawConfig call.
// This closes the read-modify-write race by default while still letting an
// explicit --digest override it.
func applyDigest(
	params *nodes.UpdateQemuConfigParams,
	fl interface{ Changed(string) bool },
	explicitDigest, autoDigest string,
) {
	if fl.Changed("digest") {
		params.Digest = strPtr(explicitDigest)
		return
	}
	if autoDigest != "" {
		params.Digest = strPtr(autoDigest)
	}
}

// mutationSuffix produces the trailing clause every security mutation message
// ends with (except protection, which applies immediately and has no
// suffix). When the VM is not running, the change simply applies on next
// start. When it is running: without --restart, it reports how to check/apply
// pending config; with --restart, it reboots the VM (honoring --async) and
// reports the outcome.
func mutationSuffix(cmd *cobra.Command, deps *cli.Deps, vmid, node string, restart bool) (string, error) {
	st, err := deps.API.Nodes.ListQemuStatusCurrent(cmd.Context(), node, vmid)
	if err != nil {
		return "", fmt.Errorf("get status for VM %s: %w", vmid, err)
	}
	running := st != nil && st.Status == "running"

	if !running {
		return " Change applies on next start.", nil
	}
	if !restart {
		return fmt.Sprintf(" Change is pending until the next stop/start or reboot "+
			"(see 'pve qemu config pending %s', or pass --restart).", vmid), nil
	}

	resp, err := deps.API.Nodes.CreateQemuStatusReboot(cmd.Context(), node, vmid, &nodes.CreateQemuStatusRebootParams{})
	if err != nil {
		return "", fmt.Errorf("reboot VM %s: %w", vmid, err)
	}
	upid, err := apiclient.UPIDFromRaw(*resp)
	if err != nil {
		return "", err
	}
	if deps.Async {
		return fmt.Sprintf(" Reboot task started (%s).", upid), nil
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return "", err
	}
	return " VM rebooted.", nil
}

// ---- posture parsing -------------------------------------------------------

// agentPosture is the parsed shape of the agent= config option, with unset
// sub-keys carrying their PVE API default.
type agentPosture struct {
	Enabled           bool   `json:"enabled"`
	FreezeFS          bool   `json:"freeze_fs"`
	FstrimClonedDisks bool   `json:"fstrim_cloned_disks"`
	Type              string `json:"type"`
}

// parseAgentPosture parses the raw agent= value, applying API defaults
// (freeze-fs=1, type=virtio) for sub-keys that are absent.
func parseAgentPosture(raw string) agentPosture {
	list := propstr.Parse(raw, "enabled")
	ap := agentPosture{FreezeFS: true, Type: "virtio"}
	if v, ok := list.Get("enabled"); ok {
		ap.Enabled = v == "1"
	}
	if v, ok := list.Get("freeze-fs"); ok {
		ap.FreezeFS = v == "1"
	}
	if v, ok := list.Get("fstrim_cloned_disks"); ok {
		ap.FstrimClonedDisks = v == "1"
	}
	if v, ok := list.Get("type"); ok {
		ap.Type = v
	}
	return ap
}

// agentKnownKeys is the set of agent= sub-keys isAgentAtAPIDefault knows how
// to compare against their PVE API default. Any other key present (a future
// PVE addition, or hand-edited config) must block the delete branch: deleting
// agent= would silently discard that unknown key, breaking propstr's
// unknown-key-preserving contract.
var agentKnownKeys = map[string]bool{
	"enabled": true, "freeze-fs": true, "fstrim_cloned_disks": true, "type": true,
}

// isAgentAtAPIDefault reports whether every sub-key in list is at (or absent,
// which is equivalent to) its PVE API default, meaning the whole agent=
// option can be deleted rather than sent as an explicit (redundant) string.
// It requires list to carry no key outside agentKnownKeys: an unrecognized
// sub-key means there is more in this value than the four known fields, so
// deleting it would lose that key rather than just clear defaults.
func isAgentAtAPIDefault(list propstr.List) bool {
	for _, p := range list {
		if !agentKnownKeys[p.Key] {
			return false
		}
	}
	if v, ok := list.Get("enabled"); ok && v != "0" {
		return false
	}
	if v, ok := list.Get("freeze-fs"); ok && v != "1" {
		return false
	}
	if v, ok := list.Get("fstrim_cloned_disks"); ok && v != "0" {
		return false
	}
	if v, ok := list.Get("type"); ok && v != "virtio" {
		return false
	}
	return true
}

// bootPosture is the parsed BIOS / EFI vars disk shape backing both
// `security show`'s boot.* keys and `secureboot show`.
type bootPosture struct {
	Bios            string `json:"bios"`
	EfidiskVolume   string `json:"efidisk,omitempty"`
	Efitype         string `json:"efitype"`
	PreEnrolledKeys bool   `json:"pre_enrolled_keys"`
	MsCert          string `json:"ms_cert,omitempty"`
	Size            string `json:"size,omitempty"`
	// Posture is the derived verdict: pre-enrolled, efi-no-keys,
	// ovmf-no-efidisk, or legacy-bios.
	Posture string `json:"posture"`
}

// parseBootPosture parses bios= and efidisk0= from a raw config map and
// derives the Secure Boot posture verdict.
func parseBootPosture(m map[string]any) bootPosture {
	bios, ok := rawStr(m, "bios")
	if !ok || bios == "" {
		bios = "seabios"
	}
	bp := bootPosture{Bios: bios, Efitype: "2m"}

	if efi, ok := rawStr(m, "efidisk0"); ok && efi != "" {
		list := propstr.Parse(efi, "file")
		if v, ok := list.Get("file"); ok {
			bp.EfidiskVolume = v
		}
		if v, ok := list.Get("efitype"); ok {
			bp.Efitype = v
		}
		if v, ok := list.Get("pre-enrolled-keys"); ok {
			bp.PreEnrolledKeys = v == "1"
		}
		if v, ok := list.Get("ms-cert"); ok {
			bp.MsCert = v
		}
		if v, ok := list.Get("size"); ok {
			bp.Size = v
		}
	}

	switch {
	case bp.Bios != "ovmf":
		bp.Posture = "legacy-bios"
	case bp.EfidiskVolume == "":
		bp.Posture = "ovmf-no-efidisk"
	case bp.Efitype == "4m" && bp.PreEnrolledKeys:
		bp.Posture = "pre-enrolled"
	default:
		bp.Posture = "efi-no-keys"
	}
	return bp
}

// tpmPosture is the parsed shape of the tpmstate0= config option.
type tpmPosture struct {
	Present bool   `json:"present"`
	Volume  string `json:"volume,omitempty"`
	Version string `json:"version,omitempty"`
	Size    string `json:"size,omitempty"`
}

// parseTPMPosture parses tpmstate0= from a raw config map. Version defaults
// to v1.2 (the PVE API default) when tpmstate0 carries no explicit version.
func parseTPMPosture(m map[string]any) tpmPosture {
	raw, ok := rawStr(m, "tpmstate0")
	if !ok || raw == "" {
		return tpmPosture{Present: false}
	}
	list := propstr.Parse(raw, "file")
	tp := tpmPosture{Present: true, Version: "v1.2"}
	if v, ok := list.Get("file"); ok {
		tp.Volume = v
	}
	if v, ok := list.Get("version"); ok {
		tp.Version = v
	}
	if v, ok := list.Get("size"); ok {
		tp.Size = v
	}
	return tp
}

// confidentialPosture is the parsed shape of whichever of amd-sev=/
// intel-tdx= is configured (a VM uses at most one).
type confidentialPosture struct {
	// Platform is "amd-sev", "intel-tdx", or "none".
	Platform string            `json:"platform"`
	Fields   map[string]string `json:"fields,omitempty"`
}

// sevSubkeys and tdxSubkeys are the validated boolean/int sub-options of
// amd-sev= and intel-tdx=; "type" is handled separately since it is passed
// through unvalidated (see the confidential set command).
var (
	sevSubkeys = []string{"allow-smt", "kernel-hashes", "no-debug", "no-key-sharing"}
	tdxSubkeys = []string{"attestation", "vsock-cid", "vsock-port"}
)

// parseConfidentialPosture parses whichever of amd-sev=/intel-tdx= is set.
func parseConfidentialPosture(m map[string]any) confidentialPosture {
	if raw, ok := rawStr(m, "amd-sev"); ok && raw != "" {
		return confidentialFromRaw("amd-sev", raw, sevSubkeys)
	}
	if raw, ok := rawStr(m, "intel-tdx"); ok && raw != "" {
		return confidentialFromRaw("intel-tdx", raw, tdxSubkeys)
	}
	return confidentialPosture{Platform: "none", Fields: map[string]string{}}
}

func confidentialFromRaw(platform, raw string, subkeys []string) confidentialPosture {
	list := propstr.Parse(raw, "type")
	fields := map[string]string{}
	if v, ok := list.Get("type"); ok {
		fields["type"] = v
	}
	for _, k := range subkeys {
		if v, ok := list.Get(k); ok {
			fields[k] = v
		}
	}
	return confidentialPosture{Platform: platform, Fields: fields}
}

// cpuFlagsPosture is the parsed shape of the cpu= config option's flags= list.
type cpuFlagsPosture struct {
	CPUType  string   `json:"cputype,omitempty"`
	Enabled  []string `json:"enabled"`
	Disabled []string `json:"disabled"`
}

// parseCPUFlagsPosture parses cpu= from a raw config map: cputype (the
// default-key head) and the +FLAG/-FLAG entries of its flags= sub-value.
func parseCPUFlagsPosture(m map[string]any) cpuFlagsPosture {
	cp := cpuFlagsPosture{Enabled: []string{}, Disabled: []string{}}
	raw, ok := rawStr(m, "cpu")
	if !ok || raw == "" {
		return cp
	}
	list := propstr.Parse(raw, "cputype")
	if v, ok := list.Get("cputype"); ok {
		cp.CPUType = v
	}
	if v, ok := list.Get("flags"); ok {
		for tok := range strings.SplitSeq(v, ";") {
			if tok == "" {
				continue
			}
			switch tok[0] {
			case '+':
				cp.Enabled = append(cp.Enabled, tok[1:])
			case '-':
				cp.Disabled = append(cp.Disabled, tok[1:])
			}
		}
	}
	return cp
}

// nicPosture is one parsed net[n] device, carrying enough sub-options for
// `security show`, `security list`, and `nic show`.
type nicPosture struct {
	Slot     int    `json:"slot"`
	Model    string `json:"model"`
	MAC      string `json:"mac,omitempty"`
	Bridge   string `json:"bridge,omitempty"`
	Tag      string `json:"vlan,omitempty"`
	Firewall bool   `json:"firewall"`
	LinkDown bool   `json:"link_down"`
	// Raw is the exact, unmodified net[n] value, reused verbatim by
	// `nic firewall` so every sub-option it does not touch round-trips.
	Raw string `json:"raw"`
}

var netKeyRe = regexp.MustCompile(`^net(\d+)$`)

// parseNICs enumerates every net[n] key of a raw config map, sorted by slot.
func parseNICs(m map[string]any) []nicPosture {
	nics := make([]nicPosture, 0)
	for k, v := range m {
		match := netKeyRe.FindStringSubmatch(k)
		if match == nil {
			continue
		}
		slot, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		raw := stringifyValue(v)
		list := propstr.Parse(raw, "")
		var model, mac string
		if len(list) > 0 && !list[0].Bare {
			model, mac = list[0].Key, list[0].Value
		}
		bridge, _ := list.Get("bridge")
		tag, _ := list.Get("tag")
		fw := false
		if s, ok := list.Get("firewall"); ok {
			fw = s == "1"
		}
		linkDown := false
		if s, ok := list.Get("link_down"); ok {
			linkDown = s == "1"
		}
		nics = append(nics, nicPosture{
			Slot: slot, Model: model, MAC: mac, Bridge: bridge, Tag: tag,
			Firewall: fw, LinkDown: linkDown, Raw: raw,
		})
	}
	sort.Slice(nics, func(i, j int) bool { return nics[i].Slot < nics[j].Slot })
	return nics
}

// firewallOptionsPosture is the subset of a VM's firewall options relevant to
// the security posture views.
type firewallOptionsPosture struct {
	Enable    bool   `json:"enable"`
	PolicyIn  string `json:"policy_in,omitempty"`
	PolicyOut string `json:"policy_out,omitempty"`
	Macfilter bool   `json:"macfilter"`
	Ipfilter  bool   `json:"ipfilter"`
}

func firewallOptionsFromResp(resp *nodes.ListQemuFirewallOptionsResponse) firewallOptionsPosture {
	fo := firewallOptionsPosture{}
	if resp == nil {
		return fo
	}
	if resp.Enable != nil {
		fo.Enable = resp.Enable.Bool()
	}
	if resp.PolicyIn != nil {
		fo.PolicyIn = *resp.PolicyIn
	}
	if resp.PolicyOut != nil {
		fo.PolicyOut = *resp.PolicyOut
	}
	if resp.Macfilter != nil {
		fo.Macfilter = resp.Macfilter.Bool()
	}
	if resp.Ipfilter != nil {
		fo.Ipfilter = resp.Ipfilter.Bool()
	}
	return fo
}

// detectRisks reports the risky/escape-surface settings present in a raw
// config map, in a fixed reporting order (args, hookscript, hostpci).
func detectRisks(m map[string]any) []string {
	risks := make([]string, 0)
	if v, ok := rawStr(m, "args"); ok && v != "" {
		risks = append(risks, "args")
	}
	if v, ok := rawStr(m, "hookscript"); ok && v != "" {
		risks = append(risks, "hookscript")
	}
	for k := range m {
		if hostpciKeyRe.MatchString(k) {
			risks = append(risks, "hostpci")
			break
		}
	}
	return risks
}

var hostpciKeyRe = regexp.MustCompile(`^hostpci\d+$`)

// securityPosture is the JSON shape emitted by `security show`.
type securityPosture struct {
	VMID         string                 `json:"vmid"`
	Node         string                 `json:"node"`
	Protection   bool                   `json:"protection"`
	Template     bool                   `json:"template,omitempty"`
	Agent        agentPosture           `json:"agent"`
	Boot         bootPosture            `json:"boot"`
	TPM          tpmPosture             `json:"tpm"`
	Confidential confidentialPosture    `json:"confidential"`
	CPUFlags     cpuFlagsPosture        `json:"cpuflags"`
	NICs         []nicPosture           `json:"nics"`
	Firewall     firewallOptionsPosture `json:"firewall"`
	Risks        []string               `json:"risks"`
}

// newSecurityShowCmd builds `pve qemu security show <vmid|name>`.
func newSecurityShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <vmid|name>",
		Short: "Show VM security posture (protection, boot chain, encryption, agent, firewall coverage)",
		Long: "Show the full security posture of a VM from one config read plus one firewall-options " +
			"read: the protection flag, BIOS/Secure Boot configuration, TPM state device, " +
			"confidential-computing settings, security-relevant CPU flags, guest-agent config, " +
			"per-NIC firewall coverage, and the VM firewall option summary. Risky settings " +
			"(raw QEMU args, hookscript, PCI passthrough) are called out.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			m, _, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			fwResp, err := deps.API.Nodes.ListQemuFirewallOptions(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get firewall options for VM %s on node %q: %w", vmid, node, err)
			}

			agentRaw, _ := rawStr(m, "agent")
			posture := securityPosture{
				VMID:         vmid,
				Node:         node,
				Protection:   rawBoolDefault(m, "protection", false),
				Template:     rawBoolDefault(m, "template", false),
				Agent:        parseAgentPosture(agentRaw),
				Boot:         parseBootPosture(m),
				TPM:          parseTPMPosture(m),
				Confidential: parseConfidentialPosture(m),
				CPUFlags:     parseCPUFlagsPosture(m),
				NICs:         parseNICs(m),
				Firewall:     firewallOptionsFromResp(fwResp),
				Risks:        detectRisks(m),
			}

			if deps.Format == output.FormatTable || deps.Format == output.FormatPlain {
				if s, ok := rawStr(m, "args"); ok && s != "" {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"WARNING: raw QEMU arguments are set (args=%s): they bypass all PVE validation\n", s)
				}
				if s, ok := rawStr(m, "hookscript"); ok && s != "" {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"WARNING: a hookscript is configured — it executes on the HOST during VM lifecycle events")
				}
				if posture.Boot.Posture == "ovmf-no-efidisk" {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
						"note: bios=ovmf without an efidisk0 — EFI variables (including Secure Boot "+
							"state) will not persist across restarts")
				}
			}

			single := map[string]string{
				"vmid":       vmid,
				"node":       node,
				"protection": strconv.FormatBool(posture.Protection),
			}
			if posture.Template {
				single["template"] = "true"
			}
			single["agent.enabled"] = strconv.FormatBool(posture.Agent.Enabled)
			single["agent.freeze_fs"] = strconv.FormatBool(posture.Agent.FreezeFS)
			single["agent.fstrim_cloned_disks"] = strconv.FormatBool(posture.Agent.FstrimClonedDisks)
			single["agent.type"] = posture.Agent.Type
			single["boot.bios"] = posture.Boot.Bios
			if posture.Boot.EfidiskVolume != "" {
				single["boot.efidisk"] = posture.Boot.EfidiskVolume
				single["boot.efitype"] = posture.Boot.Efitype
				single["boot.pre_enrolled_keys"] = strconv.FormatBool(posture.Boot.PreEnrolledKeys)
			}
			if posture.Boot.MsCert != "" {
				single["boot.ms_cert"] = posture.Boot.MsCert
			}
			single["boot.secureboot"] = posture.Boot.Posture
			single["tpm.present"] = strconv.FormatBool(posture.TPM.Present)
			if posture.TPM.Present {
				single["tpm.version"] = posture.TPM.Version
				single["tpm.volume"] = posture.TPM.Volume
			}
			single["confidential.platform"] = posture.Confidential.Platform
			for k, v := range posture.Confidential.Fields {
				single["confidential."+strings.ReplaceAll(k, "-", "_")] = v
			}
			if len(posture.CPUFlags.Enabled) > 0 {
				single["cpuflags.enabled"] = joinSigned(posture.CPUFlags.Enabled, "+")
			}
			if len(posture.CPUFlags.Disabled) > 0 {
				single["cpuflags.disabled"] = joinSigned(posture.CPUFlags.Disabled, "-")
			}
			for _, n := range posture.NICs {
				single[fmt.Sprintf("nic.net%d.firewall", n.Slot)] = strconv.FormatBool(n.Firewall)
			}
			single["firewall.enable"] = strconv.FormatBool(posture.Firewall.Enable)
			single["firewall.policy_in"] = posture.Firewall.PolicyIn
			single["firewall.policy_out"] = posture.Firewall.PolicyOut
			single["firewall.macfilter"] = strconv.FormatBool(posture.Firewall.Macfilter)
			single["firewall.ipfilter"] = strconv.FormatBool(posture.Firewall.Ipfilter)
			if len(posture.Risks) > 0 {
				single["risks"] = strings.Join(posture.Risks, ",")
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: posture}, deps.Format)
		},
	}
}

// qemuResource is the minimal decoded shape of one cluster/resources entry
// used by `security list` to enumerate VMs and the nodes they run on.
type qemuResource struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Node string `json:"node"`
	VMID *int64 `json:"vmid"`
	ID   string `json:"id"`
}

func (r qemuResource) vmidString() string {
	if r.VMID != nil {
		return strconv.FormatInt(*r.VMID, 10)
	}
	if i := strings.LastIndex(r.ID, "/"); i >= 0 {
		return r.ID[i+1:]
	}
	return ""
}

// securityListRow is one row of the `security list` audit table. Err is set
// instead of the posture fields when the per-VM config read failed; the row
// still renders (with placeholder posture cells) rather than aborting the
// whole audit.
type securityListRow struct {
	VMID       string   `json:"vmid"`
	Name       string   `json:"name"`
	Node       string   `json:"node"`
	Protection bool     `json:"protection"`
	SecureBoot string   `json:"secureboot"`
	TPM        string   `json:"tpm"`
	Conf       string   `json:"conf"`
	Agent      bool     `json:"agent"`
	NICFWOn    int      `json:"nicfw_on"`
	NICFWTotal int      `json:"nicfw_total"`
	Risks      []string `json:"risks"`
	Err        string   `json:"error,omitempty"`
}

// newSecurityListCmd builds `pve qemu security list`.
func newSecurityListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Security posture summary for all VMs",
		Long: "Audit the security posture of every VM in the cluster (or on --node): protection, " +
			"Secure Boot / TPM presence, confidential computing, guest agent, per-NIC firewall " +
			"coverage, and risky settings (raw args, hookscript, PCI passthrough). VMs with a " +
			"risky setting are flagged with '!' and sorted first. This is a cluster resources " +
			"scan plus one config read per VM.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			ctx := cmd.Context()

			typeVM := "vm"
			resp, err := deps.API.Cluster.ListResources(ctx, &pvecluster.ListResourcesParams{Type: &typeVM})
			if err != nil {
				return fmt.Errorf("list cluster resources: %w", err)
			}

			rows := make([]securityListRow, 0)
			if resp != nil {
				for _, raw := range *resp {
					var r qemuResource
					if err := json.Unmarshal(raw, &r); err != nil {
						return fmt.Errorf("decode cluster resource entry: %w", err)
					}
					if r.Type != cli.GuestQemu {
						continue
					}
					if deps.Node != "" && r.Node != deps.Node {
						continue
					}
					vmid := r.vmidString()
					m, _, err := readRawConfig(ctx, deps, r.Node, vmid)
					if err != nil {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"warning: skipping VM %s security posture (%s): %v\n", vmid, r.Node, err)
						rows = append(rows, securityListRow{
							VMID:       vmid,
							Name:       r.Name,
							Node:       r.Node,
							SecureBoot: "?",
							TPM:        "?",
							Conf:       "?",
							Err:        err.Error(),
						})
						continue
					}

					nics := parseNICs(m)
					fwOn := 0
					for _, n := range nics {
						if n.Firewall {
							fwOn++
						}
					}
					boot := parseBootPosture(m)
					tpm := parseTPMPosture(m)
					conf := parseConfidentialPosture(m)
					agentRaw, _ := rawStr(m, "agent")
					ap := parseAgentPosture(agentRaw)

					confStr := "-"
					if conf.Platform != "none" {
						t := conf.Fields["type"]
						if t == "" {
							t = "?"
						}
						confStr = conf.Platform + ":" + t
					}
					tpmStr := "-"
					if tpm.Present {
						tpmStr = tpm.Version
					}

					rows = append(rows, securityListRow{
						VMID:       vmid,
						Name:       r.Name,
						Node:       r.Node,
						Protection: rawBoolDefault(m, "protection", false),
						SecureBoot: boot.Posture,
						TPM:        tpmStr,
						Conf:       confStr,
						Agent:      ap.Enabled,
						NICFWOn:    fwOn,
						NICFWTotal: len(nics),
						Risks:      detectRisks(m),
					})
				}
			}

			// Risky rows first (an unreadable row counts as risky too, since it
			// hides whatever posture it would have reported), then numeric VMID
			// order within each group.
			sort.SliceStable(rows, func(i, j int) bool {
				iRisky := len(rows[i].Risks) > 0 || rows[i].Err != ""
				jRisky := len(rows[j].Risks) > 0 || rows[j].Err != ""
				if iRisky != jRisky {
					return iRisky
				}
				vi, _ := strconv.ParseInt(rows[i].VMID, 10, 64)
				vj, _ := strconv.ParseInt(rows[j].VMID, 10, 64)
				return vi < vj
			})

			res := output.Result{
				Headers: []string{"VMID", "NAME", "NODE", "PROTECTION", "SECUREBOOT", "TPM", "CONF", "AGENT", "NICFW", "RISKS"},
				Raw:     rows,
			}
			for _, r := range rows {
				vmidCell := r.VMID
				if len(r.Risks) > 0 || r.Err != "" {
					vmidCell = "! " + r.VMID
				}
				risksCell := strings.Join(r.Risks, ",")
				if r.Err != "" {
					risksCell = "error: " + r.Err
				}
				nicfw := "-"
				if r.NICFWTotal > 0 {
					nicfw = fmt.Sprintf("%d/%d", r.NICFWOn, r.NICFWTotal)
				}
				res.Rows = append(res.Rows, []string{
					vmidCell, r.Name, r.Node,
					strconv.FormatBool(r.Protection),
					r.SecureBoot, r.TPM, r.Conf,
					strconv.FormatBool(r.Agent),
					nicfw,
					risksCell,
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// ---- protection -------------------------------------------------------------

// newSecurityProtectionCmd builds `pve qemu security protection`.
func newSecurityProtectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "protection",
		Short: "Manage the VM protection flag (blocks destroy and disk removal)",
	}
	cmd.AddCommand(newSecurityProtectionSetCmd(true), newSecurityProtectionSetCmd(false))
	return cmd
}

// newSecurityProtectionSetCmd builds `protection enable` (enable=true) or
// `protection disable` (enable=false). The protection flag is a plain typed
// field with no read-modify-write merge needed, and applies immediately —
// there is no restart suffix.
func newSecurityProtectionSetCmd(enable bool) *cobra.Command {
	var digest string

	use, short, long := "enable <vmid|name>",
		"Set the protection flag (blocks 'qemu delete' and disk removal)",
		"Set the VM protection flag. While set, PVE refuses the remove-VM and remove-disk "+
			"operations. Applies immediately; no restart needed."
	if !enable {
		use, short, long = "disable <vmid|name>",
			"Clear the protection flag (re-enables destroy and disk removal)",
			"Clear the VM protection flag, restoring the default remove-VM/remove-disk behavior. "+
				"Applies immediately; no restart needed."
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			m, autoDigest, err := readRawConfig(cmd.Context(), deps, node, vmid)
			if err != nil {
				return err
			}
			current := rawBoolDefault(m, "protection", false)
			if current == enable {
				verb := "already protected"
				if !enable {
					verb = "already unprotected"
				}
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: fmt.Sprintf("VM %s is %s; no change.", vmid, verb)}, deps.Format)
			}

			if !enable {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"WARNING: clearing the protection flag re-enables destroy and disk-removal operations for VM %s\n", vmid)
			}

			params := &nodes.UpdateQemuConfigParams{Protection: boolPtr(enable)}
			applyDigest(params, fl, digest, autoDigest)

			if err := deps.API.Nodes.UpdateQemuConfig(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("update protection for VM %s on node %q: %w", vmid, node, err)
			}

			verb := "enabled"
			if !enable {
				verb = "disabled"
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Message: fmt.Sprintf("VM %s protection %s.", vmid, verb),
					Raw:     map[string]any{"vmid": vmid, "node": node, "protection": enable},
				}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	return cmd
}
