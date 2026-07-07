package qemu

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
)

// newAgentExecCmd builds `pmx qemu agent exec <vmid> [-- <cmd>...]`.
// Prefer the positional `-- program arg...` form: each token is forwarded
// verbatim, so arguments may contain spaces. The legacy --command flag is kept
// for backward compatibility; it splits on whitespace and therefore cannot
// express an argument that itself contains a space.
func newAgentExecCmd() *cobra.Command {
	var (
		command   string
		inputData string
	)
	cmd := &cobra.Command{
		Use:   "exec <vmid|name> [-- <cmd>...]",
		Short: "Execute a command inside a VM via the guest agent",
		Long: "Run a command inside the VM via the QEMU guest agent and return the\n" +
			"PID of the spawned process. Pass the program and its arguments after\n" +
			"`--` so each argument is preserved exactly (spaces included); the\n" +
			"legacy --command flag splits on whitespace. Use\n" +
			"`pmx qemu agent exec-status` to poll for completion and output.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			// Positional argv after `--` takes precedence and preserves each
			// token verbatim; fall back to splitting the legacy --command flag.
			argv := args[1:]
			if len(argv) == 0 {
				if !cmd.Flags().Changed("command") {
					return fmt.Errorf("provide a command: `exec <vmid> -- <program> [args...]` or --command")
				}
				argv = strings.Fields(command)
			}
			if len(argv) == 0 {
				return fmt.Errorf("command must not be empty")
			}

			params := &nodes.CreateQemuAgentExecParams{Command: argv}
			if cmd.Flags().Changed("input-data") {
				params.InputData = strPtr(inputData)
			}

			resp, err := deps.API.Nodes.CreateQemuAgentExec(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("agent exec for VM %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("agent exec for VM %s on node %q: empty response", vmid, node)
			}

			single := map[string]string{"pid": fmt.Sprintf("%d", resp.Pid)}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&command, "command", "",
		"legacy: whitespace-split command line (prefer `-- <program> [args...]` for spaced arguments)")
	cmd.Flags().StringVar(&inputData, "input-data", "", "data to pass as stdin to the command")
	return cmd
}

// newAgentExecStatusCmd builds `pmx qemu agent exec-status <vmid> --pid PID`.
func newAgentExecStatusCmd() *cobra.Command {
	var pid int64
	cmd := &cobra.Command{
		Use:   "exec-status <vmid|name>",
		Short: "Poll exit status and output of a guest-agent exec process",
		Long: "Query the status of a process started with `pmx qemu agent exec`. " +
			"Returns stdout/stderr and exit code once the process has exited.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.ListQemuAgentExecStatusParams{Pid: pid}
			resp, err := deps.API.Nodes.ListQemuAgentExecStatus(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("agent exec-status for VM %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("agent exec-status for VM %s on node %q: empty response", vmid, node)
			}

			single := map[string]string{
				"exited": fmt.Sprintf("%v", bool(resp.Exited)),
			}
			if resp.Exitcode != nil {
				single["exitcode"] = fmt.Sprintf("%d", *resp.Exitcode)
			}
			if resp.OutData != nil {
				single["out-data"] = *resp.OutData
			}
			if resp.ErrData != nil {
				single["err-data"] = *resp.ErrData
			}
			if resp.Signal != nil {
				single["signal"] = fmt.Sprintf("%d", *resp.Signal)
			}
			if resp.OutTruncated != nil {
				single["out-truncated"] = fmt.Sprintf("%v", bool(*resp.OutTruncated))
			}
			if resp.ErrTruncated != nil {
				single["err-truncated"] = fmt.Sprintf("%v", bool(*resp.ErrTruncated))
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}

	cmd.Flags().Int64Var(&pid, "pid", 0, "PID returned by `agent exec` (required)")
	cli.MustMarkRequired(cmd, "pid")
	return cmd
}

// newAgentFileReadCmd builds `pmx qemu agent file-read <vmid> --file PATH [--offset N] [--count N]`.
func newAgentFileReadCmd() *cobra.Command {
	var (
		file   string
		offset int64
		count  int64
	)
	cmd := &cobra.Command{
		Use:   "file-read <vmid|name>",
		Short: "Read a file from inside a VM via the guest agent",
		Long: "Read up to 16 MiB of a file from the running guest via the QEMU " +
			"guest agent. Content is returned as plain text (decoded from base64 " +
			"by default). Use --offset and --count to page through larger files.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.ListQemuAgentFileReadParams{File: file}
			if cmd.Flags().Changed("offset") {
				params.Offset = int64Ptr(offset)
			}
			if cmd.Flags().Changed("count") {
				params.Count = int64Ptr(count)
			}

			resp, err := deps.API.Nodes.ListQemuAgentFileRead(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("agent file-read for VM %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("agent file-read for VM %s on node %q: empty response", vmid, node)
			}

			single := map[string]string{"content": resp.Content}
			if resp.Truncated != nil {
				single["truncated"] = fmt.Sprintf("%v", bool(*resp.Truncated))
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&file, "file", "", "path of the file to read inside the guest (required)")
	cmd.Flags().Int64Var(&offset, "offset", 0, "byte offset to start reading at")
	cmd.Flags().Int64Var(&count, "count", 0, "number of bytes to read")
	cli.MustMarkRequired(cmd, "file")
	return cmd
}

// newAgentFileWriteCmd builds `pmx qemu agent file-write <vmid> --file PATH --content CONTENT`.
func newAgentFileWriteCmd() *cobra.Command {
	var (
		file    string
		content string
	)
	cmd := &cobra.Command{
		Use:   "file-write <vmid|name>",
		Short: "Write content to a file inside a VM via the guest agent",
		Long: "Write the value of --content to the specified file path inside the " +
			"running guest. The QEMU guest agent handles base64 encoding automatically.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.CreateQemuAgentFileWriteParams{
				File:    file,
				Content: content,
			}

			if err := deps.API.Nodes.CreateQemuAgentFileWrite(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("agent file-write for VM %s on node %q: %w", vmid, node, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Wrote to %s in VM %s.", file, vmid)}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&file, "file", "", "path of the file to write inside the guest (required)")
	cmd.Flags().StringVar(&content, "content", "", "content to write (required)")
	cli.MustMarkRequired(cmd, "file")
	cli.MustMarkRequired(cmd, "content")
	return cmd
}

// newAgentSetUserPasswordCmd builds `pmx qemu agent set-user-password <vmid> --username USER`.
// The password is read from stdin and never echoed or logged.
func newAgentSetUserPasswordCmd() *cobra.Command {
	var (
		username string
		yes      bool
		crypted  bool
	)
	cmd := &cobra.Command{
		Use:   "set-user-password <vmid|name>",
		Short: "Set a user's password inside a VM via the guest agent",
		Long: "Set the password for a user account inside the running guest. The " +
			"password is read from stdin so it is never exposed in process arguments, " +
			"shell history, or log files.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			// Changing a guest user's password can lock that account out, so the
			// operation is gated behind --yes to guard against accidental runs.
			if !yes {
				return fmt.Errorf("refusing to set the password for user %q in VM %s without --yes/-y",
					username, vmid)
			}

			// Read the first line of stdin as the password, without echoing it.
			// The caller pipes the password in or uses a terminal that does not
			// echo (e.g. `read -rs PW && echo "$PW" | pve ...`). bufio.Reader is
			// used instead of bufio.Scanner so there is no token-size cap, and
			// cmd.InOrStdin() lets tests inject a reader via cmd.SetIn.
			line, rerr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if rerr != nil && line == "" {
				if rerr == io.EOF {
					return fmt.Errorf("agent set-user-password: no password provided on stdin")
				}
				return fmt.Errorf("agent set-user-password: read password from stdin: %w", rerr)
			}
			password := strings.TrimRight(line, "\r\n")
			if password == "" {
				return fmt.Errorf("agent set-user-password: password must not be empty")
			}

			params := &nodes.CreateQemuAgentSetUserPasswordParams{
				Username: username,
				Password: password,
			}
			if cmd.Flags().Changed("crypted") {
				params.Crypted = boolPtr(crypted)
			}

			resp, err := deps.API.Nodes.CreateQemuAgentSetUserPassword(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("agent set-user-password for VM %s on node %q: %w", vmid, node, err)
			}

			var raw json.RawMessage
			if resp != nil {
				raw = *resp
			}
			return renderAgentResult(cmd, deps, raw,
				fmt.Sprintf("Password for user %q updated in VM %s.", username, vmid))
		},
	}

	cmd.Flags().StringVar(&username, "username", "", "username whose password to set (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm setting the password")
	cmd.Flags().BoolVar(&crypted, "crypted", false,
		"[advanced] the password has already been passed through crypt(3)")
	cli.MustMarkRequired(cmd, "username")
	return cmd
}
