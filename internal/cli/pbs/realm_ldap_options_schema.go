package pbs

// The LDAP realm option schema table (realmLdapOptionSchemas in
// realm_ldap_options_schema_gen.go) is generated from the PBS API schema for
// POST /config/access/ldap. "realm" is the create call's own identity
// parameter; "password" is a write-only bind credential the API never
// echoes back and must not appear in the schema table either.

//go:generate go run github.com/fivetwenty-io/pmx-cli/cmd/optionsgen -source pbs-apidoc.json -path /config/access/ldap -verb POST -symbol realmLdapOptionSchemas -exclude "realm,password" -out realm_ldap_options_schema_gen.go
