package qemu

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newConsoleCmd builds `pmx pve qemu console <vmid>`, which opens a console proxy to
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
		Use:   "console <vmid|name>",
		Short: "Open a console proxy to a VM and return its connection ticket",
		Long: "Request a console proxy ticket for a VM. The --type flag selects the\n" +
			"proxy kind: vnc (default), term (serial/xterm.js terminal), or spice.\n\n" +
			"The response contains a short-lived ticket and the host, port, and\n" +
			"certificate a viewer needs to connect. This command returns that\n" +
			"connection info; it does not open the console session itself.",
		Example: `  pmx pve qemu console 100
  pmx pve qemu console 100 --type spice`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			// The typed CreateQemu*proxyResponse types are json.RawMessage
			// aliases carrying the full ticket/host/port payload (not empty
			// structs), so the typed client methods are used directly here
			// and the raw bytes decoded generically for rendering.
			var raw *json.RawMessage
			switch consoleType {
			case "vnc", "":
				var params *nodes.CreateQemuVncproxyParams
				if cmd.Flags().Changed("websocket") {
					params = &nodes.CreateQemuVncproxyParams{Websocket: &websocket}
				}
				raw, err = deps.API.Nodes.CreateQemuVncproxy(cmd.Context(), node, vmid, params)
			case "term":
				var params *nodes.CreateQemuTermproxyParams
				if cmd.Flags().Changed("serial") {
					params = &nodes.CreateQemuTermproxyParams{Serial: &serial}
				}
				raw, err = deps.API.Nodes.CreateQemuTermproxy(cmd.Context(), node, vmid, params)
			case "spice":
				var params *nodes.CreateQemuSpiceproxyParams
				if cmd.Flags().Changed("proxy") {
					params = &nodes.CreateQemuSpiceproxyParams{Proxy: &proxy}
				}
				raw, err = deps.API.Nodes.CreateQemuSpiceproxy(cmd.Context(), node, vmid, params)
			default:
				return fmt.Errorf("invalid console type %q: must be vnc, term, or spice", consoleType)
			}
			if err != nil {
				return fmt.Errorf("open %s console for VM %s on node %q: %w", consoleType, vmid, node, err)
			}

			var data any
			if raw != nil {
				if err := json.Unmarshal(*raw, &data); err != nil {
					return fmt.Errorf("open %s console for VM %s: unexpected response shape: %w", consoleType, vmid, err)
				}
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
