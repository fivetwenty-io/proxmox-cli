package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"
)

// s3BucketFlags are the flags shared by the /admin/s3 verbs, which both
// address an endpoint plus a bucket and an optional key prefix.
type s3BucketFlags struct {
	bucket      string
	storePrefix string
}

func (bf *s3BucketFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&bf.bucket, "bucket", "", "bucket name on the S3 object store (required)")
	cmd.Flags().StringVar(&bf.storePrefix, "store-prefix", "",
		"key prefix within the bucket, commonly the datastore name")
	cli.MustMarkRequired(cmd, "bucket")
}

// prefix returns the --store-prefix value only when it was explicitly set.
func (bf *s3BucketFlags) prefix(cmd *cobra.Command) *string {
	if cmd.Flags().Changed("store-prefix") {
		return &bf.storePrefix
	}
	return nil
}

// newS3CheckCmd builds `pmx pbs s3 check <id>` — run the server-side sanity
// check for an endpoint against a bucket (PUT /admin/s3/{id}/check).
func newS3CheckCmd() *cobra.Command {
	var bf s3BucketFlags
	cmd := &cobra.Command{
		Use:   "check <id>",
		Short: "Sanity-check an S3 endpoint against a bucket",
		Long: "Ask the server to verify that an S3 endpoint's credentials can reach " +
			"the given bucket and, with --store-prefix, the key prefix a datastore " +
			"would use (PUT /admin/s3/{id}/check). A nil result means the check passed.",
		Example: "  pmx pbs s3 check minio-lab --bucket pbs-backups --store-prefix main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			params := &pbsadmin.UpdateS3CheckParams{Bucket: bf.bucket, StorePrefix: bf.prefix(cmd)}
			if err := deps.PBS.Admin.UpdateS3Check(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("check s3 endpoint %q: %w", id, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{
				Message: fmt.Sprintf("S3 endpoint %q check passed for bucket %q.", id, bf.bucket),
			}, deps.Format)
		},
	}
	bf.register(cmd)
	return cmd
}

// newS3ResetCountersCmd builds `pmx pbs s3 reset-counters <id>` — zero the
// request counters for an endpoint, bucket, and optional prefix
// (PUT /admin/s3/{id}/reset-counters).
func newS3ResetCountersCmd() *cobra.Command {
	var bf s3BucketFlags
	cmd := &cobra.Command{
		Use:   "reset-counters <id>",
		Short: "Reset the S3 request counters",
		Long: "Reset the request counters the server keeps for an S3 endpoint and " +
			"bucket, narrowed to one datastore's keys with --store-prefix " +
			"(PUT /admin/s3/{id}/reset-counters).",
		Example: "  pmx pbs s3 reset-counters minio-lab --bucket pbs-backups",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			params := &pbsadmin.UpdateS3ResetCountersParams{Bucket: bf.bucket, StorePrefix: bf.prefix(cmd)}
			if err := deps.PBS.Admin.UpdateS3ResetCounters(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("reset counters on s3 endpoint %q: %w", id, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{
				Message: fmt.Sprintf("Request counters reset on S3 endpoint %q for bucket %q.", id, bf.bucket),
			}, deps.Format)
		},
	}
	bf.register(cmd)
	return cmd
}

// newDatastoreS3RefreshCmd builds `pmx pbs datastore s3-refresh <store>` —
// re-sync an S3-backed datastore's local cache from the object store
// (PUT /admin/datastore/{store}/s3-refresh). Returns a task; honours --async.
func newDatastoreS3RefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "s3-refresh <store>",
		Short: "Refresh an S3-backed datastore's local cache from the object store",
		Long: "Rebuild the local cache of an S3-backed datastore from the contents of " +
			"its bucket (PUT /admin/datastore/{store}/s3-refresh). The server runs " +
			"this as a task; the command blocks until it finishes unless --async is set. " + cli.WaitBoundHelp,
		Example: "  pmx pbs datastore s3-refresh main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			store := args[0]

			resp, err := deps.PBS.Admin.UpdateDatastoreS3Refresh(cmd.Context(), store)
			if err != nil {
				return fmt.Errorf("refresh datastore %q from s3: %w", store, err)
			}
			if resp == nil {
				return fmt.Errorf("refresh datastore %q from s3: nil response from PBS", store)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Datastore %q refreshed from S3.", store))
		},
	}
}
