package cluster

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/proxmox-cli/internal/cephview"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// cephHealthCodePattern is the shape of a Ceph health check code, such as
// POOL_NO_REDUNDANCY or OSD_DOWN. It is the server's [A-Z][A-Z0-9_]+ pattern
// with the server's 64 character bound, so a typo is rejected before a request.
var cephHealthCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

// cephHealthMuteMinPVE is the first pve-manager release that serves
// /cluster/ceph/health-mute. Older servers answer every call with 501.
const cephHealthMuteMinPVE = "9.2.12"

// healthMuteError wraps a failed health-mute call. When the server does not
// implement the endpoint at all, it says which pve-manager release does,
// because the server's own "not implemented" reads like a pmx defect.
func healthMuteError(action string, err error) error {
	var apiErr *pveerrors.APIError
	if errors.As(err, &apiErr) &&
		(apiErr.HTTPCode == http.StatusNotImplemented || strings.Contains(apiErr.Message, "not implemented")) {
		return fmt.Errorf("%s: this server does not serve ceph health mutes, which need pve-manager %s "+
			"or newer: %w", action, cephHealthMuteMinPVE, err)
	}
	return fmt.Errorf("%s: %w", action, err)
}

// newCephHealthMuteCmd builds the `pmx pve cluster ceph health-mute` sub-tree:
// list the muted Ceph health checks, mute one, and unmute one. A mute is a
// listable resource, so the verbs are list, create, and delete, with the
// native `ceph health mute` and `unmute` spellings kept as aliases.
func newCephHealthMuteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health-mute",
		Short: "List, create, and delete Ceph health check mutes",
		Long: "Manage the Ceph health checks that are muted cluster-wide. A muted check no longer " +
			"counts towards the cluster status, but it stays visible and Ceph keeps evaluating it. " +
			"Requires a configured Ceph cluster and pve-manager " + cephHealthMuteMinPVE + " or newer.",
	}
	cmd.AddCommand(
		newCephHealthMuteListCmd(),
		newCephHealthMuteCreateCmd(),
		newCephHealthMuteDeleteCmd(),
	)
	return cmd
}

// validateCephHealthCode rejects a code the server would refuse, so the
// operator sees the mistake before any request goes out.
func validateCephHealthCode(code string) error {
	if !cephHealthCodePattern.MatchString(code) {
		return fmt.Errorf("invalid ceph health check code %q: want an upper-case check name of up to 64 "+
			"characters, such as POOL_NO_REDUNDANCY", code)
	}
	return nil
}

