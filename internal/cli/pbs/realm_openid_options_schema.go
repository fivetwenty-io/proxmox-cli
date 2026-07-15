package pbs

// The OpenID realm option schema table (realmOpenidOptionSchemas in
// realm_openid_options_schema_gen.go) is generated from the PBS API schema
// for POST /config/access/openid. "realm" is the create call's own identity
// parameter; "client-key" is a write-only client secret the API never
// echoes back and must not appear in the schema table either.

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/access/openid -verb POST -symbol realmOpenidOptionSchemas -exclude "realm,client-key" -out realm_openid_options_schema_gen.go
