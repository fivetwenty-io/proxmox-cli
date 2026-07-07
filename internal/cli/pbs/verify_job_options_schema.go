package pbs

// The verify job option schema table (verifyJobOptionSchemas in
// verify_job_options_schema_gen.go) is generated from the PBS API schema for
// POST /config/verify. "id" is the create call's own identity parameter,
// not an option.

//go:generate go run github.com/fivetwenty-io/pve-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/verify -verb POST -symbol verifyJobOptionSchemas -exclude "id" -out verify_job_options_schema_gen.go
