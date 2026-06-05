package lxc

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

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
		Use:   "console <vmid>",
		Short: "Open a console proxy to a container and return its connection ticket",
		Long: "Request a console proxy ticket for a container. The --type flag selects\n" +
			"the proxy kind: vnc (default), term (xterm.js terminal), or spice.\n\n" +
			"The response contains a short-lived ticket and the host, port, and\n" +
			"certificate a viewer needs to connect. This command returns that\n" +
			"connection info; it does not open the console session itself.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := getDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
				return err
			}

			var endpoint string
			body := map[string]any{}
			switch consoleType {
			case "vnc", "":
				endpoint = "vncproxy"
				if cmd.Flags().Changed("websocket") {
					body["websocket"] = websocket
				}
				if cmd.Flags().Changed("width") {
					body["width"] = width
				}
				if cmd.Flags().Changed("height") {
					body["height"] = height
				}
			case "term":
				endpoint = "termproxy"
			case "spice":
				endpoint = "spiceproxy"
				if cmd.Flags().Changed("proxy") {
					body["proxy"] = proxy
				}
			default:
				return fmt.Errorf("invalid console type %q: must be vnc, term, or spice", consoleType)
			}

			// The typed client methods discard the response payload (the generated
			// CreateLxc*proxyResponse structs are empty), so the ticket fields
			// would be lost. POST the raw endpoint instead and render the decoded
			// connection info generically.
			path := fmt.Sprintf("/nodes/%s/lxc/%s/%s",
				url.PathEscape(node), url.PathEscape(vmid), endpoint)
			data, err := deps.API.Raw.PostCtx(cmd.Context(), path, body)
			if err != nil {
				return fmt.Errorf("open %s console for container %s on node %q: %w", consoleType, vmid, node, err)
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
