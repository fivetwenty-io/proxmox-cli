package pbs

// The remote option schema table (remoteOptionSchemas in
// remote_options_schema_gen.go) is generated from the PBS API schema for
// POST /config/remote. "name" is the create call's own identity parameter,
// not an option. "password" is write-only credential material and must
// never be listed alongside the other options.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/remote -verb POST -symbol remoteOptionSchemas -exclude "name,password" -out remote_options_schema_gen.go
