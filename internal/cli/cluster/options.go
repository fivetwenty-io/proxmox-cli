package cluster

import (
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newOptionsCmd builds the `pve cluster options` sub-tree, which reads and edits
// the datacenter-wide options stored in datacenter.cfg (console defaults,
// migration policy, MAC prefix, language, and similar cluster settings).
func newOptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Inspect and set cluster-wide datacenter options",
		Long: "Read or update the datacenter options (datacenter.cfg): default console " +
			"viewer, keyboard and language, migration and HA policy, bandwidth limits, " +
			"MAC prefix, and other cluster-wide settings.",
	}
	cmd.AddCommand(
		newOptionsGetCmd(),
		newOptionsSetCmd(),
	)
	return cmd
}

func newOptionsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the cluster-wide datacenter options",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.ListOptions(cmd.Context())
			if err != nil {
				return fmt.Errorf("get cluster options: %w", err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get cluster options: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// optionsSetFlags are the names of every flag the set command forwards. The
// datacenter option set is large; only the flags the user passes are changed.
var optionsSetFlags = []string{
	"bwlimit", "console", "consent-text", "crs", "description", "email-from",
	"fencing", "ha", "http-proxy", "keyboard", "language", "location",
	"mac-prefix", "max-workers", "migration", "next-id", "notify",
	"registered-tags", "replication", "tag-style", "user-tag-access", "delete",
}

func newOptionsSetCmd() *cobra.Command {
	var (
		bwlimit        string
		console        string
		consentText    string
		crs            string
		description    string
		emailFrom      string
		fencing        string
		ha             string
		httpProxy      string
		keyboard       string
		language       string
		location       string
		macPrefix      string
		maxWorkers     int64
		migration      string
		nextID         string
		notify         string
		registeredTags string
		replication    string
		tagStyle       string
		userTagAccess  string
		del            string
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set cluster-wide datacenter options",
		Long:  "Update the datacenter options. Only the flags you pass are changed.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			if !anyFlagChanged(fl, optionsSetFlags...) {
				return fmt.Errorf("no options to set: pass at least one option flag")
			}

			params := &pvecluster.UpdateOptionsParams{}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}
			if fl.Changed("console") {
				params.Console = &console
			}
			if fl.Changed("consent-text") {
				params.ConsentText = &consentText
			}
			if fl.Changed("crs") {
				params.Crs = &crs
			}
			if fl.Changed("description") {
				params.Description = &description
			}
			if fl.Changed("email-from") {
				params.EmailFrom = &emailFrom
			}
			if fl.Changed("fencing") {
				params.Fencing = &fencing
			}
			if fl.Changed("ha") {
				params.Ha = &ha
			}
			if fl.Changed("http-proxy") {
				params.HttpProxy = &httpProxy
			}
			if fl.Changed("keyboard") {
				params.Keyboard = &keyboard
			}
			if fl.Changed("language") {
				params.Language = &language
			}
			if fl.Changed("location") {
				params.Location = &location
			}
			if fl.Changed("mac-prefix") {
				params.MacPrefix = &macPrefix
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			if fl.Changed("migration") {
				params.Migration = &migration
			}
			if fl.Changed("next-id") {
				params.NextId = &nextID
			}
			if fl.Changed("notify") {
				params.Notify = &notify
			}
			if fl.Changed("registered-tags") {
				params.RegisteredTags = &registeredTags
			}
			if fl.Changed("replication") {
				params.Replication = &replication
			}
			if fl.Changed("tag-style") {
				params.TagStyle = &tagStyle
			}
			if fl.Changed("user-tag-access") {
				params.UserTagAccess = &userTagAccess
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}

			if err := deps.API.Cluster.UpdateOptions(cmd.Context(), params); err != nil {
				return fmt.Errorf("set cluster options: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: "Cluster options updated."}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&bwlimit, "bwlimit", "", "I/O bandwidth limits per operation type (KiB/s), for example migration=100000")
	f.StringVar(&console, "console", "", "default console viewer: applet, vv, html5, or xtermjs")
	f.StringVar(&consentText, "consent-text", "", "consent text shown before login")
	f.StringVar(&crs, "crs", "", "cluster resource scheduling settings, for example ha=basic")
	f.StringVar(&description, "description", "", "datacenter description shown in the web UI notes panel")
	f.StringVar(&emailFrom, "email-from", "", "sender address for notification email")
	f.StringVar(&fencing, "fencing", "", "HA fencing mode: watchdog, hardware, or both")
	f.StringVar(&ha, "ha", "", "cluster-wide HA settings, for example shutdown_policy=migrate")
	f.StringVar(&httpProxy, "http-proxy", "", "external HTTP proxy used for downloads")
	f.StringVar(&keyboard, "keyboard", "", "default VNC keyboard layout, for example en-us")
	f.StringVar(&language, "language", "", "default web UI language, for example en")
	f.StringVar(&location, "location", "", "geographic location of the cluster")
	f.StringVar(&macPrefix, "mac-prefix", "", "prefix for auto-generated guest MAC addresses")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum workers per node for bulk actions")
	f.StringVar(&migration, "migration", "", "cluster-wide migration settings, for example type=insecure")
	f.StringVar(&nextID, "next-id", "", "range for free VMID auto-selection, for example lower=100,upper=10000")
	f.StringVar(&notify, "notify", "", "cluster-wide notification settings")
	f.StringVar(&registeredTags, "registered-tags", "", "tags that require Sys.Modify on '/' to set or delete")
	f.StringVar(&replication, "replication", "", "cluster-wide replication settings")
	f.StringVar(&tagStyle, "tag-style", "", "tag style options, for example color-map=...")
	f.StringVar(&userTagAccess, "user-tag-access", "", "privilege options for user-settable tags")
	f.StringVar(&del, "delete", "", "comma-separated list of options to reset to default")
	return cmd
}
