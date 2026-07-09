package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pdmautoinstall "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/autoinstall"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// validAutoInstallRebootModes are the reboot-mode enum values accepted by
// `prepared add`/`prepared update` --reboot-mode, per the PDM API schema
// (pdm-apidoc.json POST /auto-install/prepared parameters.reboot-mode).
var validAutoInstallRebootModes = []string{"reboot", "power-off"}

// validAutoInstallDiskFilterMatch are the disk-filter-match enum values
// accepted by --disk-filter-match, per the PDM API schema (pdm-apidoc.json
// POST /auto-install/prepared parameters.disk-filter-match).
var validAutoInstallDiskFilterMatch = []string{"any", "all"}

// autoinstallTokenSecretKeys are the auto-install token fields that must
// never be echoed back on `token ls`. GET /auto-install/tokens's item schema
// (comment, created-by, enabled, expire-at, id) does not currently declare a
// "secret" property, so there is nothing to strip today, but the create/
// update responses carry the secret under that exact key
// (CreateTokensResponse.Secret, UpdateTokensResponse.Secret,
// autoinstall_gen.go:630-636, :713-719, v3.6.0); stripAutoInstallTokenSecrets
// defends `token ls` against a future API revision echoing it back, mirroring
// realm_openid.go's defensive stripRealmOpenidSecrets.
var autoinstallTokenSecretKeys = []string{"secret"}

// stripAutoInstallTokenSecrets deletes every key in autoinstallTokenSecretKeys
// from fields, in place.
func stripAutoInstallTokenSecrets(fields map[string]any) {
	for _, k := range autoinstallTokenSecretKeys {
		delete(fields, k)
	}
}

// autoInstallInstallationSecretKeys are the installation-record fields that
// must never be echoed back on `installation ls`. post-hook-token is declared
// optional in GET /auto-install/installations's item schema, but its
// description states it is "[p]ersisted on disk only; stripped before being
// returned over the API" — the server is not expected to ever send it, but
// stripAutoInstallInstallationSecrets strips it defensively in case a future
// revision does, mirroring realm_openid.go's stripRealmOpenidSecrets for a
// field the schema declares present but that should stay write-only.
var autoInstallInstallationSecretKeys = []string{"post-hook-token"}

// stripAutoInstallInstallationSecrets deletes every key in
// autoInstallInstallationSecretKeys from fields, in place.
func stripAutoInstallInstallationSecrets(fields map[string]any) {
	for _, k := range autoInstallInstallationSecretKeys {
		delete(fields, k)
	}
}

// newAutoInstallCmd builds `pmx pdm auto-install` — inspect automated
// installation records, manage prepared auto-installer answer configurations,
// and manage the tokens used to authenticate automated installation requests
// (/auto-install).
//
// POST /auto-install/answer (CreateAnswer) and POST
// /auto-install/installations/{uuid}/post-hook (CreateInstallationsPostHook)
// are intentionally not exposed here: both are called by the
// proxmox-auto-installer client itself during provisioning of a new machine,
// not by an administrator operating this Proxmox Datacenter Manager.
func newAutoInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto-install",
		Short: "Manage automated installations, prepared answers, and tokens",
		Long: "Inspect automated-installation records, manage prepared auto-installer " +
			"answer configurations, and manage tokens used to authenticate automated " +
			"installation requests (/auto-install).\n\n" +
			"The installer-machine-facing endpoints POST /auto-install/answer and POST " +
			"/auto-install/installations/{uuid}/post-hook are intentionally not exposed " +
			"here: they are called by the proxmox-auto-installer client itself during " +
			"provisioning, not by an administrator operating this Proxmox Datacenter Manager.",
	}
	cmd.AddCommand(newAutoInstallInstallationCmd(), newAutoInstallPreparedCmd(), newAutoInstallTokenCmd())
	return cmd
}

// --- installation ------------------------------------------------------------

// newAutoInstallInstallationCmd builds `pmx pdm auto-install installation` —
// inspect and remove automated installation records
// (/auto-install/installations).
func newAutoInstallInstallationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "installation",
		Short: "Inspect and remove automated installation records",
		Long:  "List and remove records of automated installations received by this Proxmox Datacenter Manager.",
	}
	cmd.AddCommand(newAutoInstallInstallationLsCmd(), newAutoInstallInstallationDeleteCmd())
	return cmd
}

