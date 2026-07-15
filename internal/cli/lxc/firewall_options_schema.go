package lxc

// The container firewall option schema table (firewallOptionSchemas in
// firewall_options_schema_gen.go) is generated from the PVE API schema for
// PUT /nodes/{node}/lxc/{vmid}/firewall/options; the types and shared surfaces
// live in internal/optionschema.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -path /nodes/{node}/lxc/{vmid}/firewall/options -symbol firewallOptionSchemas -out firewall_options_schema_gen.go
