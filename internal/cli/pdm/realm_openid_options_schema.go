package pdm

// The OpenID realm option schema table (realmOpenidOptionSchemas in
// realm_openid_options_schema_gen.go) is generated from the PDM API schema
// for POST /config/access/openid. "realm" is the create call's own identity
// parameter; "client-key" is a write-only client secret that must not
// appear in the schema table either (see realmOpenidSecretKeys in
// realm_openid.go for why the live value is also stripped from show/ls
// output, unlike the AD/LDAP password).

//go:generate go run github.com/fivetwenty-io/proxmox-cli/cmd/optionsgen -source pdm-apidoc.json -path /config/access/openid -verb POST -symbol realmOpenidOptionSchemas -exclude "realm,client-key" -out realm_openid_options_schema_gen.go