// autoInstallInstallationEntry is the decoded shape of one element of
// GET /auto-install/installations.
type autoInstallInstallationEntry struct {
	Uuid       string  `json:"uuid"`
	Status     string  `json:"status"`
	ReceivedAt int64   `json:"received-at"`
	AnswerId   *string `json:"answer-id,omitempty"`
}

// newAutoInstallInstallationLsCmd builds `pmx pdm auto-install installation
// ls` — list every automated installation record (GET
// /auto-install/installations).
func newAutoInstallInstallationLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List automated installation records",
		Long: "List every automated installation record received by this Proxmox " +
			"Datacenter Manager (GET /auto-install/installations).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.AutoInstall.ListInstallations(cmd.Context())
			if err != nil {
				return fmt.Errorf("list installations: %w", err)
			}

			items := rawItemsOf(resp)
			type installationRow struct {
				entry autoInstallInstallationEntry
				raw   map[string]any
			}
			table := make([]installationRow, 0, len(items))

			for _, raw := range items {
				var e autoInstallInstallationEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode installation entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode installation entry: %w", err)
				}
				stripAutoInstallInstallationSecrets(m)

				table = append(table, installationRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Uuid < table[j].entry.Uuid })

			headers := []string{"UUID", "STATUS", "RECEIVED-AT", "ANSWER-ID"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Uuid, e.Status, strconv.FormatInt(e.ReceivedAt, 10), strPtrString(e.AnswerId),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newAutoInstallInstallationDeleteCmd builds `pmx pdm auto-install
// installation delete <uuid>` — remove an installation record (DELETE
// /auto-install/installations/{uuid}).
func newAutoInstallInstallationDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <uuid>",
		Short: "Remove an automated installation record",
		Long: "Remove an automated installation record (DELETE " +
			"/auto-install/installations/{uuid}). This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			uuid := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete installation %q without confirmation: pass --yes/-y", uuid)
			}

			err := deps.PDM.AutoInstall.DeleteInstallations(cmd.Context(), uuid)
			if err != nil {
				return fmt.Errorf("delete installation %q: %w", uuid, err)
			}

			res := output.Result{Message: fmt.Sprintf("Installation %q deleted.", uuid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// --- prepared ------------------------------------------------------------

// newAutoInstallPreparedCmd builds `pmx pdm auto-install prepared` — manage
// prepared auto-installer answer configurations (/auto-install/prepared).
func newAutoInstallPreparedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prepared",
		Short: "Manage prepared auto-installer answer configurations",
		Long:  "List, inspect, create, update, and delete prepared auto-installer answer configurations.",
	}
	cmd.AddCommand(
		newAutoInstallPreparedLsCmd(),
		newAutoInstallPreparedShowCmd(),
		newAutoInstallPreparedAddCmd(),
		newAutoInstallPreparedUpdateCmd(),
		newAutoInstallPreparedDeleteCmd(),
	)
	return cmd
}

// autoInstallPreparedEntry is the decoded shape of one element of
// GET /auto-install/prepared: the same object shape CreatePrepared /
// UpdatePrepared return under their "config" field.
type autoInstallPreparedEntry struct {
	Id         string `json:"id"`
	Country    string `json:"country"`
	DiskMode   string `json:"disk-mode"`
	Fqdn       string `json:"fqdn"`
	IsDefault  *bool  `json:"is-default,omitempty"`
	RebootMode string `json:"reboot-mode"`
	Timezone   string `json:"timezone"`
}

