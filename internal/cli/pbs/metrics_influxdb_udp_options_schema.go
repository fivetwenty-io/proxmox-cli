package pbs

// The influxdb-udp metric server option schema table
// (metricsInfluxdbUDPOptionSchemas in metrics_influxdb_udp_options_schema_gen.go)
// is generated from the PBS API schema for POST /config/metrics/influxdb-udp.
// "name" is the create call's own identity parameter, not an option.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/metrics/influxdb-udp -verb POST -symbol metricsInfluxdbUDPOptionSchemas -exclude "name" -out metrics_influxdb_udp_options_schema_gen.go
