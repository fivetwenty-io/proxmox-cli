package node

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// renderCertTask renders the asynchronous task started by an ACME certificate
// operation. The endpoints return a worker UPID; honour --async and otherwise
// block on the task, but tolerate a non-UPID or empty body by falling back to a
// plain success message.
func renderCertTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, doneMsg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
	}
	if deps.Async {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{
				Single:  map[string]string{"upid": upid},
				Raw:     map[string]string{"upid": upid},
				Message: upid,
			}, deps.Format)
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return fmt.Errorf("ACME certificate operation on node %q: %w", deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}

func newCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cert",
		Aliases: []string{"certificates"},
		Short:   "Inspect and manage the node's TLS certificates",
		Long: "List the certificate chain serving the node's API, manage ACME (Let's Encrypt) " +
			"certificate orders, and upload or remove a custom certificate.",
	}
	cmd.AddCommand(newCertListCmd(), newCertAcmeCmd(), newCertCustomCmd())
	return cmd
}

func newCertListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the node's certificate chain",
		Long:  "Show every certificate currently serving the resolved node's API, including subject, issuer, fingerprint, and validity window.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCertificatesInfo(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list certificates on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

// ---- acme ------------------------------------------------------------------

func newCertAcmeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acme",
		Short: "Manage the node's ACME certificate",
		Long:  "Inspect, order, renew, or remove the ACME (Let's Encrypt) certificate for the resolved node.",
	}
	cmd.AddCommand(newCertAcmeListCmd(), newCertAcmeOrderCmd(), newCertAcmeRenewCmd(), newCertAcmeDeleteCmd())
	return cmd
}

// acmeManagedCertFilename is where ACME (and custom) certificates are
// installed on a PVE node; pve-ssl.pem is the self-signed fallback.
const acmeManagedCertFilename = "pveproxy-ssl.pem"

// newCertAcmeListCmd builds `pve node cert acme list`.
//
// GET /nodes/{node}/certificates/acme is only a directory index (certificate);
// the certificate details live in /nodes/{node}/certificates/info, filtered
// here to the ACME-managed file.
func newCertAcmeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the node's ACME-managed certificate",
		Long: "Show the certificate installed at " + acmeManagedCertFilename + " on the resolved node — the file " +
			"ACME orders and renewals manage. If none is installed, the node is serving its self-signed certificate.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListCertificatesInfo(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list ACME certificate state on node %q: %w", deps.Node, err)
			}
			matches := make([]json.RawMessage, 0, 1)
			for _, raw := range derefRaws(resp) {
				var e struct {
					Filename string `json:"filename"`
				}
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode certificate entry: %w", err)
				}
				if e.Filename == acmeManagedCertFilename {
					matches = append(matches, raw)
				}
			}
			if len(matches) == 0 {
				// Raw keeps -o json/yaml emitting an empty list rather than a
				// message object; table mode still prints the message.
				return deps.Out.Render(cmd.OutOrStdout(), output.Result{
					Message: fmt.Sprintf("No ACME or custom certificate installed on node %q; "+
						"the self-signed pve-ssl.pem is in use.", deps.Node),
					Raw: []json.RawMessage{},
				}, deps.Format)
			}
			return renderScan(cmd, deps, matches, matches)
		},
	}
}

func newCertAcmeOrderCmd() *cobra.Command {
	var (
		force bool
		yes   bool
	)
	cmd := &cobra.Command{
		Use:   "order",
		Short: "Order a new ACME certificate for the node",
		Long: "Request a new ACME (Let's Encrypt) certificate for the resolved node and install it. " +
			"This contacts the configured ACME directory and replaces the node's API certificate.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "order an ACME certificate"); err != nil {
				return err
			}
			params := &nodes.CreateCertificatesAcmeCertificateParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}
			resp, err := deps.API.Nodes.CreateCertificatesAcmeCertificate(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("order ACME certificate on node %q: %w", deps.Node, err)
			}
			return renderCertTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("ACME certificate ordered on node %q.", deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "overwrite an existing custom certificate")
	f.BoolVarP(&yes, "yes", "y", false, "confirm ordering an ACME certificate")
	return cmd
}

