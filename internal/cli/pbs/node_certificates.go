package pbs

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// nodeCertInfoEntry mirrors one element of the JSON array PBS returns from
// GET /nodes/{node}/certificates/info and POST /nodes/{node}/certificates/custom,
// per the PBS API's documented CertificateInfo schema.
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

// newNodeCertificatesCmd builds `pmx pbs node certificates` and its
// info/acme/custom verbs (/nodes/{node}/certificates...).
//
// GET /nodes/{node}/certificates itself is only a directory index (returns
// null; its sub-resources are acme, custom, info) with no data behind it, so
// there is no `ls` verb — the group's own help lists its real sub-commands.
func newNodeCertificatesCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "certificates",
		Aliases: []string{"cert"},
		Short:   "Inspect and manage the node's TLS certificates",
		Long: "Inspect and manage the TLS certificates serving the node's API: view the " +
			"current certificate chain, order or renew an ACME certificate, or upload and " +
			"remove a custom certificate.",
	}
	cmd.AddCommand(
		newNodeCertificatesInfoCmd(nf),
		newNodeCertificatesAcmeCmd(nf),
		newNodeCertificatesCustomCmd(nf),
	)
	return cmd
}

// newNodeCertificatesInfoCmd builds `pmx pbs node certificates info` — the
// node's certificate chain (GET /nodes/{node}/certificates/info).
func newNodeCertificatesInfoCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show the node's certificate chain",
		Long:  "Show every certificate currently serving the node's API, including subject, issuer, fingerprint, and validity window.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListCertificatesInfo(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("get certificate info on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeCertInfoEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode certificate info on node %q: %w", nf.node, err)
			}

			return renderNodeCertEntries(cmd, deps, entries)
		},
	}
}

// renderNodeCertEntries renders a list of certificate info entries as a
// table, shared by `certificates info` and `certificates custom upload`.
func renderNodeCertEntries(cmd *cobra.Command, deps *cli.Deps, entries []nodeCertInfoEntry) error {
	headers := []string{"FILENAME", "SUBJECT", "ISSUER", "NOT-BEFORE", "NOT-AFTER", "FINGERPRINT"}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			e.Filename, pbsFormatOptionalString(e.Subject), pbsFormatOptionalString(e.Issuer),
			epochCellPtr(e.Notbefore), epochCellPtr(e.Notafter), pbsFormatOptionalString(e.Fingerprint),
		})
	}

	res := output.Result{Headers: headers, Rows: rows, Raw: entries}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// --- acme -------------------------------------------------------------------

func newNodeCertificatesAcmeCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acme",
		Short: "Order or renew the node's ACME certificate",
		Long:  "Order a new ACME certificate for the node, or renew its current ACME certificate.",
	}
	cmd.AddCommand(newNodeCertificatesAcmeOrderCmd(nf), newNodeCertificatesAcmeRenewCmd(nf))
	return cmd
}

// newNodeCertificatesAcmeOrderCmd builds
// `pmx pbs node certificates acme order` — order a new ACME certificate
// (POST /nodes/{node}/certificates/acme/certificate).
//
// The generated Nodes.CreateCertificatesAcmeCertificate binding discards its
// response body entirely (no data type at all in its signature), even
// though ordering a certificate from an external ACME directory is
// necessarily an asynchronous task. This bypasses it via the shared raw
// transport to recover the task UPID and support --async.
func newNodeCertificatesAcmeOrderCmd(nf *nodeFlags) *cobra.Command {
	var (
		force bool
		yes   bool
	)

	cmd := &cobra.Command{
		Use:   "order",
		Short: "Order a new ACME certificate for the node",
		Long: "Request a new ACME (Let's Encrypt) certificate for the node and install it. " +
			"Runs as an asynchronous task; the command blocks until it finishes unless --async " +
			"is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to order an ACME certificate on node %q without confirmation: pass --yes/-y",
					nf.node)
			}

			body := map[string]interface{}{}
			if cmd.Flags().Changed("force") {
				body["force"] = force
			}

			path := fmt.Sprintf("/nodes/%s/certificates/acme/certificate", url.PathEscape(nf.node))
			msg := fmt.Sprintf("ACME certificate ordered on node %q.", nf.node)

			err := nodeFinishAsync(cmd, deps, http.MethodPost, path, body, msg)
			if err != nil {
				return fmt.Errorf("order ACME certificate on node %q: %w", nf.node, err)
			}

			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "overwrite an existing custom certificate")
	f.BoolVarP(&yes, "yes", "y", false, "confirm ordering an ACME certificate")

	return cmd
}

