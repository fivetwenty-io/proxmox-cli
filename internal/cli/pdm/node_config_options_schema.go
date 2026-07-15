package pdm

// The node config option schema table (nodeConfigOptionSchemas in
// node_config_options_schema_gen.go) is generated from the PDM API schema
// for PUT /nodes/{node}/config. "node" is the endpoint's own path parameter
// and is excluded automatically (see cmd/optionsgen); there is no other
// identity field in the body to exclude.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pdm-apidoc.json -path /nodes/{node}/config -verb PUT -symbol nodeConfigOptionSchemas -out node_config_options_schema_gen.go