func newCertAcmeRenewCmd() *cobra.Command {
	var (
		force bool
		yes   bool
	)
	cmd := &cobra.Command{
		Use:   "renew",
		Short: "Renew the node's ACME certificate",
		Long: "Renew the ACME (Let's Encrypt) certificate for the resolved node. By default PVE only " +
			"renews when expiry is within 30 days; pass --force to renew regardless.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "renew the ACME certificate"); err != nil {
				return err
			}
			params := &nodes.UpdateCertificatesAcmeCertificateParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}
			resp, err := deps.API.Nodes.UpdateCertificatesAcmeCertificate(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("renew ACME certificate on node %q: %w", deps.Node, err)
			}
			return renderCertTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("ACME certificate renewed on node %q.", deps.Node))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "renew even if expiry is more than 30 days away")
	f.BoolVarP(&yes, "yes", "y", false, "confirm renewing the ACME certificate")
	return cmd
}

func newCertAcmeDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove the node's ACME certificate",
		Long:  "Delete the ACME (Let's Encrypt) certificate from the resolved node, reverting it to the self-signed certificate.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "remove the ACME certificate"); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.DeleteCertificatesAcmeCertificate(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("remove ACME certificate on node %q: %w", deps.Node, err)
			}
			return renderCertTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("ACME certificate removed on node %q.", deps.Node))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm removing the ACME certificate")
	return cmd
}

// ---- custom ----------------------------------------------------------------

func newCertCustomCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "custom",
		Short: "Upload or remove a custom certificate",
		Long:  "Install a custom (externally issued) certificate on the resolved node, or remove it to revert to the self-signed certificate.",
	}
	cmd.AddCommand(newCertCustomUploadCmd(), newCertCustomDeleteCmd())
	return cmd
}

func newCertCustomUploadCmd() *cobra.Command {
	var (
		certificates string
		key          string
		force        bool
		restart      bool
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "upload",
		Short: "Upload a custom certificate for the node",
		Long: "Install a PEM-encoded certificate (chain) — and optionally its private key — as the node's " +
			"API certificate. The private key is sent to the API but never echoed back. Use --restart to " +
			"reload pveproxy so the new certificate takes effect immediately.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "upload a custom certificate"); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.CreateCertificatesCustomParams{Certificates: certificates}
			if fl.Changed("key") {
				params.Key = &key
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("restart") {
				params.Restart = &restart
			}
			resp, err := deps.API.Nodes.CreateCertificatesCustom(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("upload custom certificate on node %q: %w", deps.Node, err)
			}
			// resp describes the installed certificate (subject, fingerprint, validity);
			// it never contains the private key, so it is safe to render in full.
			return renderObject(cmd, deps, resp)
		},
	}
	f := cmd.Flags()
	f.StringVar(&certificates, "certificates", "", "PEM-encoded certificate chain (required)")
	f.StringVar(&key, "key", "", "PEM-encoded private key (kept secret, never echoed)")
	f.BoolVar(&force, "force", false, "overwrite an existing custom or ACME certificate")
	f.BoolVar(&restart, "restart", false, "restart pveproxy so the certificate takes effect")
	f.BoolVarP(&yes, "yes", "y", false, "confirm uploading a custom certificate")
	cli.MustMarkRequired(cmd, "certificates")
	return cmd
}

func newCertCustomDeleteCmd() *cobra.Command {
	var (
		restart bool
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove the node's custom certificate",
		Long:  "Delete the custom certificate from the resolved node, reverting it to the self-signed certificate.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "remove the custom certificate"); err != nil {
				return err
			}
			params := &nodes.DeleteCertificatesCustomParams{}
			if cmd.Flags().Changed("restart") {
				params.Restart = &restart
			}
			if err := deps.API.Nodes.DeleteCertificatesCustom(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("remove custom certificate on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Custom certificate removed on node %q.", deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&restart, "restart", false, "restart pveproxy after removing the certificate")
	f.BoolVarP(&yes, "yes", "y", false, "confirm removing the custom certificate")
	return cmd
}
