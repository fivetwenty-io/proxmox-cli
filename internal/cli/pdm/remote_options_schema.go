package pdm

// The remote option schema table (remoteOptionSchemas in
// remote_options_schema_gen.go) is generated from the PDM API schema for
// POST /remotes/remote. "id" is the create call's own identity parameter,
// not an option. "token" is write-only credential material and must never
// be listed alongside the other options.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pdm-apidoc.json -path /remotes/remote -verb POST -symbol remoteOptionSchemas -exclude "id,token" -out remote_options_schema_gen.go
