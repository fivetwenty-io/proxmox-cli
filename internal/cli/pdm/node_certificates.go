package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// nodeCertInfoEntry mirrors one element of the JSON array PDM returns from
// GET /nodes/{node}/certificates/info and POST
// /nodes/{node}/certificates/custom, per the PDM API's documented
// CertificateInfo schema.
type nodeCertInfoEntry struct {
	Filename      string  `json:"filename"`
	Fingerprint   *string `json:"fingerprint,omitempty"`
	Issuer        *string `json:"issuer,omitempty"`
	Subject       *string `json:"subject,omitempty"`
	Notbefore     *int64  `json:"notbefore,omitempty"`
	Notafter      *int64  `json:"notafter,omitempty"`
	PublicKeyType *string `json:"public-key-type,omitempty"`
	PublicKeyBits *int64  `json:"public-key-bits,omitempty"`
}

// newNodeCertificateCmd builds `pmx pdm node certificate` and its
// info/upload/delete-custom/acme verbs (/nodes/{node}/certificates...).
//
// GET /nodes/{node}/certificates itself is only a directory index with no
// data behind it, so there is no `ls` verb — the group's own help lists its
// real sub-commands, matching the PBS analog
// (internal/cli/pbs/node_certificates.go:30-48).
func newNodeCertificateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "certificate",
		Short: "Inspect and manage the node's TLS certificates",
		Long: "Inspect and manage the TLS certificates serving the node's API: view the " +
			"chain, upload or remove a custom certificate, and order or renew an ACME certificate.",
	}
	cmd.AddCommand(
		newNodeCertificateInfoCmd(),
		newNodeCertificateUploadCmd(),
		newNodeCertificateDeleteCustomCmd(),
		newNodeCertificateAcmeCmd(),
	)
	return cmd
}

// newNodeCertificateInfoCmd builds `pmx pdm node certificate info <node>` —
// the node's certificate chain (GET /nodes/{node}/certificates/info).
func newNodeCertificateInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <node>",
		Short: "Show the node's certificate chain",
		Long:  "Show every certificate currently serving the node's API, including subject, issuer, fingerprint, and validity window.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListCertificatesInfo(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get certificate info on node %q: %w", node, err)
			}

			entries, err := nodeDecodeArray[nodeCertInfoEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode certificate info on node %q: %w", node, err)
			}

			return renderNodeCertEntries(cmd, deps, entries)
		},
	}
}

// renderNodeCertEntries renders a list of certificate info entries as a
// table, shared by `certificate info` and `certificate upload`.
func renderNodeCertEntries(cmd *cobra.Command, deps *cli.Deps, entries []nodeCertInfoEntry) error {
	headers := []string{"FILENAME", "SUBJECT", "ISSUER", "NOT-BEFORE", "NOT-AFTER", "FINGERPRINT"}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			e.Filename, strPtrString(e.Subject), strPtrString(e.Issuer),
			int64PtrString(e.Notbefore), int64PtrString(e.Notafter), strPtrString(e.Fingerprint),
		})
	}

	res := output.Result{Headers: headers, Rows: rows, Raw: entries}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// newNodeCertificateUploadCmd builds `pmx pdm node certificate upload
// <node>` — install a custom certificate (POST
// /nodes/{node}/certificates/custom). Certificates and key are raw
// PEM-encoded text flags, matching the PBS analog
// (internal/cli/pbs/node_certificates.go:219-277); neither product's CLI
// reads a client-side file path for this — the value is the PEM text itself.
func newNodeCertificateUploadCmd() *cobra.Command {
	var (
		certificates string
		key          string
		force        bool
		restart      bool
		yes          bool
	)

	cmd := &cobra.Command{
		Use:   "upload <node>",
		Short: "Upload a custom certificate for the node",
		Long: "Install a PEM-encoded certificate (chain) — and optionally its private key — as " +
			"the node's API certificate. The private key is sent to the API but never echoed " +
			"back. Use --restart to reload the API proxy so the new certificate takes effect " +
			"immediately.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			if !yes {
				return fmt.Errorf("refusing to upload a custom certificate on node %q without confirmation: pass --yes/-y",
					node)
			}
			fl := cmd.Flags()

			params := &pdmnodes.CreateCertificatesCustomParams{Certificates: certificates}
			if fl.Changed("key") {
				params.Key = &key
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("restart") {
				params.Restart = &restart
			}

			resp, err := deps.PDM.Nodes.CreateCertificatesCustom(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("upload custom certificate on node %q: %w", node, err)
			}

			entries, err := nodeDecodeArray[nodeCertInfoEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode custom certificate response on node %q: %w", node, err)
			}

			return renderNodeCertEntries(cmd, deps, entries)
		},
	}
	f := cmd.Flags()
	f.StringVar(&certificates, "certificates", "", "PEM-encoded certificate chain (required)")
	f.StringVar(&key, "key", "", "PEM-encoded private key (kept secret, never echoed)")
	f.BoolVar(&force, "force", false, "overwrite an existing custom or ACME certificate")
	f.BoolVar(&restart, "restart", false, "restart the API proxy so the certificate takes effect")
	f.BoolVarP(&yes, "yes", "y", false, "confirm uploading a custom certificate")
	cli.MustMarkRequired(cmd, "certificates")

	return cmd
}

