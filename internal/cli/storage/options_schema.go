package storage

// The storage option schema table (storageOptionSchemas in
// options_schema_gen.go) is generated from the PVE API schema for
// POST /storage — the create schema, because it is the only verb that carries
// every option including the create-only identity fields (path, portal,
// vgname, …). Which options are valid for which storage type is not part of
// the API schema; that mapping lives in type_options_gen.go, generated from
// the pve-storage plugin sources by scripts/storage_type_options.py.
// "storage" and "type" are the create call's own parameters, not options.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -path /storage -verb POST -symbol storageOptionSchemas -exclude "storage,type" -flag-override "target=iscsi-target" -out options_schema_gen.go
