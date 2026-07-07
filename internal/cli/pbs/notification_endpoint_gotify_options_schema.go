package pbs

// The gotify endpoint option schema table (notifGotifyOptionSchemas in
// notification_endpoint_gotify_options_schema_gen.go) is generated from the
// PBS API schema for POST /config/notifications/endpoints/gotify. "name" is
// the create call's own identity parameter; "token" is a write-only
// credential the API never echoes back and must not appear in the schema
// table either.

//go:generate go run github.com/fivetwenty-io/pve-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/notifications/endpoints/gotify -verb POST -symbol notifGotifyOptionSchemas -exclude "name,token" -out notification_endpoint_gotify_options_schema_gen.go
