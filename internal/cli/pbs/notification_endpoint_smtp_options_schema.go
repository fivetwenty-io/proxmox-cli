package pbs

// The smtp endpoint option schema table (notifSmtpOptionSchemas in
// notification_endpoint_smtp_options_schema_gen.go) is generated from the
// PBS API schema for POST /config/notifications/endpoints/smtp. "name" is
// the create call's own identity parameter; "password" is a write-only
// credential the API never echoes back and must not appear in the schema
// table either.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/notifications/endpoints/smtp -verb POST -symbol notifSmtpOptionSchemas -exclude "name,password" -out notification_endpoint_smtp_options_schema_gen.go
