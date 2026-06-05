package access

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newDomainCmd builds `pve access domain` and its sub-commands for managing
// authentication realms (domains): the built-in pam/pve realms plus configured
// ldap, ad, and openid realms, including user/group synchronization.
func newDomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "Manage authentication realms (domains)",
		Long: "List, inspect, create, update, and delete authentication realms, " +
			"and synchronize users and groups from ldap/ad realms.",
	}
	cmd.AddCommand(
		newDomainListCmd(),
		newDomainGetCmd(),
		newDomainCreateCmd(),
		newDomainSetCmd(),
		newDomainDeleteCmd(),
		newDomainSyncCmd(),
	)
	return cmd
}

// domainListEntry is a single row of the GET /access/domains response. The
// client returns each realm as a raw JSON object, so only the stable columns
// are decoded here; the full object is available via `domain get`.
type domainListEntry struct {
	Realm   string  `json:"realm"`
	Type    string  `json:"type"`
	Comment string  `json:"comment,omitempty"`
	Tfa     string  `json:"tfa,omitempty"`
	Default pveBool `json:"default,omitempty"`
}

// newDomainListCmd builds `pve access domain list`.
func newDomainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List authentication realms",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)

			resp, err := deps.API.Access.ListDomains(cmd.Context())
			if err != nil {
				return fmt.Errorf("list domains: %w", err)
			}

			rows := make([][]string, 0, len(*resp))
			for _, raw := range *resp {
				var e domainListEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode domain entry: %w", err)
				}
				rows = append(rows, []string{e.Realm, e.Type, e.Default.cell(), e.Tfa, e.Comment})
			}

			result := output.Result{
				Headers: []string{"REALM", "TYPE", "DEFAULT", "TFA", "COMMENT"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// newDomainGetCmd builds `pve access domain get <realm>`. The realm config is an
// open-ended set of keys that varies by realm type, so the raw object is
// rendered generically as a sorted key/value table.
func newDomainGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <realm>",
		Short: "Show a realm's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			realm := args[0]

			resp, err := deps.API.Access.GetDomains(cmd.Context(), realm)
			if err != nil {
				return fmt.Errorf("get domain %q: %w", realm, err)
			}

			single, err := rawObjectToSingle(*resp)
			if err != nil {
				return fmt.Errorf("decode domain %q: %w", realm, err)
			}
			if _, ok := single["REALM"]; !ok {
				single["REALM"] = realm
			}

			result := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
}

// domainFlags holds the realm-config flags shared by create and set. Each is
// forwarded only when the user actually changed it, so an update never clears a
// field the caller did not mention.
type domainFlags struct {
	comment      string
	defaultRealm bool
	server1      string
	server2      string
	port         int64
	mode         string
	baseDn       string
	bindDn       string
	userAttr     string
	domain       string
	issuerURL    string
	clientID     string
	clientKey    string
	autocreate   bool
}

// register attaches the shared realm-config flags to a command.
func (df *domainFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&df.comment, "comment", "", "description")
	f.BoolVar(&df.defaultRealm, "default", false, "use this as the default realm")
	f.StringVar(&df.server1, "server1", "", "primary server address (ldap/ad)")
	f.StringVar(&df.server2, "server2", "", "fallback server address (ldap/ad)")
	f.Int64Var(&df.port, "port", 0, "server port (ldap/ad)")
	f.StringVar(&df.mode, "mode", "", "ldap protocol mode (ldap|ldaps|ldap+starttls)")
	f.StringVar(&df.baseDn, "base-dn", "", "ldap base domain name")
	f.StringVar(&df.bindDn, "bind-dn", "", "ldap bind domain name")
	f.StringVar(&df.userAttr, "user-attr", "", "ldap user attribute name")
	f.StringVar(&df.domain, "domain", "", "ad domain name")
	f.StringVar(&df.issuerURL, "issuer-url", "", "openid issuer URL")
	f.StringVar(&df.clientID, "client-id", "", "openid client ID")
	f.StringVar(&df.clientKey, "client-key", "", "openid client key")
	f.BoolVar(&df.autocreate, "autocreate", false, "automatically create users on login")
}