// newCephHealthMuteListCmd builds `pmx pve cluster ceph health-mute list`
// (GET /cluster/ceph/health-mute).
func newCephHealthMuteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the muted Ceph health checks",
		Long: "List the Ceph health checks that are currently muted, with whether each mute is sticky, " +
			"when it expires, and what the check reports. Reading the list needs Sys.Audit or " +
			"Datastore.Audit on /. Requires a configured Ceph cluster and pve-manager " +
			cephHealthMuteMinPVE + " or newer.",
		Example: `  pmx pve cluster ceph health-mute list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListCephHealthMute(cmd.Context())
			if err != nil {
				return healthMuteError("list ceph health mutes", err)
			}
			res, err := cephview.HealthMute(resp)
			if err != nil {
				return fmt.Errorf("decode ceph health mutes: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newCephHealthMuteCreateCmd builds `pmx pve cluster ceph health-mute create`
// (PUT /cluster/ceph/health-mute/{code} with value=1).
func newCephHealthMuteCreateCmd() *cobra.Command {
	var (
		ttl    string
		sticky bool
	)
	cmd := &cobra.Command{
		Use:     "create <code>",
		Aliases: []string{"mute"},
		Short:   "Mute a Ceph health check",
		Long: "Mute one Ceph health check, named by its code as 'ceph health' reports it, for example " +
			"POOL_NO_REDUNDANCY. A muted check no longer counts towards the cluster status, but it stays " +
			"visible and Ceph keeps evaluating it. Muting needs Sys.Modify on /.\n\n" +
			"--ttl sets how long the mute lasts, for example 2h, 3d, 1w, or a compound such as '1d 2h'. " +
			"Without it the mute never expires. Without --sticky, Ceph mutes only a check that is raised " +
			"right now and refuses otherwise, and the mute lifts itself when the check clears or the " +
			"number of affected items grows, which brings the check back to attention. With --sticky, " +
			"the mute holds regardless, even when the check is quiet or gets worse. The server " +
			"validates the --ttl value.",
		Example: `  pmx pve cluster ceph health-mute create POOL_NO_REDUNDANCY --ttl 2h
  pmx pve cluster ceph health-mute create OSD_DOWN --ttl 1d --sticky`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			code := args[0]
			if err := validateCephHealthCode(code); err != nil {
				return err
			}
			params := &pvecluster.UpdateCephHealthMuteParams{Value: true}
			var terms []string
			if fl.Changed("ttl") {
				if strings.TrimSpace(ttl) == "" {
					return fmt.Errorf("invalid --ttl: the value is empty; give a span such as 2h, 3d, or 1w, " +
						"or leave --ttl out for a mute that never expires")
				}
				params.Ttl = &ttl
				terms = append(terms, "ttl "+ttl)
			}
			if fl.Changed("sticky") {
				params.Sticky = &sticky
				if sticky {
					terms = append(terms, "sticky")
				}
			}
			if err := deps.API.Cluster.UpdateCephHealthMute(cmd.Context(), code, params); err != nil {
				return healthMuteError(fmt.Sprintf("mute ceph health check %q", code), err)
			}
			msg := fmt.Sprintf("ceph health check %s muted", code)
			if len(terms) > 0 {
				msg += " (" + strings.Join(terms, ", ") + ")"
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg + "."}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&ttl, "ttl", "", "how long the mute lasts, for example 2h, 3d, or 1w (default: no expiry)")
	f.BoolVar(&sticky, "sticky", false, "keep the mute even when the check gets worse")
	return cmd
}

// newCephHealthMuteDeleteCmd builds `pmx pve cluster ceph health-mute delete`
// (PUT /cluster/ceph/health-mute/{code} with value=0).
func newCephHealthMuteDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <code>",
		Aliases: []string{"unmute"},
		Short:   "Unmute a Ceph health check",
		Long: "Unmute one Ceph health check, named by its code as 'ceph health' reports it, so it counts " +
			"towards the cluster status again. Unmuting needs Sys.Modify on /. When the list of mutes " +
			"shows the check is not muted, nothing is sent and the command says so.",
		Example: `  pmx pve cluster ceph health-mute delete POOL_NO_REDUNDANCY`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			code := args[0]
			if err := validateCephHealthCode(code); err != nil {
				return err
			}
			if muted, known := cephHealthCodeMuted(cmd, deps, code); known && !muted {
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{Message: fmt.Sprintf("ceph health check %s is not muted.", code)}, deps.Format)
			}
			params := &pvecluster.UpdateCephHealthMuteParams{Value: false}
			if err := deps.API.Cluster.UpdateCephHealthMute(cmd.Context(), code, params); err != nil {
				return healthMuteError(fmt.Sprintf("unmute ceph health check %q", code), err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("ceph health check %s unmuted.", code)}, deps.Format)
		},
	}
}

// cephHealthCodeMuted reports whether code is in the list of mutes. The server
// unmutes a code that was never muted without complaint, so this lookup is
// what lets delete say so. known is false when the list cannot be read, for
// example under a token with Sys.Modify but no audit privilege, and the caller
// then sends the unmute anyway.
func cephHealthCodeMuted(cmd *cobra.Command, deps *cli.Deps, code string) (muted, known bool) {
	resp, err := deps.API.Cluster.ListCephHealthMute(cmd.Context())
	if err != nil {
		return false, false
	}
	res, err := cephview.HealthMute(resp)
	if err != nil {
		return false, false
	}
	return slices.ContainsFunc(res.Rows, func(row []string) bool { return row[0] == code }), true
}
