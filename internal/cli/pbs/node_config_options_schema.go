package pbs

// The node config option schema table (nodeConfigOptionSchemas in
// node_config_options_schema_gen.go) is generated from the PBS API schema for
// PUT /nodes/{node}/config — GET carries only the "node" path parameter, so
// PUT is the only verb with a real option set.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -source pbs-apidoc.json -path /nodes/{node}/config -verb PUT -symbol nodeConfigOptionSchemas -out node_config_options_schema_gen.go
