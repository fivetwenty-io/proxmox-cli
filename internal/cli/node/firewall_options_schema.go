package node

// The host firewall option schema table (firewallOptionSchemas in
// firewall_options_schema_gen.go) is generated from the PVE API schema for
// PUT /nodes/{node}/firewall/options; the types and shared surfaces live in
// internal/optionschema.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -path /nodes/{node}/firewall/options -symbol firewallOptionSchemas -out firewall_options_schema_gen.go
