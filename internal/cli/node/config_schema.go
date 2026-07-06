package node

// The node config option schema table (configSchemas in
// config_schema_gen.go) is generated from the PVE API schema for
// PUT /nodes/{node}/config; the types and shared surfaces live in
// internal/optionschema.

//go:generate go run github.com/fivetwenty-io/pve-cli/cmd/optionsgen -path /nodes/{node}/config -symbol configSchemas -out config_schema_gen.go -flag-override "acmedomain[n]=acme-domain"
