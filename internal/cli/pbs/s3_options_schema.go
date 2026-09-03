package pbs

// The S3 endpoint option schema table (s3OptionSchemas in
// s3_options_schema_gen.go) is generated from the PBS API schema for
// POST /config/s3. "id" is the create call's own identity parameter, not an
// option. "secret-key" is write-only credential material and must never be
// listed alongside the other options.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/s3 -verb POST -symbol s3OptionSchemas -exclude "id,secret-key" -out s3_options_schema_gen.go
