package apiclient

import (
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs"
)

// BuildPBSOptions maps individual configuration fields onto a pve.Options
// value ready for pbs.NewClient / NewPBSClient. Parameters and authentication
// priority are identical to BuildOptions; see its doc comment for the token /
// ticket / password precedence and TLS handling.
//
// The base options are built via BuildOptions and then passed through
// pbs.DefaultOptions, which fills only the fields still at their zero value:
//   - Port defaults to 8007 (pbs.DefaultPort) when the caller passed 0; an
//     explicit port (including PVE's 8006, for a PBS instance colocated on a
//     non-standard port) is preserved unchanged.
//   - APITokenName defaults to "PBSAPIToken".
//   - CookieName defaults to "PBSAuthCookie".
func BuildPBSOptions(
	host string,
	port int,
	protocol string,
	username string,
	realm string,
	token string,
	password string,
	ticket string,
	csrf string,
	insecure bool,
	fingerprint string,
) pve.Options {
	opts := BuildOptions(host, port, protocol, username, realm, token, password, ticket, csrf, insecure, fingerprint)

	return pbs.DefaultOptions(opts)
}
