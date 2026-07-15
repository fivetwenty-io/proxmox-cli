package qemu

// The VM configuration option schema table (configSchemas in
// config_schema_gen.go) is generated from the PVE API schema for
// PUT /nodes/{node}/qemu/{vmid}/config; the types and shared surfaces live in
// internal/optionschema.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -path /nodes/{node}/qemu/{vmid}/config -symbol configSchemas -out config_schema_gen.go -exclude "delete,digest,revert,force,skiplock,background_delay" -flag-override "numa[n]=numa-node"