// newAutoInstallPreparedLsCmd builds `pmx pdm auto-install prepared ls` —
// list every prepared auto-installer answer configuration (GET
// /auto-install/prepared).
func newAutoInstallPreparedLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List prepared auto-installer answer configurations",
		Long:  "List every prepared auto-installer answer configuration (GET /auto-install/prepared).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.AutoInstall.ListPrepared(cmd.Context())
			if err != nil {
				return fmt.Errorf("list prepared answers: %w", err)
			}

			items := rawItemsOf(resp)
			type preparedRow struct {
				entry autoInstallPreparedEntry
				raw   map[string]any
			}
			table := make([]preparedRow, 0, len(items))

			for _, raw := range items {
				var e autoInstallPreparedEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode prepared answer entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode prepared answer entry: %w", err)
				}

				table = append(table, preparedRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Id < table[j].entry.Id })

			headers := []string{"ID", "COUNTRY", "DISK-MODE", "FQDN", "REBOOT-MODE", "TIMEZONE", "DEFAULT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Id, e.Country, e.DiskMode, e.Fqdn, e.RebootMode, e.Timezone, boolPtrString(e.IsDefault),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newAutoInstallPreparedShowCmd builds `pmx pdm auto-install prepared show
// <id>` — show a single prepared auto-installer answer configuration (GET
// /auto-install/prepared/{id}).
func newAutoInstallPreparedShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a prepared auto-installer answer configuration",
		Long: "Show every populated field of a single prepared auto-installer answer " +
			"configuration (GET /auto-install/prepared/{id}).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			resp, err := deps.PDM.AutoInstall.GetPrepared(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get prepared answer %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("get prepared answer %q: decode response: %w", id, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// autoInstallPreparedFlags collects the prepared-answer attribute flags
// shared by `prepared add` and `prepared update`. Every field maps onto the
// CreatePreparedParams / UpdatePreparedParams field of the same name.
//
// filesystem, disk-filter, netdev-filter, target-filter, and
// template-counters are json.RawMessage-typed nested objects in both
// generated params structs — filesystem is even a required field of
// CreatePreparedParams — so these five flags carry literal JSON text for
// their respective nested object rather than exposing individual sub-fields.
type autoInstallPreparedFlags struct {
	authorizedTokens        []string
	cidr                    string
	country                 string
	diskFilter              string
	diskFilterMatch         string
	diskList                []string
	diskMode                string
	dns                     string
	filesystem              string
	fqdn                    string
	gateway                 string
	isDefault               bool
	keyboard                string
	mailto                  string
	netdevFilter            string
	netifNamePinningEnabled bool
	postHookBaseUrl         string
	postHookCertFp          string
	rebootMode              string
	rebootOnError           bool
	rootPassword            string
	rootPasswordHashed      string
	rootSshKeys             []string
	subscriptionKey         string
	targetFilter            string
	templateCounters        string
	timezone                string
	useDhcpFqdn             bool
	useDhcpNetwork          bool

	// update-only
	del    []string
	digest string
}

// registerCommon binds every prepared-answer attribute flag accepted by both
// `prepared add` and `prepared update`. Core fields (country, disk-mode,
// fqdn, keyboard, mailto, timezone, filesystem) are required on add and
// optional on update; the caller marks them required after calling this on
// the add command.
func (pf *autoInstallPreparedFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArrayVar(&pf.authorizedTokens, "authorized-token", nil,
		"token ID authorized to retrieve this answer (repeatable)")
	f.StringVar(&pf.cidr, "cidr", "", "IP address and netmask if not using DHCP (literal or MiniJinja template)")
	f.StringVar(&pf.country, "country", "", "two-letter country code to use for apt mirrors")
	f.StringVar(&pf.diskFilter, "disk-filter", "",
		"JSON object filtering udev properties to select disks dynamically")
	f.StringVar(&pf.diskFilterMatch, "disk-filter-match", "",
		"whether --disk-filter filters must all match or any one is enough: any|all")
	f.StringArrayVar(&pf.diskList, "disk-list", nil, "raw disk identifier to use for the root filesystem (repeatable)")
	f.StringVar(&pf.diskMode, "disk-mode", "",
		"whether to use --disk-list (fixed) or --disk-filter (dynamic udev selection)")
	f.StringVar(&pf.dns, "dns", "", "DNS server address if not using DHCP (literal or MiniJinja template)")
	f.StringVar(&pf.filesystem, "filesystem", "", "JSON object of filesystem-specific options for the root disk")
	f.StringVar(&pf.fqdn, "fqdn", "", "FQDN to set for the installed system (supports MiniJinja templating)")
	f.StringVar(&pf.gateway, "gateway", "", "gateway if not using DHCP (literal or MiniJinja template)")
	f.BoolVar(&pf.isDefault, "default", false,
		"make this the default answer (there can only be one; ignored if --target-filter is set)")
	f.StringVar(&pf.keyboard, "keyboard", "", "keyboard layout of the system")
	f.StringVar(&pf.mailto, "mailto", "", "e-mail address for installation notifications")
	f.StringVar(&pf.netdevFilter, "netdev-filter", "",
		"JSON object filtering network devices to select the management interface")
	f.BoolVar(&pf.netifNamePinningEnabled, "netif-name-pinning-enabled", false, "enable network interface name pinning")
	f.StringVar(&pf.postHookBaseUrl, "post-hook-base-url", "",
		"HTTP(s) base URL (with optional port) for the post-installation hook")
	f.StringVar(&pf.postHookCertFp, "post-hook-cert-fp", "", "sha256 certificate fingerprint of the post-hook URL")
	f.StringVar(&pf.rebootMode, "reboot-mode", "reboot", "action to take after installation completes: reboot|power-off")
	f.BoolVar(&pf.rebootOnError, "reboot-on-error", false, "reboot the machine if an error occurred during installation")
	f.StringVar(&pf.rootPassword, "root-password", "", "root password")
	f.StringVar(&pf.rootPasswordHashed, "root-password-hashed", "", "pre-hashed password for the root PAM account")
	f.StringArrayVar(&pf.rootSshKeys, "root-ssh-key", nil, "public SSH key to authorize for root (repeatable)")
	f.StringVar(&pf.subscriptionKey, "subscription-key", "", "Proxmox subscription key for the installed product")
	f.StringVar(&pf.targetFilter, "target-filter", "",
		"JSON object of JSON-Pointer/glob filters matching this configuration against incoming installations")
	f.StringVar(&pf.templateCounters, "template-counters", "",
		"JSON object of auto-incrementing counters for MiniJinja templating")
	f.StringVar(&pf.timezone, "timezone", "", "timezone to set on the new system")
	f.BoolVar(&pf.useDhcpFqdn, "use-dhcp-fqdn", false, "use the FQDN from the DHCP lease instead of --fqdn")
	f.BoolVar(&pf.useDhcpNetwork, "use-dhcp-network", false, "use the network configuration from the DHCP lease")
}

// registerUpdateOnly binds the update-only --delete/--digest flags.
func (pf *autoInstallPreparedFlags) registerUpdateOnly(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArrayVar(&pf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&pf.digest, "digest", "", "only update if the current config digest matches")
}

// jsonFlags returns the JSON-text flag names mapped to their current value,
// for shared not-empty-and-valid-JSON validation between add and update.
func (pf *autoInstallPreparedFlags) jsonFlags() map[string]string {
	return map[string]string{
		"filesystem":        pf.filesystem,
		"disk-filter":       pf.diskFilter,
		"netdev-filter":     pf.netdevFilter,
		"target-filter":     pf.targetFilter,
		"template-counters": pf.templateCounters,
	}
}

// validate checks --reboot-mode, --disk-filter-match, and every JSON-text
// flag that was explicitly set (or, for add, is non-empty because it carries
// a required value). verb/noun feed the error message ("add prepared answer
// %q" / "update prepared answer %q").
func (pf *autoInstallPreparedFlags) validate(id, verb string) error {
	if pf.rebootMode != "" && !stringInSlice(pf.rebootMode, validAutoInstallRebootModes) {
		return fmt.Errorf("%s prepared answer %q: --reboot-mode must be one of %s (got %q)",
			verb, id, strings.Join(validAutoInstallRebootModes, ", "), pf.rebootMode)
	}
	if pf.diskFilterMatch != "" && !stringInSlice(pf.diskFilterMatch, validAutoInstallDiskFilterMatch) {
		return fmt.Errorf("%s prepared answer %q: --disk-filter-match must be one of %s (got %q)",
			verb, id, strings.Join(validAutoInstallDiskFilterMatch, ", "), pf.diskFilterMatch)
	}
	for name, text := range pf.jsonFlags() {
		if text != "" && !json.Valid([]byte(text)) {
			return fmt.Errorf("%s prepared answer %q: --%s is not valid JSON", verb, id, name)
		}
	}
	return nil
}

// toCreateParams builds the CreatePreparedParams for `prepared add`. Core
// fields (country, disk-mode, fqdn, keyboard, mailto, timezone, filesystem,
// reboot-mode, and the four required booleans) have no "omitempty" in
// CreatePreparedParams, so they are always sent; every other field is
// forwarded only when its flag was explicitly set.
func (pf *autoInstallPreparedFlags) toCreateParams(cmd *cobra.Command, id string) *pdmautoinstall.CreatePreparedParams {
	params := &pdmautoinstall.CreatePreparedParams{
		Id:                      id,
		Country:                 pf.country,
		DiskMode:                pf.diskMode,
		Fqdn:                    pf.fqdn,
		Keyboard:                pf.keyboard,
		Mailto:                  pf.mailto,
		Timezone:                pf.timezone,
		Filesystem:              json.RawMessage(pf.filesystem),
		RebootMode:              pf.rebootMode,
		NetifNamePinningEnabled: pf.netifNamePinningEnabled,
		RebootOnError:           pf.rebootOnError,
		UseDhcpFqdn:             pf.useDhcpFqdn,
		UseDhcpNetwork:          pf.useDhcpNetwork,
	}
	pf.applyOptionalCreate(cmd, params)
	return params
}

// toUpdateParams builds the UpdatePreparedParams for `prepared update`.
// Every field, including the core ones, is forwarded only when its flag was
// explicitly set.
func (pf *autoInstallPreparedFlags) toUpdateParams(cmd *cobra.Command) *pdmautoinstall.UpdatePreparedParams {
	params := &pdmautoinstall.UpdatePreparedParams{}
	fl := cmd.Flags()

	if fl.Changed("country") {
		params.Country = strPtr(pf.country)
	}
	if fl.Changed("disk-mode") {
		params.DiskMode = strPtr(pf.diskMode)
	}
	if fl.Changed("fqdn") {
		params.Fqdn = strPtr(pf.fqdn)
	}
	if fl.Changed("keyboard") {
		params.Keyboard = strPtr(pf.keyboard)
	}
	if fl.Changed("mailto") {
		params.Mailto = strPtr(pf.mailto)
	}
	if fl.Changed("timezone") {
		params.Timezone = strPtr(pf.timezone)
	}
	if fl.Changed("filesystem") {
		params.Filesystem = json.RawMessage(pf.filesystem)
	}
	if fl.Changed("reboot-mode") {
		params.RebootMode = strPtr(pf.rebootMode)
	}
	if fl.Changed("netif-name-pinning-enabled") {
		params.NetifNamePinningEnabled = boolPtr(pf.netifNamePinningEnabled)
	}
	if fl.Changed("reboot-on-error") {
		params.RebootOnError = boolPtr(pf.rebootOnError)
	}
	if fl.Changed("use-dhcp-fqdn") {
		params.UseDhcpFqdn = boolPtr(pf.useDhcpFqdn)
	}
	if fl.Changed("use-dhcp-network") {
		params.UseDhcpNetwork = boolPtr(pf.useDhcpNetwork)
	}
	pf.applyOptionalUpdate(cmd, params)
	if fl.Changed("delete") {
		params.Delete = pf.del
	}
	if fl.Changed("digest") {
		params.Digest = strPtr(pf.digest)
	}
	return params
}

// applyOptionalCreate forwards every optional field into params only when
// its flag was explicitly set. Field-for-field mirror of
// applyOptionalUpdate, duplicated because CreatePreparedParams and
// UpdatePreparedParams are distinct generated types.
func (pf *autoInstallPreparedFlags) applyOptionalCreate(cmd *cobra.Command, params *pdmautoinstall.CreatePreparedParams) {
	fl := cmd.Flags()
	if fl.Changed("authorized-token") {
		params.AuthorizedTokens = pf.authorizedTokens
	}
	if fl.Changed("cidr") {
		params.Cidr = strPtr(pf.cidr)
	}
	if fl.Changed("disk-filter") {
		params.DiskFilter = json.RawMessage(pf.diskFilter)
	}
	if fl.Changed("disk-filter-match") {
		params.DiskFilterMatch = strPtr(pf.diskFilterMatch)
	}
	if fl.Changed("disk-list") {
		params.DiskList = pf.diskList
	}
	if fl.Changed("dns") {
		params.Dns = strPtr(pf.dns)
	}
	if fl.Changed("default") {
		params.IsDefault = boolPtr(pf.isDefault)
	}
	if fl.Changed("netdev-filter") {
		params.NetdevFilter = json.RawMessage(pf.netdevFilter)
	}
	if fl.Changed("post-hook-base-url") {
		params.PostHookBaseUrl = strPtr(pf.postHookBaseUrl)
	}
	if fl.Changed("post-hook-cert-fp") {
		params.PostHookCertFp = strPtr(pf.postHookCertFp)
	}
	if fl.Changed("root-password") {
		params.RootPassword = strPtr(pf.rootPassword)
	}
	if fl.Changed("root-password-hashed") {
		params.RootPasswordHashed = strPtr(pf.rootPasswordHashed)
	}
	if fl.Changed("root-ssh-key") {
		params.RootSshKeys = pf.rootSshKeys
	}
	if fl.Changed("subscription-key") {
		params.SubscriptionKey = strPtr(pf.subscriptionKey)
	}
	if fl.Changed("target-filter") {
		params.TargetFilter = json.RawMessage(pf.targetFilter)
	}
	if fl.Changed("template-counters") {
		params.TemplateCounters = json.RawMessage(pf.templateCounters)
	}
}

// applyOptionalUpdate is applyOptionalCreate's UpdatePreparedParams
// counterpart.
func (pf *autoInstallPreparedFlags) applyOptionalUpdate(cmd *cobra.Command, params *pdmautoinstall.UpdatePreparedParams) {
	fl := cmd.Flags()
	if fl.Changed("authorized-token") {
		params.AuthorizedTokens = pf.authorizedTokens
	}
	if fl.Changed("cidr") {
		params.Cidr = strPtr(pf.cidr)
	}
	if fl.Changed("disk-filter") {
		params.DiskFilter = json.RawMessage(pf.diskFilter)
	}
	if fl.Changed("disk-filter-match") {
		params.DiskFilterMatch = strPtr(pf.diskFilterMatch)
	}
	if fl.Changed("disk-list") {
		params.DiskList = pf.diskList
	}
	if fl.Changed("dns") {
		params.Dns = strPtr(pf.dns)
	}
	if fl.Changed("default") {
		params.IsDefault = boolPtr(pf.isDefault)
	}
	if fl.Changed("netdev-filter") {
		params.NetdevFilter = json.RawMessage(pf.netdevFilter)
	}
	if fl.Changed("post-hook-base-url") {
		params.PostHookBaseUrl = strPtr(pf.postHookBaseUrl)
	}
	if fl.Changed("post-hook-cert-fp") {
		params.PostHookCertFp = strPtr(pf.postHookCertFp)
	}
	if fl.Changed("root-password") {
		params.RootPassword = strPtr(pf.rootPassword)
	}
	if fl.Changed("root-password-hashed") {
		params.RootPasswordHashed = strPtr(pf.rootPasswordHashed)
	}
	if fl.Changed("root-ssh-key") {
		params.RootSshKeys = pf.rootSshKeys
	}
	if fl.Changed("subscription-key") {
		params.SubscriptionKey = strPtr(pf.subscriptionKey)
	}
	if fl.Changed("target-filter") {
		params.TargetFilter = json.RawMessage(pf.targetFilter)
	}
	if fl.Changed("template-counters") {
		params.TemplateCounters = json.RawMessage(pf.templateCounters)
	}
}

// newAutoInstallPreparedAddCmd builds `pmx pdm auto-install prepared add
// <id>` — create a prepared auto-installer answer configuration (POST
// /auto-install/prepared). --filesystem, --disk-filter, --netdev-filter,
// --target-filter, and --template-counters each take literal JSON text for
// their respective CreatePreparedParams json.RawMessage field.
func newAutoInstallPreparedAddCmd() *cobra.Command {
	var pf autoInstallPreparedFlags
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create a prepared auto-installer answer configuration",
		Long: "Create a new prepared auto-installer answer configuration (POST " +
			"/auto-install/prepared). --filesystem, --disk-filter, --netdev-filter, " +
			"--target-filter, and --template-counters each take literal JSON text for " +
			"their respective nested object; --filesystem is required.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if err := pf.validate(id, "add"); err != nil {
				return err
			}

			params := pf.toCreateParams(cmd, id)

			resp, err := deps.PDM.AutoInstall.CreatePrepared(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("add prepared answer %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("add prepared answer %q: decode response: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Prepared answer %q created.", id), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pf.registerCommon(cmd)
	cli.MustMarkRequired(cmd, "country")
	cli.MustMarkRequired(cmd, "disk-mode")
	cli.MustMarkRequired(cmd, "fqdn")
	cli.MustMarkRequired(cmd, "keyboard")
	cli.MustMarkRequired(cmd, "mailto")
	cli.MustMarkRequired(cmd, "timezone")
	cli.MustMarkRequired(cmd, "filesystem")
	return cmd
}

// newAutoInstallPreparedUpdateCmd builds `pmx pdm auto-install prepared
// update <id>` — update a prepared auto-installer answer configuration (PUT
// /auto-install/prepared/{id}). Only flags explicitly set are sent; use
// --delete to reset properties to their default.
func newAutoInstallPreparedUpdateCmd() *cobra.Command {
	var pf autoInstallPreparedFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a prepared auto-installer answer configuration",
		Long: "Update an existing prepared auto-installer answer configuration (PUT " +
			"/auto-install/prepared/{id}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead. --filesystem, " +
			"--disk-filter, --netdev-filter, --target-filter, and --template-counters " +
			"each take literal JSON text for their respective nested object.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update prepared answer %q: no changes given: pass at least one flag", id)
			}

			if err := pf.validate(id, "update"); err != nil {
				return err
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range pf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := pf.toUpdateParams(cmd)

			resp, err := deps.PDM.AutoInstall.UpdatePrepared(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update prepared answer %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("update prepared answer %q: decode response: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Prepared answer %q updated.", id), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pf.registerCommon(cmd)
	pf.registerUpdateOnly(cmd)
	return cmd
}

// newAutoInstallPreparedDeleteCmd builds `pmx pdm auto-install prepared
// delete <id>` — remove a prepared auto-installer answer configuration
// (DELETE /auto-install/prepared/{id}).
func newAutoInstallPreparedDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a prepared auto-installer answer configuration",
		Long: "Delete a prepared auto-installer answer configuration (DELETE " +
			"/auto-install/prepared/{id}). This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete prepared answer %q without confirmation: pass --yes/-y", id)
			}

			err := deps.PDM.AutoInstall.DeletePrepared(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("delete prepared answer %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Prepared answer %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// --- token ------------------------------------------------------------

// newAutoInstallTokenCmd builds `pmx pdm auto-install token` — manage the
// tokens used to authenticate automated installation requests
// (/auto-install/tokens).
func newAutoInstallTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage automated-installation authentication tokens",
		Long:  "List, create, update, and delete tokens used to authenticate automated installation requests.",
	}
	cmd.AddCommand(
		newAutoInstallTokenLsCmd(),
		newAutoInstallTokenAddCmd(),
		newAutoInstallTokenUpdateCmd(),
		newAutoInstallTokenDeleteCmd(),
	)
	return cmd
}

