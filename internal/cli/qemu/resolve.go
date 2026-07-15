package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// agentIPAddress is one address entry from the guest agent's
// network-get-interfaces response.
type agentIPAddress struct {
	Address string `json:"ip-address"`
	Type    string `json:"ip-address-type"`
}

// agentInterface is one interface entry from the guest agent's
// network-get-interfaces response.
type agentInterface struct {
	Name      string           `json:"name"`
	Addresses []agentIPAddress `json:"ip-addresses"`
}

// guestIP discovers a VM's first usable IPv4 address via the QEMU guest agent's
// network-get-interfaces endpoint, skipping loopback. It is used when --host is
// not supplied; the error names --host as the workaround when the agent is
// unreachable or exposes no usable address.
func guestIP(ctx context.Context, deps *cli.Deps, node, vmid string) (string, error) {
	resp, err := deps.API.Nodes.ListQemuAgentNetworkGetInterfaces(ctx, node, vmid)
	if err != nil {
		return "", fmt.Errorf(
			"query guest agent for VM %s on node %q: %w; pass --host to connect directly",
			vmid, node, err)
	}

	var raw json.RawMessage
	if resp != nil {
		raw = *resp
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "", fmt.Errorf(
			"guest agent for VM %s on node %q returned no network interfaces; pass --host to connect directly",
			vmid, node)
	}

	// The agent payload is sometimes a bare array of interfaces and sometimes an
	// object wrapping them under "result"; accept both.
	var ifaces []agentInterface
	if err := json.Unmarshal(raw, &ifaces); err != nil {
		var wrapped struct {
			Result []agentInterface `json:"result"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return "", fmt.Errorf("decode guest agent network interfaces for VM %s: %w", vmid, err)
		}
		ifaces = wrapped.Result
	}

	for _, iface := range ifaces {
		if isLoopbackName(iface.Name) {
			continue
		}
		for _, addr := range iface.Addresses {
			if !isIPv4Addr(addr) {
				continue
			}
			if isLoopbackIP(addr.Address) {
				continue
			}
			return addr.Address, nil
		}
	}

	return "", fmt.Errorf(
		"no non-loopback IPv4 address found via the guest agent for VM %s on node %q; pass --host to connect directly",
		vmid, node)
}

// isIPv4Addr reports whether the agent address is IPv4. The guest agent
// normally tags addresses with ip-address-type, but some agents omit it; fall
// back to parsing the literal when the type is absent.
func isIPv4Addr(addr agentIPAddress) bool {
	switch addr.Type {
	case "ipv4":
		return true
	case "":
		ip := net.ParseIP(addr.Address)
		return ip != nil && ip.To4() != nil
	default:
		return false
	}
}

// isLoopbackName reports whether the interface name is the loopback device.
func isLoopbackName(name string) bool {
	return name == "lo"
}

// isLoopbackIP reports whether addr is an IPv4 or IPv6 loopback address.
func isLoopbackIP(addr string) bool {
	return strings.HasPrefix(addr, "127.") || addr == "::1"
}
