package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/nodeaddr"
	"github.com/fivetwenty-io/proxmox-cli/internal/sshcmd"
)

// sshJumpNone is the literal an operator writes to ssh.jump to say "no jump,
// dial direct". The API side and `pmx ssh` both read it that way, so an ssh
// call built here must not hand ssh a "-J none" that it would take for a
// jump host literally named none.
const sshJumpNone = "none"

// contextSSHJump returns the ssh -J destination list every lab ssh call uses,
// or "" for a direct dial. When the invocation built a client, that is the
// bastion the API connection itself dials through (deps.Route.Jump), so
// --api-jump and $PMX_API_JUMP move the ssh calls together with the API
// calls, and "none" from any source dials direct. Without a client it is the
// context's own ssh.jump, with the "none" literal read as no jump.
// deps.Ctx must be non-nil.
func contextSSHJump(deps *cli.Deps) string {
	if deps.Route.Host != "" {
		return deps.Route.Jump.Chain
	}

	jump := deps.Ctx.SSH.Jump
	if jump == sshJumpNone {
		return ""
	}
	return jump
}

// labDatasetNodePolicy says what a verb does when the lab has no VM to show
// which node hosts its dataset.
type labDatasetNodePolicy int

const (
	// labDatasetNodeAmbient falls back to the invocation's node: --node,
	// $PMX_NODE, or the context's default node. It is for a verb that sets a
	// property and can be re-run.
	labDatasetNodeAmbient labDatasetNodePolicy = iota

	// labDatasetNodeExplicit requires --node on the command line. It is for a
	// verb that destroys the dataset, where an ambient node could be the
	// wrong machine.
	labDatasetNodeExplicit
)

// labDatasetNode picks the outer PVE node that hosts a lab's ZFS dataset for
// a verb that has to reach it over ssh.
//
// When the lab has VMs, the node they live on decides, because that is where
// create put the dataset. An ambient node ($PMX_NODE or the context's default
// node) never overrides them. VMs on more than one node are refused, since
// there is no one node to act on, and so is an explicit --node that names a
// different node than the VMs, since one of the two is wrong and the verb
// cannot tell which.
//
// When the lab has no VMs the policy decides: labDatasetNodeAmbient takes the
// invocation's node, and labDatasetNodeExplicit refuses unless --node was
// passed. verb names the command in the errors.
func labDatasetNode(deps *cli.Deps, verb string, vmNodes []string, policy labDatasetNodePolicy) (string, error) {
	distinct := distinctNodes(vmNodes)

	switch len(distinct) {
	case 0:
		switch {
		case policy == labDatasetNodeExplicit && (!deps.NodeExplicit || deps.Node == ""):
			return "", fmt.Errorf(
				"%s found no VM of the lab, so nothing shows which node hosts its ZFS dataset; "+
					"pass --node naming that node", verb)
		case deps.Node == "":
			return "", fmt.Errorf(
				"%s needs the outer PVE node that hosts the lab's ZFS dataset; "+
					"pass --node, set $PMX_NODE, or configure a context default node", verb)
		}
		return deps.Node, nil

	case 1:
		if deps.NodeExplicit && deps.Node != "" && deps.Node != distinct[0] {
			return "", fmt.Errorf(
				"%s: --node %q disagrees with node %q, where the lab's VMs live and where its ZFS dataset is; "+
					"drop --node or pass the node the lab lives on", verb, deps.Node, distinct[0])
		}
		return distinct[0], nil

	default:
		return "", fmt.Errorf(
			"%s: the lab's VMs live on more than one node (%s), so there is no single node whose ZFS dataset "+
				"to act on; move the VMs to one node or act on the dataset by hand",
			verb, strings.Join(distinct, ", "))
	}
}

