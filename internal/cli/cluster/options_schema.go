package cluster

// The datacenter-option schema table (optionSchemas in options_schema_gen.go)
// is generated from the PVE API schema for PUT /cluster/options; the types
// and shared surfaces live in internal/optionschema.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -path /cluster/options -out options_schema_gen.go
