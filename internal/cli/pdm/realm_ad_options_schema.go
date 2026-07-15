package pdm

// The AD realm option schema table (realmAdOptionSchemas in
// realm_ad_options_schema_gen.go) is generated from the PDM API schema for
// POST /config/access/ad. "realm" is the create call's own identity
// parameter; "password" is a write-only bind credential the API never
// echoes back and must not appear in the schema table either.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pdm-apidoc.json -path /config/access/ad -verb POST -symbol realmAdOptionSchemas -exclude "realm,password" -out realm_ad_options_schema_gen.go