// distinctNodes returns the sorted, non-empty, de-duplicated names in nodes.
func distinctNodes(nodes []string) []string {
	seen := make(map[string]struct{}, len(nodes))
	var out []string
	for _, n := range nodes {
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// labPoolVMNodes returns the node of every live VM in pool, so a dataset verb
// can ask where the lab actually lives. Unlike findLabVMs it does not refuse a
// member whose name matches no lab convention: such a VM is still a guest on
// some node, and that node still counts.
func labPoolVMNodes(vms []labVM, pool string) []string {
	var nodes []string
	for _, vm := range vms {
		if vm.Pool == pool {
			nodes = append(nodes, vm.Node)
		}
	}
	return nodes
}

// resolveLabDatasetSSH resolves where and how to reach the lab's ZFS dataset
// over ssh: the connection flags from the active context, with the ssh jump
// of the route the API itself takes (createDatasetSSHFlags), and the node's
// own address from cluster status (createDatasetSSHHost), exactly as the
// create verb's dataset step does. It is the single resolution every lab verb
// that runs zfs on the outer node shares, so none of them can fall back to the
// context's own host, which on a multi-node cluster is only the API
// entrypoint and may not be reachable by ssh at all.
//
// With strict set, the address must come from cluster status. A node that the
// API's cluster does not list, an empty list, or a failed lookup is an error,
// where the lenient lookup would hand ssh the bare node name to resolve
// through DNS or ~/.ssh/config, possibly to a machine in a different cluster.
// A verb that destroys data passes strict.
func resolveLabDatasetSSH(
	ctx context.Context, deps *cli.Deps, verb, node string, strict bool,
) (sshcmd.Flags, string, error) {
	if deps.Ctx == nil {
		return sshcmd.Flags{}, "", fmt.Errorf(
			"%s requires an active pmx context to resolve an ssh target; select one with --context/-c", verb)
	}
	if deps.API == nil {
		return sshcmd.Flags{}, "", fmt.Errorf("%s requires an API client to resolve the address of node %q", verb, node)
	}
	if err := labDatasetProxyRefusal(deps, verb); err != nil {
		return sshcmd.Flags{}, "", err
	}

	var (
		host string
		err  error
	)
	if strict {
		host, err = nodeaddr.ResolveStrict(ctx, deps.API.Cluster, node)
		if err != nil {
			return sshcmd.Flags{}, "", fmt.Errorf(
				"%s will not guess the address of node %q: %w; "+
					"nothing was changed, so fix the node name or the cluster connection and retry", verb, node, err)
		}
	} else {
		host, err = createDatasetSSHHost(ctx, deps.API, node, deps.Log)
		if err != nil {
			return sshcmd.Flags{}, "", err
		}
	}

	return createDatasetSSHFlags(deps), host, nil
}

// labProxyFunc builds the function that names the proxy for a request, as the
// API transport does. It is a seam so a test can name an environment, because
// net/http reads the proxy variables only once per process.
var labProxyFunc = apiclient.ProxyFunc

// labDatasetProxyRefusal refuses a dataset ssh step when the API connection
// reaches the cluster through a proxy. ssh does not follow --api-proxy,
// $PMX_API_PROXY, a context's proxy.url, or --api-proxy-from-env, so nothing
// would show that the machine ssh lands on is the one the API described, and
// the same private address can belong to a different cluster on the other side
// of the proxy. It returns nil when the route carries no proxy, and when
// --api-proxy-from-env is set but the environment names no proxy for the API's
// address.
func labDatasetProxyRefusal(deps *cli.Deps, verb string) error {
	p := deps.Route.Proxy

	proxied := p.URL != nil
	if !proxied && p.FromEnv {
		pf, err := labProxyFunc(p)
		if err != nil {
			return fmt.Errorf("%s: the API proxy settings are unusable (%w), so the ssh target cannot be checked", verb, err)
		}

		scheme := deps.Route.Protocol
		if scheme == "" {
			scheme = "https"
		}

		u := &url.URL{
			Scheme: scheme,
			Host:   net.JoinHostPort(strings.Trim(deps.Route.Host, "[]"), strconv.Itoa(deps.Route.Port)),
		}
		got, perr := pf(&http.Request{URL: u})
		if perr != nil {
			return fmt.Errorf("%s: the API proxy settings are unusable (%w), so the ssh target cannot be checked", verb, perr)
		}
		proxied = got != nil
	}
	if !proxied {
		return nil
	}

	via := p.String()
	if deps.Route.ProxySource != "" {
		via += " from " + deps.Route.ProxySource
	}

	return fmt.Errorf(
		"%s will not run ssh while the API connection goes through a proxy (%s). ssh cannot follow the proxy, "+
			"so nothing shows the machine it reaches is the node the API described, and the same private "+
			"address can belong to another cluster. Run it from a host that reaches the API without a proxy",
		verb, via)
}

// datasetIdentityCommand is the remote command of the identity preflight. It
// prints the machine's host name, then the SHA-256 fingerprint of the
// certificate PVE serves for the node. Both lines must come from that one
// connection.
const datasetIdentityCommand = "hostname && openssl x509 -noout -fingerprint -sha256 -in /etc/pve/local/pve-ssl.pem"

// datasetPreflightConnectTimeoutSec bounds how long the identity preflight
// waits to connect, so an unreachable target is reported instead of hanging.
const datasetPreflightConnectTimeoutSec = 10

// comparableFingerprint lowercases a certificate fingerprint and drops its
// colons, so the openssl form (SHA256 Fingerprint=AA:BB) and the API's form
// compare equal.
func comparableFingerprint(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
}

// apiNodeFingerprint returns the SHA-256 fingerprint of the pve-ssl.pem the API
// reports for node, from GET /nodes/{node}/certificates/info.
func apiNodeFingerprint(ctx context.Context, deps *cli.Deps, node string) (string, error) {
	infos, err := deps.API.Nodes.ListCertificatesInfo(ctx, node)
	if err != nil {
		return "", fmt.Errorf("read the certificates of node %q: %w", node, err)
	}
	if infos == nil {
		return "", fmt.Errorf("the API listed no certificates for node %q", node)
	}

	for _, raw := range *infos {
		var cert struct {
			Filename    string `json:"filename"`
			Fingerprint string `json:"fingerprint"`
		}
		if err := json.Unmarshal(raw, &cert); err != nil {
			continue
		}
		if cert.Filename != "pve-ssl.pem" {
			continue
		}
		if comparableFingerprint(cert.Fingerprint) == "" {
			break
		}
		return cert.Fingerprint, nil
	}

	return "", fmt.Errorf("the API did not report a pve-ssl.pem fingerprint for node %q", node)
}

// parseDatasetIdentity reads the output of datasetIdentityCommand: the host
// name on the first line, and the fingerprint after the "=" of the openssl
// line.
func parseDatasetIdentity(stdout string) (hostname, fingerprint string, err error) {
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.Contains(line, "Fingerprint="):
			_, fingerprint, _ = strings.Cut(line, "=")
		case hostname == "":
			hostname = line
		}
	}

	if hostname == "" || comparableFingerprint(fingerprint) == "" {
		return "", "", fmt.Errorf("the reply had no host name and certificate fingerprint (got %q)", strings.TrimSpace(stdout))
	}

	return hostname, fingerprint, nil
}

