package pbs

// The datastore option schema table (datastoreOptionSchemas in
// datastore_options_schema_gen.go) is generated from the PBS API schema for
// POST /config/datastore — the create schema, the only verb that carries
// every option. "name" is the create call's own identity parameter, not an
// option.

//go:generate go run github.com/fivetwenty-io/pve-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/datastore -verb POST -symbol datastoreOptionSchemas -exclude "name" -out datastore_options_schema_gen.go
