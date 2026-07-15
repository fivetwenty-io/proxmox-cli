package pbs

// The notification matcher option schema table (notifMatcherOptionSchemas
// in notification_matcher_options_schema_gen.go) is generated from the PBS
// API schema for POST /config/notifications/matchers. "name" is the create
// call's own identity parameter, not an option.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/notifications/matchers -verb POST -symbol notifMatcherOptionSchemas -exclude "name" -out notification_matcher_options_schema_gen.go