// newNodeCertificatesAcmeRenewCmd builds
// `pmx pbs node certificates acme renew` — renew the current ACME
// certificate (PUT /nodes/{node}/certificates/acme/certificate).
//
// Same discarded-body workaround as newNodeCertificatesAcmeOrderCmd (see its
// comment).
func newNodeCertificatesAcmeRenewCmd(nf *nodeFlags) *cobra.Command {
	var (
		force bool
		yes   bool
	)

	cmd := &cobra.Command{
		Use:   "renew",
		Short: "Renew the node's ACME certificate",
		Long: "Renew the ACME (Let's Encrypt) certificate for the node. By default PBS only " +
			"renews when expiry is within its renewal lead time; pass --force to renew " +
			"regardless. Runs as an asynchronous task; the command blocks until it finishes " +
			"unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to renew the ACME certificate on node %q without confirmation: pass --yes/-y",
					nf.node)
			}

			body := map[string]interface{}{}
			if cmd.Flags().Changed("force") {
				body["force"] = force
			}

			path := fmt.Sprintf("/nodes/%s/certificates/acme/certificate", url.PathEscape(nf.node))
			msg := fmt.Sprintf("ACME certificate renewed on node %q.", nf.node)

			err := nodeFinishAsync(cmd, deps, http.MethodPut, path, body, msg)
			if err != nil {
				return fmt.Errorf("renew ACME certificate on node %q: %w", nf.node, err)
			}

			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "renew even if expiry is outside the renewal lead time")
	f.BoolVarP(&yes, "yes", "y", false, "confirm renewing the ACME certificate")

	return cmd
}

// --- custom -------------------------------------------------------------

func newNodeCertificatesCustomCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "custom",
		Short: "Upload or remove a custom certificate",
		Long:  "Install a custom PEM-encoded certificate for the node's API, or remove the currently installed one.",
	}
	cmd.AddCommand(newNodeCertificatesCustomUploadCmd(nf), newNodeCertificatesCustomDeleteCmd(nf))
	return cmd
}

// newNodeCertificatesCustomUploadCmd builds
// `pmx pbs node certificates custom upload` — install a custom certificate
// (POST /nodes/{node}/certificates/custom).
func newNodeCertificatesCustomUploadCmd(nf *nodeFlags) *cobra.Command {
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
		Long: "Install a PEM-encoded certificate (chain) — and optionally its private key — as " +
			"the node's API certificate. The private key is sent to the API but never echoed " +
			"back. Use --restart to reload the API proxy so the new certificate takes effect " +
			"immediately.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to upload a custom certificate on node %q without confirmation: pass --yes/-y",
					nf.node)
			}
			fl := cmd.Flags()

			params := &pbsnodes.CreateCertificatesCustomParams{Certificates: certificates}
			if fl.Changed("key") {
				params.Key = &key
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("restart") {
				params.Restart = &restart
			}

			resp, err := deps.PBS.Nodes.CreateCertificatesCustom(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("upload custom certificate on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeCertInfoEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode custom certificate response on node %q: %w", nf.node, err)
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

// newNodeCertificatesCustomDeleteCmd builds
// `pmx pbs node certificates custom delete` — remove the custom certificate
// (DELETE /nodes/{node}/certificates/custom).
func newNodeCertificatesCustomDeleteCmd(nf *nodeFlags) *cobra.Command {
	var (
		restart bool
		yes     bool
	)

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove the node's custom certificate",
		Long:  "Delete the custom certificate from the node, reverting it to the self-signed certificate.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to remove the custom certificate on node %q without confirmation: pass --yes/-y",
					nf.node)
			}

			params := &pbsnodes.DeleteCertificatesCustomParams{}
			if cmd.Flags().Changed("restart") {
				params.Restart = &restart
			}

			err := deps.PBS.Nodes.DeleteCertificatesCustom(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("remove custom certificate on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Custom certificate on node %q removed.", nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&restart, "restart", false, "restart the API proxy after removing the certificate")
	f.BoolVarP(&yes, "yes", "y", false, "confirm removing the custom certificate")

	return cmd
}
