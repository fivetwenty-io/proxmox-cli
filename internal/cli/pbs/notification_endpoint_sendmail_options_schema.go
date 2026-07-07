package pbs

// The sendmail endpoint option schema table (notifSendmailOptionSchemas in
// notification_endpoint_sendmail_options_schema_gen.go) is generated from
// the PBS API schema for POST /config/notifications/endpoints/sendmail.
// "name" is the create call's own identity parameter, not an option.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/notifications/endpoints/sendmail -verb POST -symbol notifSendmailOptionSchemas -exclude "name" -out notification_endpoint_sendmail_options_schema_gen.go