// newNodeCertificateDeleteCustomCmd builds `pmx pdm node certificate
// delete-custom <node>` — remove the custom certificate (DELETE
// /nodes/{node}/certificates/custom), regenerating a self-signed one.
func newNodeCertificateDeleteCustomCmd() *cobra.Command {
	var (
		restart bool
		yes     bool
	)

	cmd := &cobra.Command{
		Use:   "delete-custom <node>",
		Short: "Remove the node's custom certificate",
		Long: "Delete the custom certificate from the node, regenerating a self-signed " +
			"certificate in its place. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			if !yes {
				return fmt.Errorf("refusing to remove the custom certificate on node %q without confirmation: pass --yes/-y",
					node)
			}

			params := &pdmnodes.DeleteCertificatesCustomParams{}
			if cmd.Flags().Changed("restart") {
				params.Restart = &restart
			}

			err := deps.PDM.Nodes.DeleteCertificatesCustom(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("remove custom certificate on node %q: %w", node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Custom certificate on node %q removed.", node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&restart, "restart", false, "restart the API proxy after removing the certificate")
	f.BoolVarP(&yes, "yes", "y", false, "confirm removing the custom certificate")

	return cmd
}

// --- acme -------------------------------------------------------------------

// newNodeCertificateAcmeCmd builds `pmx pdm node certificate acme` and its
// order/renew verbs.
func newNodeCertificateAcmeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acme",
		Short: "Order or renew the node's ACME certificate",
		Long:  "Order a new ACME (Let's Encrypt) certificate for the node, or renew the current one.",
	}
	cmd.AddCommand(newNodeCertificateAcmeOrderCmd(), newNodeCertificateAcmeRenewCmd())
	return cmd
}

// newNodeCertificateAcmeOrderCmd builds `pmx pdm node certificate acme order
// <node>` — order a new ACME certificate (POST
// /nodes/{node}/certificates/acme/certificate).
//
// Runs as an asynchronous task: its returns.pattern in the PDM API schema is
// the UPID regex (pdm-apidoc.json, verified 2026-07-08), and nodes_gen.go
// types CreateCertificatesAcmeCertificateResponse as `= json.RawMessage`
// (nodes_gen.go:583-626, v3.6.0) — a real typed response, unlike the PBS
// analog whose generated binding discards the body entirely and must bypass
// the raw transport to recover the UPID
// (internal/cli/pbs/node_certificates.go:103-153).
func newNodeCertificateAcmeOrderCmd() *cobra.Command {
	var (
		force bool
		yes   bool
	)

	cmd := &cobra.Command{
		Use:   "order <node>",
		Short: "Order a new ACME certificate for the node",
		Long: "Request a new ACME (Let's Encrypt) certificate for the node and install it. " +
			"Runs as an asynchronous task; the command blocks until it finishes unless " +
			"--async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			if !yes {
				return fmt.Errorf("refusing to order an ACME certificate on node %q without confirmation: pass --yes/-y",
					node)
			}

			params := &pdmnodes.CreateCertificatesAcmeCertificateParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}

			resp, err := deps.PDM.Nodes.CreateCertificatesAcmeCertificate(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("order ACME certificate on node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("order ACME certificate on node %q: empty response from server", node)
			}

			msg := fmt.Sprintf("ACME certificate ordered on node %q.", node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "overwrite an existing custom certificate")
	f.BoolVarP(&yes, "yes", "y", false, "confirm ordering an ACME certificate")

	return cmd
}

// newNodeCertificateAcmeRenewCmd builds `pmx pdm node certificate acme
// renew <node>` — renew the current ACME certificate (PUT
// /nodes/{node}/certificates/acme/certificate).
//
// Same async classification and typed-response divergence from the PBS
// analog as newNodeCertificateAcmeOrderCmd (see its comment):
// UpdateCertificatesAcmeCertificateResponse is `= json.RawMessage`
// (nodes_gen.go:634-677, v3.6.0) and the schema's returns.pattern is the
// UPID regex (verified 2026-07-08).
func newNodeCertificateAcmeRenewCmd() *cobra.Command {
	var (
		force bool
		yes   bool
	)

	cmd := &cobra.Command{
		Use:   "renew <node>",
		Short: "Renew the node's ACME certificate",
		Long: "Renew the ACME (Let's Encrypt) certificate for the node. By default PDM only " +
			"renews when expiry is within its renewal lead time; pass --force to renew " +
			"regardless. Runs as an asynchronous task; the command blocks until it finishes " +
			"unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			if !yes {
				return fmt.Errorf("refusing to renew the ACME certificate on node %q without confirmation: pass --yes/-y",
					node)
			}

			params := &pdmnodes.UpdateCertificatesAcmeCertificateParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}

			resp, err := deps.PDM.Nodes.UpdateCertificatesAcmeCertificate(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("renew ACME certificate on node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("renew ACME certificate on node %q: empty response from server", node)
			}

			msg := fmt.Sprintf("ACME certificate renewed on node %q.", node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "renew even if expiry is outside the renewal lead time")
	f.BoolVarP(&yes, "yes", "y", false, "confirm renewing the ACME certificate")

	return cmd
}
