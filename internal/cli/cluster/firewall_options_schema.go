package cluster

// The datacenter firewall option schema table (firewallOptionSchemas in
// firewall_options_schema_gen.go) is generated from the PVE API schema for
// PUT /cluster/firewall/options; the types and shared surfaces live in
// internal/optionschema.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -path /cluster/firewall/options -symbol firewallOptionSchemas -out firewall_options_schema_gen.go
