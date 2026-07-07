package node

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// renderProxyResponse decodes a json.RawMessage proxy response (ticket/port
// object) and renders it as a key/value Single. Returns an error when raw is
// nil, empty, or not a JSON object.
func renderProxyResponse(cmd *cobra.Command, deps *cli.Deps, raw *json.RawMessage) error {
	if raw == nil || len(*raw) == 0 {
		return fmt.Errorf("empty response from proxy endpoint")
	}
	var obj map[string]any
	if err := json.Unmarshal(*raw, &obj); err != nil {
		return fmt.Errorf("decode proxy response: %w", err)
	}
	single, rawMap, err := objectToSingle(obj)
	if err != nil {
		return fmt.Errorf("render proxy response: %w", err)
	}
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Single: single, Raw: rawMap}, deps.Format)
}

// newTermproxyCmd builds `pmx node termproxy`. It requests a terminal proxy
// ticket from the Proxmox API and prints the connection info (ticket, port,
// user). The ticket can be used with a websocket-capable terminal client to
// open an interactive session on the node.
//
// A live websocket terminal is not attached here because no shared websocket
// helper is available across packages; see the debt ledger for the deferred
// interactive-attach path.
func newTermproxyCmd() *cobra.Command {
	var (
		cmdFlag     string
		cmdOptsFlag string
	)
	cmd := &cobra.Command{
		Use:   "termproxy",
		Short: "Request a terminal proxy ticket for the node",
		Long: "POST /nodes/{node}/termproxy to acquire a short-lived ticket and port\n" +
			"for a websocket-based xterm.js terminal session on the node. The ticket,\n" +
			"port, and user fields are printed; hand them to a websocket terminal\n" +
			"client (e.g. the Proxmox web UI or a compatible third-party viewer) to\n" +
			"establish the interactive session.\n\n" +
			"Use --cmd to run a specific command instead of the default login shell\n" +
			"(requires root@pam credentials).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.CreateTermproxyParams{}
			if fl.Changed("cmd") {
				params.Cmd = &cmdFlag
			}
			if fl.Changed("cmd-opts") {
				params.CmdOpts = &cmdOptsFlag
			}
			resp, err := deps.API.Nodes.CreateTermproxy(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create termproxy on node %q: %w", deps.Node, err)
			}
			return renderProxyResponse(cmd, deps, resp)
		},
	}
	cmd.Flags().StringVar(&cmdFlag, "cmd", "", "command to run instead of login shell (requires root@pam)")
	cmd.Flags().StringVar(&cmdOptsFlag, "cmd-opts", "", "null-terminated parameters appended to the command")
	return cmd
}

// newVncshellCmd builds `pmx node vncshell`. It requests a VNC shell proxy
// ticket and prints the host/port/ticket fields needed by a VNC viewer.
// VNC is a GUI protocol; this command does not open a viewer — use the
// printed connection info with TigerVNC, RealVNC, or the Proxmox web UI.
func newVncshellCmd() *cobra.Command {
	var (
		cmdFlag     string
		cmdOptsFlag string
		height      int64
		width       int64
		websocket   bool
	)
	cmd := &cobra.Command{
		Use:   "vncshell",
		Short: "Request a VNC shell proxy ticket for the node",
		Long: "POST /nodes/{node}/vncshell to acquire a short-lived ticket and port\n" +
			"for a VNC shell session on the node. The ticket, host, and port fields\n" +
			"are printed; hand them to a VNC viewer (e.g. TigerVNC, RealVNC, or the\n" +
			"Proxmox web UI) to open the session.\n\n" +
			"VNC is a GUI protocol — this command emits connection info only and\n" +
			"does not open an interactive terminal.\n\n" +
			"Use --cmd to run a specific command instead of the default login shell\n" +
			"(requires root@pam credentials).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.CreateVncshellParams{}
			if fl.Changed("cmd") {
				params.Cmd = &cmdFlag
			}
			if fl.Changed("cmd-opts") {
				params.CmdOpts = &cmdOptsFlag
			}
			if fl.Changed("height") {
				params.Height = &height
			}
			if fl.Changed("width") {
				params.Width = &width
			}
			if fl.Changed("websocket") {
				params.Websocket = &websocket
			}
			resp, err := deps.API.Nodes.CreateVncshell(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create vncshell on node %q: %w", deps.Node, err)
			}
			return renderProxyResponse(cmd, deps, resp)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cmdFlag, "cmd", "", "command to run instead of login shell (requires root@pam)")
	f.StringVar(&cmdOptsFlag, "cmd-opts", "", "null-terminated parameters appended to the command")
	f.Int64Var(&height, "height", 0, "console height in pixels")
	f.Int64Var(&width, "width", 0, "console width in pixels")
	f.BoolVar(&websocket, "websocket", false, "use WebSocket upgrade instead of standard VNC framing")
	return cmd
}