// newDomainCreateCmd builds `pve access domain create <realm> --type <type>`.
func newDomainCreateCmd() *cobra.Command {
	var df domainFlags
	var realmType, usernameClaim string
	cmd := &cobra.Command{
		Use:   "create <realm> --type <type>",
		Short: "Create an authentication realm",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			realm := args[0]

			if realmType == "" {
				return fmt.Errorf("--type is required (one of ad, ldap, openid, pam, pve)")
			}

			params := &access.CreateDomainsParams{Realm: realm, Type: realmType}
			setIfChanged(cmd, "comment", &params.Comment, df.comment)
			setBoolIfChanged(cmd, "default", &params.Default, df.defaultRealm)
			setIfChanged(cmd, "server1", &params.Server1, df.server1)
			setIfChanged(cmd, "server2", &params.Server2, df.server2)
			setInt64IfChanged(cmd, "port", &params.Port, df.port)
			setIfChanged(cmd, "mode", &params.Mode, df.mode)
			setIfChanged(cmd, "base-dn", &params.BaseDn, df.baseDn)
			setIfChanged(cmd, "bind-dn", &params.BindDn, df.bindDn)
			setIfChanged(cmd, "user-attr", &params.UserAttr, df.userAttr)
			setIfChanged(cmd, "domain", &params.Domain, df.domain)
			setIfChanged(cmd, "issuer-url", &params.IssuerUrl, df.issuerURL)
			setIfChanged(cmd, "client-id", &params.ClientId, df.clientID)
			setIfChanged(cmd, "client-key", &params.ClientKey, df.clientKey)
			setIfChanged(cmd, "username-claim", &params.UsernameClaim, usernameClaim)
			setBoolIfChanged(cmd, "autocreate", &params.Autocreate, df.autocreate)

			if err := deps.API.Access.CreateDomains(cmd.Context(), params); err != nil {
				return fmt.Errorf("create domain %q: %w", realm, err)
			}

			result := output.Result{Message: fmt.Sprintf("Realm '%s' created.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().StringVar(&realmType, "type", "", "realm type: ad, ldap, openid, pam, pve (required)")
	cmd.Flags().StringVar(&usernameClaim, "username-claim", "", "openid claim used for the username (create-only)")
	df.register(cmd)
	return cmd
}

// newDomainSetCmd builds `pve access domain set <realm>`.
func newDomainSetCmd() *cobra.Command {
	var df domainFlags
	var deleteKeys string
	cmd := &cobra.Command{
		Use:   "set <realm>",
		Short: "Update an authentication realm",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			realm := args[0]

			params := &access.UpdateDomainsParams{}
			setIfChanged(cmd, "comment", &params.Comment, df.comment)
			setBoolIfChanged(cmd, "default", &params.Default, df.defaultRealm)
			setIfChanged(cmd, "server1", &params.Server1, df.server1)
			setIfChanged(cmd, "server2", &params.Server2, df.server2)
			setInt64IfChanged(cmd, "port", &params.Port, df.port)
			setIfChanged(cmd, "mode", &params.Mode, df.mode)
			setIfChanged(cmd, "base-dn", &params.BaseDn, df.baseDn)
			setIfChanged(cmd, "bind-dn", &params.BindDn, df.bindDn)
			setIfChanged(cmd, "user-attr", &params.UserAttr, df.userAttr)
			setIfChanged(cmd, "domain", &params.Domain, df.domain)
			setIfChanged(cmd, "issuer-url", &params.IssuerUrl, df.issuerURL)
			setIfChanged(cmd, "client-id", &params.ClientId, df.clientID)
			setIfChanged(cmd, "client-key", &params.ClientKey, df.clientKey)
			setBoolIfChanged(cmd, "autocreate", &params.Autocreate, df.autocreate)
			setIfChanged(cmd, "delete", &params.Delete, deleteKeys)

			if err := deps.API.Access.UpdateDomains(cmd.Context(), realm, params); err != nil {
				return fmt.Errorf("update domain %q: %w", realm, err)
			}

			result := output.Result{Message: "Realm updated."}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	df.register(cmd)
	cmd.Flags().StringVar(&deleteKeys, "delete", "", "comma-separated list of settings to clear")
	return cmd
}

// newDomainDeleteCmd builds `pve access domain delete <realm>`.
func newDomainDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <realm>",
		Short: "Delete an authentication realm",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			realm := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete realm %q without --yes/-y", realm)
			}

			if err := deps.API.Access.DeleteDomains(cmd.Context(), realm); err != nil {
				return fmt.Errorf("delete domain %q: %w", realm, err)
			}

			result := output.Result{Message: fmt.Sprintf("Realm '%s' deleted.", realm)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion")
	return cmd
}

// newDomainSyncCmd builds `pve access domain sync <realm>`. Synchronization is
// only meaningful for ldap and ad realms; the server rejects it for other
// types. The response is a worker task identifier, rendered verbatim.
func newDomainSyncCmd() *cobra.Command {
	var dryRun, enableNew bool
	var removeVanished, scope string
	cmd := &cobra.Command{
		Use:   "sync <realm>",
		Short: "Synchronize users and groups from an ldap/ad realm",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			realm := args[0]

			params := &access.CreateDomainsSyncParams{}
			setBoolIfChanged(cmd, "dry-run", &params.DryRun, dryRun)
			setBoolIfChanged(cmd, "enable-new", &params.EnableNew, enableNew)
			setIfChanged(cmd, "remove-vanished", &params.RemoveVanished, removeVanished)
			setIfChanged(cmd, "scope", &params.Scope, scope)

			resp, err := deps.API.Access.CreateDomainsSync(cmd.Context(), realm, params)
			if err != nil {
				return fmt.Errorf("sync domain %q: %w", realm, err)
			}

			msg := fmt.Sprintf("Sync started for realm '%s'.", realm)
			if resp != nil {
				if upid := rawString(*resp); upid != "" {
					msg = upid
				}
			}
			result := output.Result{Message: msg, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do not write anything")
	cmd.Flags().BoolVar(&enableNew, "enable-new", false, "enable newly synced users immediately")
	cmd.Flags().StringVar(&removeVanished, "remove-vanished", "",
		"semicolon list of items to remove when they vanish (entry;properties;acl), or none")
	cmd.Flags().StringVar(&scope, "scope", "", "select what to sync (users, groups, both)")
	return cmd
}

// setBoolIfChanged sets *dst to val only when the named flag was provided, so an
// unset boolean flag is omitted from the request rather than sent as false.
func setBoolIfChanged(cmd *cobra.Command, name string, dst **bool, val bool) {
	if cmd.Flags().Changed(name) {
		v := val
		*dst = &v
	}
}

// setInt64IfChanged sets *dst to val only when the named flag was provided.
func setInt64IfChanged(cmd *cobra.Command, name string, dst **int64, val int64) {
	if cmd.Flags().Changed(name) {
		v := val
		*dst = &v
	}
}

// rawObjectToSingle decodes a raw JSON object into a sorted-key string map for
// key/value rendering, stringifying scalar values and JSON-encoding any nested
// arrays or objects. A null or empty raw message yields an empty map.
func rawObjectToSingle(raw json.RawMessage) (map[string]string, error) {
	out := map[string]string{}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(generic))
	for k := range generic {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[upperKey(k)] = stringifyValue(generic[k])
	}
	return out, nil
}

// upperKey converts a JSON key to an upper-case header label, mapping
// underscores and hyphens to a consistent hyphen separator.
func upperKey(k string) string {
	b := make([]byte, 0, len(k))
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z':
			b = append(b, c-('a'-'A'))
		case c == '_':
			b = append(b, '-')
		default:
			b = append(b, c)
		}
	}
	return string(b)
}

// stringifyValue renders a decoded JSON scalar as a string; composite values are
// re-encoded as compact JSON so they survive in a flat key/value view.
func stringifyValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "1"
		}
		return "0"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// rawString returns the string value of a raw JSON message when it encodes a
// JSON string, and "" otherwise.
func rawString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
