package pbs

// The prune job option schema table (pruneJobOptionSchemas in
// prune_job_options_schema_gen.go) is generated from the PBS API schema for
// POST /config/prune. "id" is the create call's own identity parameter, not
// an option.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/prune -verb POST -symbol pruneJobOptionSchemas -exclude "id" -out prune_job_options_schema_gen.go
