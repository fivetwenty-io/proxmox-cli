package pbs

// The influxdb-http metric server option schema table
// (metricsInfluxdbHTTPOptionSchemas in metrics_influxdb_http_options_schema_gen.go)
// is generated from the PBS API schema for POST /config/metrics/influxdb-http.
// "name" is the create call's own identity parameter; "token" is a write-only
// credential the API never echoes back (see stripMetricsSecrets in
// metrics.go) and must not appear in the schema table either.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/metrics/influxdb-http -verb POST -symbol metricsInfluxdbHTTPOptionSchemas -exclude "name,token" -out metrics_influxdb_http_options_schema_gen.go
