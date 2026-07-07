package pbs

// The traffic-control rule option schema table (trafficOptionSchemas in
// traffic_options_schema_gen.go) is generated from the PBS API schema for
// POST /config/traffic-control. "name" is the create call's own identity
// parameter, not an option.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/traffic-control -verb POST -symbol trafficOptionSchemas -exclude "name" -out traffic_options_schema_gen.go