// autoInstallTokenEntry is the decoded shape of one element of
// GET /auto-install/tokens.
type autoInstallTokenEntry struct {
	Id        string  `json:"id"`
	CreatedBy string  `json:"created-by"`
	Enabled   *bool   `json:"enabled,omitempty"`
	ExpireAt  *int64  `json:"expire-at,omitempty"`
	Comment   *string `json:"comment,omitempty"`
}

// newAutoInstallTokenLsCmd builds `pmx pdm auto-install token ls` — list
// every automated-installation authentication token (GET
// /auto-install/tokens). The token secret is never returned by this
// endpoint; see autoinstallTokenSecretKeys.
func newAutoInstallTokenLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List automated-installation authentication tokens",
		Long: "List every token used to authenticate automated installation requests " +
			"(GET /auto-install/tokens). The token secret is never returned by this endpoint.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.AutoInstall.ListTokens(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tokens: %w", err)
			}

			items := rawItemsOf(resp)
			type tokenRow struct {
				entry autoInstallTokenEntry
				raw   map[string]any
			}
			table := make([]tokenRow, 0, len(items))

			for _, raw := range items {
				var e autoInstallTokenEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode token entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode token entry: %w", err)
				}
				stripAutoInstallTokenSecrets(m)

				table = append(table, tokenRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Id < table[j].entry.Id })

			headers := []string{"ID", "CREATED-BY", "ENABLED", "EXPIRE-AT", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Id, e.CreatedBy, boolPtrString(e.Enabled), int64PtrString(e.ExpireAt), strPtrString(e.Comment),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newAutoInstallTokenAddCmd builds `pmx pdm auto-install token add <id>` —
// create a token (POST /auto-install/tokens).
func newAutoInstallTokenAddCmd() *cobra.Command {
	var (
		comment  string
		enabled  bool
		expireAt int64
	)
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create an automated-installation authentication token",
		Long: "Create a new token for authenticating automated installation requests " +
			"(POST /auto-install/tokens). The response's SECRET column carries the " +
			"token secret; it is shown only once here and is never retrievable again.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()

			params := &pdmautoinstall.CreateTokensParams{Id: id}
			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}
			if fl.Changed("enabled") {
				params.Enabled = boolPtr(enabled)
			}
			if fl.Changed("expire-at") {
				params.ExpireAt = int64Ptr(expireAt)
			}

			resp, err := deps.PDM.AutoInstall.CreateTokens(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("add token %q: %w", id, err)
			}
			if resp == nil {
				return fmt.Errorf("add token %q: nil response from PDM", id)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("add token %q: decode response: %w", id, err)
			}

			res := output.Result{
				Headers: []string{"ID", "SECRET"},
				Rows:    [][]string{{id, resp.Secret}},
				Raw:     fields,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&enabled, "enabled", true, "whether the token is enabled")
	f.Int64Var(&expireAt, "expire-at", 0, "expiration (epoch seconds; 0 = never)")
	return cmd
}

