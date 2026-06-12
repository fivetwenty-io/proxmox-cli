package qemu

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newConsoleCmd builds `pve qemu console <vmid>`, which opens a console proxy to
// a VM and returns the connection ticket. Three console types are supported via
// --type: vnc (default), term (serial/xterm.js), and spice. Each maps to a
// distinct proxy endpoint and returns a short-lived ticket the caller hands to a
// viewer; this command emits the ticket fields and does not itself connect.
func newConsoleCmd() *cobra.Command {
	var (
		consoleType string
		websocket   bool
		serial      string
		proxy       string
	)

	cmd := &cobra.Command{
		Use:   "console <vmid>",
		Short: "Open a console proxy to a VM and return its connection ticket",
		Long: "Request a console proxy ticket for a VM. The --type flag selects the\n" +
			"proxy kind: vnc (default), term (serial/xterm.js terminal), or spice.\n\n" +
			"The response contains a short-lived ticket and the host, port, and\n" +
			"certificate a viewer needs to connect. This command returns that\n" +
			"connection info; it does not open the console session itself.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
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
			case "term":
				endpoint = "termproxy"
				if cmd.Flags().Changed("serial") {
					body["serial"] = serial
				}
			case "spice":
				endpoint = "spiceproxy"
				if cmd.Flags().Changed("proxy") {
					body["proxy"] = proxy
				}
			default:
				return fmt.Errorf("invalid console type %q: must be vnc, term, or spice", consoleType)
			}

			// The typed client methods discard the response payload (the generated
			// CreateQemu*proxyResponse structs are empty), so the ticket fields
			// would be lost. POST the raw endpoint instead and render the decoded
			// connection info generically.
			path := fmt.Sprintf("/nodes/%s/qemu/%s/%s",
				url.PathEscape(node), url.PathEscape(vmid), endpoint)
			data, err := deps.API.Raw.PostCtx(cmd.Context(), path, body)
			if err != nil {
				return fmt.Errorf("open %s console for VM %s on node %q: %w", consoleType, vmid, node, err)
			}
			m, ok := data.(map[string]any)
			if !ok {
				return fmt.Errorf("open %s console for VM %s: unexpected response shape %T", consoleType, vmid, data)
			}
			single := make(map[string]string, len(m))
			for k, v := range m {
				single[k] = stringifyValue(v)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: data}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&consoleType, "type", "vnc", "console type: vnc, term, or spice")
	cmd.Flags().BoolVar(&websocket, "websocket", false, "prepare for a websocket upgrade (vnc)")
	cmd.Flags().StringVar(&serial, "serial", "", "open a serial terminal on the given device, e.g. serial0 (term)")
	cmd.Flags().StringVar(&proxy, "proxy", "", "SPICE proxy server hostname (spice)")

	return cmd
}