// verifyLabDatasetTarget proves that the exact ssh target the dataset step is
// about to use, host reached with flags f, is the PVE node the API described.
// It runs over that same host and flags, reads the machine's host name and the
// SHA-256 fingerprint of /etc/pve/local/pve-ssl.pem, and compares them with the
// node's name and with the pve-ssl.pem fingerprint the API reports for the node.
// Without it, a private address that two clusters share, or an ssh route that
// differs from the API's, could point a destructive zfs command at another
// cluster's dataset of the same name.
//
// It refuses, naming the node and both values, on a host name mismatch, a
// fingerprint mismatch, an unreachable target, and an API that will not return
// the fingerprint. Callers run it before anything is deleted.
func verifyLabDatasetTarget(
	ctx context.Context, deps *cli.Deps, verb, node string, f sshcmd.Flags, host string,
) error {
	if deps.Runner == nil {
		return fmt.Errorf("%s needs a command runner to check the ssh target", verb)
	}

	want, err := apiNodeFingerprint(ctx, deps, node)
	if err != nil {
		return fmt.Errorf("%s cannot confirm that ssh %s@%s reaches node %q, so it stops before changing anything: %w",
			verb, f.User, host, node, err)
	}

	argv := append(sshcmd.OptionArgs(&f), "-o", fmt.Sprintf("ConnectTimeout=%d", datasetPreflightConnectTimeoutSec),
		sshcmd.Dest(&f, host), datasetIdentityCommand)

	var stdout, stderr bytes.Buffer
	if err := deps.Runner.Run("ssh", argv, nil, nil, &stdout, &stderr); err != nil {
		reach := "could not be reached"
		if code := exec.ExitCodeOf(err); code != sshTransportExitCode {
			reach = fmt.Sprintf("did not answer the identity check (exit %d)", code)
		}

		return fmt.Errorf("%s: ssh %s@%s %s, so it cannot confirm the target is node %q and stops before changing "+
			"anything: %w (stderr: %s)", verb, f.User, host, reach, node, err, strings.TrimSpace(stderr.String()))
	}

	gotHost, gotFP, err := parseDatasetIdentity(stdout.String())
	if err != nil {
		return fmt.Errorf("%s: ssh %s@%s gave no usable identity for node %q, so it stops before changing anything: %w",
			verb, f.User, host, node, err)
	}

	if !strings.EqualFold(gotHost, node) {
		return fmt.Errorf(
			"%s: ssh %s@%s reached a machine named %q, not node %q, so it stops before changing anything; "+
				"the address may belong to another cluster",
			verb, f.User, host, gotHost, node)
	}

	if comparableFingerprint(gotFP) != comparableFingerprint(want) {
		return fmt.Errorf(
			"%s: ssh %s@%s reached a machine named %q whose pve-ssl.pem fingerprint is %s, but the API reports %s for "+
				"node %q, so it stops before changing anything; the address may belong to another cluster",
			verb, f.User, host, gotHost, gotFP, want, node)
	}

	return nil
}
