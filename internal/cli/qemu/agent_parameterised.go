package qemu

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newAgentExecCmd builds `pve qemu agent exec <vmid> --command CMD [--input-data DATA]`.
// The command field accepts space-separated tokens that are split into a slice
// and forwarded to the API as the JSON array field. The caller must quote the
// whole value if it contains shell metacharacters.
func newAgentExecCmd() *cobra.Command {
	var (
		command   string
		inputData string
	)
	cmd := &cobra.Command{
		Use:   "exec <vmid>",
		Short: "Execute a command inside a VM via the guest agent",
		Long: "Run a command inside the VM via the QEMU guest agent and return the\n" +
			"PID of the spawned process. Use `pve qemu agent exec-status` to poll\n" +
			"for completion and retrieve stdout/stderr.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
				return err
			}
			if !cmd.Flags().Changed("command") {
				return fmt.Errorf("--command is required: provide the program and its arguments")
			}

			// Split the command string into argv so the API receives a proper array.
			argv := strings.Fields(command)
			if len(argv) == 0 {
				return fmt.Errorf("--command must not be empty")
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

	cmd.Flags().StringVar(&command, "command", "", "command to run (program and arguments, space-separated)")
	cmd.Flags().StringVar(&inputData, "input-data", "", "data to pass as stdin to the command")
	return cmd
}

// newAgentExecStatusCmd builds `pve qemu agent exec-status <vmid> --pid PID`.
func newAgentExecStatusCmd() *cobra.Command {
	var pid int64
	cmd := &cobra.Command{
		Use:   "exec-status <vmid>",
		Short: "Poll exit status and output of a guest-agent exec process",
		Long: "Query the status of a process started with `pve qemu agent exec`. " +
			"Returns stdout/stderr and exit code once the process has exited.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
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
	if err := cmd.MarkFlagRequired("pid"); err != nil {
		// MarkFlagRequired only errors when the flag does not exist; panic is
		// appropriate here because it indicates a coding error.
		panic(fmt.Sprintf("agent exec-status: mark --pid required: %v", err))
	}
	return cmd
}

// newAgentFileReadCmd builds `pve qemu agent file-read <vmid> --file PATH [--offset N] [--count N]`.
func newAgentFileReadCmd() *cobra.Command {
	var (
		file   string
		offset int64
		count  int64
	)
	cmd := &cobra.Command{
		Use:   "file-read <vmid>",
		Short: "Read a file from inside a VM via the guest agent",
		Long: "Read up to 16 MiB of a file from the running guest via the QEMU " +
			"guest agent. Content is returned as plain text (decoded from base64 " +
			"by default). Use --offset and --count to page through larger files.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
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
	if err := cmd.MarkFlagRequired("file"); err != nil {
		panic(fmt.Sprintf("agent file-read: mark --file required: %v", err))
	}
	return cmd
}

// newAgentFileWriteCmd builds `pve qemu agent file-write <vmid> --file PATH --content CONTENT`.
func newAgentFileWriteCmd() *cobra.Command {
	var (
		file    string
		content string
	)
	cmd := &cobra.Command{
		Use:   "file-write <vmid>",
		Short: "Write content to a file inside a VM via the guest agent",
		Long: "Write the value of --content to the specified file path inside the " +
			"running guest. The QEMU guest agent handles base64 encoding automatically.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
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
	if err := cmd.MarkFlagRequired("file"); err != nil {
		panic(fmt.Sprintf("agent file-write: mark --file required: %v", err))
	}
	if err := cmd.MarkFlagRequired("content"); err != nil {
		panic(fmt.Sprintf("agent file-write: mark --content required: %v", err))
	}
	return cmd
}

// newAgentSetUserPasswordCmd builds `pve qemu agent set-user-password <vmid> --username USER`.
// The password is read from stdin and never echoed or logged.
func newAgentSetUserPasswordCmd() *cobra.Command {
	var username string
	cmd := &cobra.Command{
		Use:   "set-user-password <vmid>",
		Short: "Set a user's password inside a VM via the guest agent",
		Long: "Set the password for a user account inside the running guest. The " +
			"password is read from stdin so it is never exposed in process arguments, " +
			"shell history, or log files.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
				return err
			}

			// Read password from stdin without echoing it. bufio.Scanner reads
			// up to the first newline; the caller pipes the password in or uses
			// a terminal that does not echo (e.g. `read -rs PW && echo "$PW" | pve ...`).
			// cmd.InOrStdin() is used so tests can inject a reader via cmd.SetIn.
			scanner := bufio.NewScanner(cmd.InOrStdin())
			if !scanner.Scan() {
				if scanErr := scanner.Err(); scanErr != nil {
					return fmt.Errorf("agent set-user-password: read password from stdin: %w", scanErr)
				}
				return fmt.Errorf("agent set-user-password: no password provided on stdin")
			}
			password := scanner.Text()
			if password == "" {
				return fmt.Errorf("agent set-user-password: password must not be empty")
			}

			params := &nodes.CreateQemuAgentSetUserPasswordParams{
				Username: username,
				Password: password,
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
	if err := cmd.MarkFlagRequired("username"); err != nil {
		panic(fmt.Sprintf("agent set-user-password: mark --username required: %v", err))
	}
	return cmd
}
