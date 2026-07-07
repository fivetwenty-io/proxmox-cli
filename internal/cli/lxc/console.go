package lxc

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newConsoleCmd builds `pve lxc console <vmid>`, which opens a console proxy to a
// container and returns the connection ticket. Three console types are supported
// via --type: vnc (default), term (xterm.js terminal), and spice. Each maps to a
// distinct proxy endpoint and returns a short-lived ticket a viewer uses to
// connect; this command emits the ticket fields and does not itself connect.
func newConsoleCmd() *cobra.Command {
	var (
		consoleType string
		websocket   bool
		width       int64
		height      int64
		proxy       string
	)

	cmd := &cobra.Command{
		Use:   "console <vmid|name>",
		Short: "Open a console proxy to a container and return its connection ticket",
		Long: "Request a console proxy ticket for a container. The --type flag selects\n" +
			"the proxy kind: vnc (default), term (xterm.js terminal), or spice.\n\n" +
			"The response contains a short-lived ticket and the host, port, and\n" +
			"certificate a viewer needs to connect. This command returns that\n" +
			"connection info; it does not open the console session itself.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			// The typed CreateLxc*proxyResponse types are json.RawMessage
			// aliases carrying the full ticket/host/port payload (not empty
			// structs), so the typed client methods are used directly here
			// and the raw bytes decoded generically for rendering.
			var raw *json.RawMessage
			switch consoleType {
			case "vnc", "":
				var params *nodes.CreateLxcVncproxyParams
				if cmd.Flags().Changed("websocket") || cmd.Flags().Changed("width") || cmd.Flags().Changed("height") {
					params = &nodes.CreateLxcVncproxyParams{}
					if cmd.Flags().Changed("websocket") {
						params.Websocket = &websocket
					}
					if cmd.Flags().Changed("width") {
						params.Width = &width
					}
					if cmd.Flags().Changed("height") {
						params.Height = &height
					}
				}
				raw, err = deps.API.Nodes.CreateLxcVncproxy(cmd.Context(), node, vmid, params)
			case "term":
				raw, err = deps.API.Nodes.CreateLxcTermproxy(cmd.Context(), node, vmid)
			case "spice":
				var params *nodes.CreateLxcSpiceproxyParams
				if cmd.Flags().Changed("proxy") {
					params = &nodes.CreateLxcSpiceproxyParams{Proxy: &proxy}
				}
				raw, err = deps.API.Nodes.CreateLxcSpiceproxy(cmd.Context(), node, vmid, params)
			default:
				return fmt.Errorf("invalid console type %q: must be vnc, term, or spice", consoleType)
			}
			if err != nil {
				return fmt.Errorf("open %s console for container %s on node %q: %w", consoleType, vmid, node, err)
			}

			var data any
			if raw != nil {
				if err := json.Unmarshal(*raw, &data); err != nil {
					return fmt.Errorf("open %s console for container %s: decode response: %w", consoleType, vmid, err)
				}
			}
			single, err := structToStringMap(data)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: data}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&consoleType, "type", "vnc", "console type: vnc, term, or spice")
	cmd.Flags().BoolVar(&websocket, "websocket", false, "use a websocket instead of standard VNC (vnc)")
	cmd.Flags().Int64Var(&width, "width", 0, "console width in pixels (vnc)")
	cmd.Flags().Int64Var(&height, "height", 0, "console height in pixels (vnc)")
	cmd.Flags().StringVar(&proxy, "proxy", "", "SPICE proxy server hostname (spice)")

	return cmd
}
