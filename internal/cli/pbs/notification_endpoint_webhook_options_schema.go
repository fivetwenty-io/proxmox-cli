package pbs

// The webhook endpoint option schema table (notifWebhookOptionSchemas in
// notification_endpoint_webhook_options_schema_gen.go) is generated from
// the PBS API schema for POST /config/notifications/endpoints/webhook.
// "name" is the create call's own identity parameter; "secret" carries
// write-only header values (the API returns only secret names, never
// values, per notifWebhookEntry's comment) and is excluded from the schema
// table for the same reason as the other write-only fields.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/notifications/endpoints/webhook -verb POST -symbol notifWebhookOptionSchemas -exclude "name,secret" -out notification_endpoint_webhook_options_schema_gen.go
