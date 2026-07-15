package pbs

// The sync job option schema table (syncJobOptionSchemas in
// sync_job_options_schema_gen.go) is generated from the PBS API schema for
// POST /config/sync. "id" is the create call's own identity parameter, not
// an option.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/sync -verb POST -symbol syncJobOptionSchemas -exclude "id" -out sync_job_options_schema_gen.go