// spiceFieldOrder lists the canonical [virt-viewer] key order for .vv files.
// Only fields present in the response are emitted; unrecognised keys are
// appended at the end.
var spiceFieldOrder = []string{
	"type", "host", "port", "tls-port", "host-subject", "ca",
	"password", "proxy", "fullscreen", "title",
	"enable-smartcard", "enable-usb-autoshare",
}

// writeSpiceVV writes a SPICE .vv connection file from the decoded response
// object and returns the file path.
func writeSpiceVV(obj map[string]any) (string, error) {
	f, err := os.CreateTemp("", "pve-spice-*.vv")
	if err != nil {
		return "", fmt.Errorf("create SPICE .vv file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var sb strings.Builder
	sb.WriteString("[virt-viewer]\n")

	written := make(map[string]bool)
	for _, key := range spiceFieldOrder {
		v, ok := obj[key]
		if !ok {
			continue
		}
		_, _ = fmt.Fprintf(&sb, "%s=%s\n", key, anyCell(v))
		written[key] = true
	}
	// Append any response fields not in the canonical list.
	for key, v := range obj {
		if written[key] {
			continue
		}
		_, _ = fmt.Fprintf(&sb, "%s=%s\n", key, anyCell(v))
	}
	// Tell virt-viewer to remove the file after opening it.
	if !written["delete-this-file"] {
		sb.WriteString("delete-this-file=1\n")
	}

	if _, err := f.WriteString(sb.String()); err != nil {
		return "", fmt.Errorf("write SPICE .vv file: %w", err)
	}
	return f.Name(), nil
}

// newSpiceshellCmd builds `pmx node spiceshell`. It requests a SPICE shell
// proxy ticket, writes a SPICE .vv connection file to the system temp
// directory, and prints the file path alongside the connection details.
// SPICE is a GUI protocol; open the .vv file with virt-viewer or a
// compatible SPICE client (e.g. remote-viewer) to start the session.
func newSpiceshellCmd() *cobra.Command {
	var (
		cmdFlag     string
		cmdOptsFlag string
		proxy       string
	)
	cmd := &cobra.Command{
		Use:   "spiceshell",
		Short: "Request a SPICE shell proxy ticket for the node",
		Long: "POST /nodes/{node}/spiceshell to acquire a short-lived SPICE ticket for\n" +
			"a shell session on the node. A SPICE .vv connection file is written to\n" +
			"the system temp directory; its path is printed so you can open it with\n" +
			"virt-viewer (remote-viewer file.vv) or any compatible SPICE client.\n" +
			"The full connection details (host, tls-port, password, etc.) are also\n" +
			"printed.\n\n" +
			"SPICE is a GUI protocol — this command emits connection info and a .vv\n" +
			"file only and does not open an interactive terminal.\n\n" +
			"Use --cmd to run a specific command instead of the default login shell\n" +
			"(requires root@pam credentials).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.CreateSpiceshellParams{}
			if fl.Changed("cmd") {
				params.Cmd = &cmdFlag
			}
			if fl.Changed("cmd-opts") {
				params.CmdOpts = &cmdOptsFlag
			}
			if fl.Changed("proxy") {
				params.Proxy = &proxy
			}
			resp, err := deps.API.Nodes.CreateSpiceshell(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("create spiceshell on node %q: %w", deps.Node, err)
			}
			if resp == nil || len(*resp) == 0 {
				return fmt.Errorf("empty response from spiceshell endpoint on node %q", deps.Node)
			}
			var obj map[string]any
			if err := json.Unmarshal(*resp, &obj); err != nil {
				return fmt.Errorf("decode spiceshell response on node %q: %w", deps.Node, err)
			}
			vvPath, err := writeSpiceVV(obj)
			if err != nil {
				// Non-fatal: still render connection info even if the file write fails.
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not write SPICE .vv file: %v\n", err)
				vvPath = ""
			}
			single, rawAny, err := objectToSingle(obj)
			if err != nil {
				return fmt.Errorf("render spiceshell response on node %q: %w", deps.Node, err)
			}
			if vvPath != "" {
				single["vv-file"] = vvPath
				if rawObj, ok := rawAny.(map[string]any); ok {
					rawObj["vv-file"] = vvPath
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: rawAny}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cmdFlag, "cmd", "", "command to run instead of login shell (requires root@pam)")
	f.StringVar(&cmdOptsFlag, "cmd-opts", "", "null-terminated parameters appended to the command")
	f.StringVar(&proxy, "proxy", "", "SPICE proxy server URL (defaults to current node)")
	return cmd
}