// newAutoInstallTokenUpdateCmd builds `pmx pdm auto-install token update
// <id>` — update a token (PUT /auto-install/tokens/{id}).
func newAutoInstallTokenUpdateCmd() *cobra.Command {
	var (
		comment    string
		del        []string
		digest     string
		enabled    bool
		expireAt   int64
		regenerate bool
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an automated-installation authentication token",
		Long: "Update an existing token used to authenticate automated installation " +
			"requests (PUT /auto-install/tokens/{id}). Only flags explicitly set are " +
			"sent; use --delete to reset properties to their default, or --regenerate " +
			"to issue a new secret — the new secret is printed once in the response " +
			"and is never retrievable again.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update token %q: no changes given: pass at least one flag", id)
			}

			params := &pdmautoinstall.UpdateTokensParams{}
			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("enabled") {
				params.Enabled = boolPtr(enabled)
			}
			if fl.Changed("expire-at") {
				params.ExpireAt = int64Ptr(expireAt)
			}
			if fl.Changed("regenerate") {
				params.RegenerateSecret = boolPtr(regenerate)
			}

			resp, err := deps.PDM.AutoInstall.UpdateTokens(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update token %q: %w", id, err)
			}

			if resp != nil && resp.Secret != nil && *resp.Secret != "" {
				fields, err := flattenToMap(resp)
				if err != nil {
					return fmt.Errorf("update token %q: decode response: %w", id, err)
				}

				res := output.Result{
					Headers: []string{"ID", "SECRET"},
					Rows:    [][]string{{id, *resp.Secret}},
					Raw:     fields,
				}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			res := output.Result{Message: fmt.Sprintf("Token %q updated.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&comment, "comment", "", "comment")
	f.StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	f.BoolVar(&enabled, "enabled", true, "whether the token is enabled")
	f.Int64Var(&expireAt, "expire-at", 0, "expiration (epoch seconds; 0 = never)")
	f.BoolVar(&regenerate, "regenerate", false, "regenerate the token secret, invalidating the old one")
	return cmd
}

// newAutoInstallTokenDeleteCmd builds `pmx pdm auto-install token delete
// <id>` — remove a token (DELETE /auto-install/tokens/{id}).
func newAutoInstallTokenDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an automated-installation authentication token",
		Long: "Delete a token used to authenticate automated installation requests " +
			"(DELETE /auto-install/tokens/{id}). Fails if the token is currently in use " +
			"by any prepared answer configuration. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete token %q without confirmation: pass --yes/-y", id)
			}

			err := deps.PDM.AutoInstall.DeleteTokens(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("delete token %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Token %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
