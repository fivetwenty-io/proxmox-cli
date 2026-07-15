package sdn

// The vnet firewall option schema table (vnetFirewallOptionSchemas in
// vnet_firewall_options_schema_gen.go) is generated from the PVE API schema
// for PUT /cluster/sdn/vnets/{vnet}/firewall/options; the types and shared
// surfaces live in internal/optionschema.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -path /cluster/sdn/vnets/{vnet}/firewall/options -symbol vnetFirewallOptionSchemas -out vnet_firewall_options_schema_gen.go
