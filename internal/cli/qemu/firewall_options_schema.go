package qemu

// The per-VM firewall option schema table (firewallOptionSchemas in
// firewall_options_schema_gen.go) is generated from the PVE API schema for
// PUT /nodes/{node}/qemu/{vmid}/firewall/options; the types and shared
// surfaces live in internal/optionschema.

//go:generate go run github.com/fivetwenty-io/pve-cli/cmd/optionsgen -path /nodes/{node}/qemu/{vmid}/firewall/options -symbol firewallOptionSchemas -out firewall_options_schema_gen.go
