package lab

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// preflightNodeIPs maps the addresses the fake cluster status lists to the node
// that owns each, so a fake ssh target can answer the identity preflight as that
// node would.
var preflightNodeIPs = map[string]string{
	"192.168.1.10": "pve1",
	"192.168.1.20": "pve2",
}

// preflightFingerprint returns the pve-ssl.pem fingerprint of a fake node, in
// the upper-case colon form the API reports.
func preflightFingerprint(node string) string {
	sum := sha256.Sum256([]byte("pve-ssl.pem of " + node))
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// registerNodeCertificates makes GET /nodes/{node}/certificates/info answer
// for every node of the fake cluster, with the fingerprint that node's fake ssh
// target prints, plus a certificate the preflight must ignore.
func registerNodeCertificates(f *testhelper.FakePVE) {
	for _, node := range preflightNodeIPs {
		f.HandleJSON("GET /api2/json/nodes/"+node+"/certificates/info", []any{
			map[string]any{"filename": "pve-root-ca.pem", "fingerprint": "00:11:22"},
			map[string]any{"filename": "pve-ssl.pem", "fingerprint": preflightFingerprint(node)},
		})
	}
}

// identityRunner is a runner that answers the dataset identity preflight as the
// node at the ssh destination would, and passes every other call to the
// embedded FakeRunner. The preflight calls are recorded in Probes and kept out
// of the FakeRunner's Calls, so a test that counts the zfs calls is unaffected.
type identityRunner struct {
	*exec.FakeRunner

	// Reply, when set, replaces the answer for one preflight. It receives the
	// destination host and returns the stdout and the error to give.
	Reply func(host string) (stdout string, err error)

	mu     sync.Mutex
	Probes []exec.Call
}

func newIdentityRunner(fake *exec.FakeRunner) *identityRunner {
	return &identityRunner{FakeRunner: fake}
}

// Run implements exec.Runner.
func (r *identityRunner) Run(
	name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer,
) error {
	if name != "ssh" || len(args) == 0 || args[len(args)-1] != datasetIdentityCommand {
		return r.FakeRunner.Run(name, args, env, stdin, stdout, stderr)
	}

	r.mu.Lock()
	r.Probes = append(r.Probes, exec.Call{Name: name, Args: args})
	r.mu.Unlock()

	host := args[len(args)-2]
	host = host[strings.LastIndex(host, "@")+1:]

	if r.Reply != nil {
		out, err := r.Reply(host)
		_, _ = io.WriteString(stdout, out)
		return err
	}

	node := preflightNodeIPs[host]
	_, _ = io.WriteString(stdout, node+"\nsha256 Fingerprint="+strings.ToLower(preflightFingerprint(node))+"\n")
	return nil
}

// identityProbes returns the preflight calls the deps' runner saw.
func identityProbes(t *testing.T, deps *cli.Deps) []exec.Call {
	t.Helper()

	r, ok := deps.Runner.(*identityRunner)
	require.True(t, ok, "the test wired no identity runner")

	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]exec.Call(nil), r.Probes...)
}

// replyIdentity makes the fake ssh target answer the preflight with the given
// host name and fingerprint line.
func replyIdentity(deps *cli.Deps, hostname, fingerprint string) {
	deps.Runner.(*identityRunner).Reply = func(string) (string, error) {
		return hostname + "\nsha256 Fingerprint=" + fingerprint + "\n", nil
	}
}

// TestParseDatasetIdentity covers the reply of the identity command.
func TestParseDatasetIdentity(t *testing.T) {
	host, fp, err := parseDatasetIdentity("pve1\nsha256 Fingerprint=ab:cd:ef\n")
	require.NoError(t, err)
	assert.Equal(t, "pve1", host)
	assert.Equal(t, "ab:cd:ef", fp)

	for name, in := range map[string]string{
		"empty":          "",
		"no fingerprint": "pve1\n",
		"no host":        "SHA256 Fingerprint=AA:BB\n",
		"blank fp":       "pve1\nsha256 Fingerprint=\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseDatasetIdentity(in)
			assert.Error(t, err)
		})
	}
}

// TestComparableFingerprint covers the comparison form: case and colons do not
// matter.
func TestComparableFingerprint(t *testing.T) {
	assert.Equal(t, "aabbcc", comparableFingerprint(" AA:bb:Cc\n"))
	assert.Equal(t, comparableFingerprint(preflightFingerprint("pve1")),
		comparableFingerprint(strings.ToLower(preflightFingerprint("pve1"))))
}

// TestLabDatasetProxyRefusal covers the proxy check: a proxy on the route
// refuses, and so does the environment's proxy for the API's address, while an
// environment with no proxy for it, or no proxy at all, does not.
func TestLabDatasetProxyRefusal(t *testing.T) {
	route := func(t *testing.T, o cli.ConnectionOverrides) *cli.Deps {
		t.Helper()

		conn, err := cli.ResolveConnection("sm", &config.Context{Host: "pve-0.example", Port: 8006}, o)
		require.NoError(t, err)
		return &cli.Deps{Route: conn}
	}

	t.Run("no proxy", func(t *testing.T) {
		assert.NoError(t, labDatasetProxyRefusal(route(t, cli.ConnectionOverrides{}), "the verb"))
	})

	t.Run("an explicit proxy refuses and says why", func(t *testing.T) {
		deps := route(t, cli.ConnectionOverrides{Proxy: "socks5://proxy.example:1080", ProxySource: "--api-proxy"})
		err := labDatasetProxyRefusal(deps, "the verb")
		require.Error(t, err)
		assert.ErrorContains(t, err, "the verb will not run ssh while the API connection goes through a proxy")
		assert.ErrorContains(t, err, "ssh cannot follow the proxy")
		assert.ErrorContains(t, err, "--api-proxy")
	})

	// net/http reads the proxy variables once per process, so the tests name the
	// environment through the seam instead.
	withEnvProxy := func(t *testing.T, proxy *url.URL, exempt string) {
		t.Helper()

		orig := labProxyFunc
		labProxyFunc = func(apiclient.ProxySpec) (func(*http.Request) (*url.URL, error), error) {
			return func(r *http.Request) (*url.URL, error) {
				if r.URL.Hostname() == exempt {
					return nil, nil
				}
				return proxy, nil
			}, nil
		}
		t.Cleanup(func() { labProxyFunc = orig })
	}
	fromEnv := cli.ConnectionOverrides{ProxyFromEnv: true, ProxyFromEnvSet: true, ProxySource: "--api-proxy-from-env"}

	t.Run("the environment's proxy refuses", func(t *testing.T) {
		withEnvProxy(t, &url.URL{Scheme: "http", Host: "envproxy.example:3128"}, "")
		require.Error(t, labDatasetProxyRefusal(route(t, fromEnv), "the verb"))
	})

	t.Run("an environment that exempts the API's address does not", func(t *testing.T) {
		withEnvProxy(t, &url.URL{Scheme: "http", Host: "envproxy.example:3128"}, "pve-0.example")
		assert.NoError(t, labDatasetProxyRefusal(route(t, fromEnv), "the verb"))
	})

	t.Run("an environment with no proxy does not", func(t *testing.T) {
		withEnvProxy(t, nil, "")
		assert.NoError(t, labDatasetProxyRefusal(route(t, fromEnv), "the verb"))
	})
}

// preflightCase is one way the identity preflight must refuse. Each case wires
// the fake ssh target or the API so the check fails, then expects the verb to
// name the node and say what it saw.
type preflightCase struct {
	name string
	// arrange breaks the check on deps and the fake API.
	arrange func(t *testing.T, f *testhelper.FakePVE, deps *cli.Deps)
	// want are the fragments the refusal must carry.
	want []string
}

func preflightCases() []preflightCase {
	return []preflightCase{
		{
			name: "hostname mismatch",
			arrange: func(_ *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				replyIdentity(deps, "pve7", preflightFingerprint("pve1"))
			},
			want: []string{`reached a machine named "pve7", not node "pve1"`, "another cluster"},
		},
		{
			name: "fingerprint mismatch",
			arrange: func(_ *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				replyIdentity(deps, "pve1", preflightFingerprint("somewhere else"))
			},
			want: []string{
				`machine named "pve1"`, preflightFingerprint("somewhere else"), preflightFingerprint("pve1"), `node "pve1"`,
			},
		},
		{
			name: "unreachable target",
			arrange: func(_ *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				deps.Runner.(*identityRunner).Reply = func(string) (string, error) {
					return "", &exec.ExitError{Code: 255}
				}
			},
			want: []string{"could not be reached", `node "pve1"`, "root@192.168.1.10"},
		},
		{
			name: "remote command failure",
			arrange: func(_ *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				deps.Runner.(*identityRunner).Reply = func(string) (string, error) {
					return "", &exec.ExitError{Code: 1}
				}
			},
			want: []string{"did not answer the identity check (exit 1)", `node "pve1"`},
		},
		{
			name: "empty reply",
			arrange: func(_ *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				deps.Runner.(*identityRunner).Reply = func(string) (string, error) { return "\n", nil }
			},
			want: []string{"gave no usable identity", `node "pve1"`},
		},
		{
			name: "API refuses the certificate list",
			arrange: func(_ *testing.T, f *testhelper.FakePVE, _ *cli.Deps) {
				f.HandleFunc("GET /api2/json/nodes/pve1/certificates/info", func(w http.ResponseWriter, _ *http.Request) {
					testhelper.WriteError(w, http.StatusForbidden, "Permission check failed")
				})
			},
			want: []string{`cannot confirm that ssh root@192.168.1.10 reaches node "pve1"`, "Permission check failed"},
		},
		{
			name: "API reports no pve-ssl.pem",
			arrange: func(_ *testing.T, f *testhelper.FakePVE, _ *cli.Deps) {
				f.HandleJSON("GET /api2/json/nodes/pve1/certificates/info", []any{
					map[string]any{"filename": "pve-root-ca.pem", "fingerprint": "00:11"},
				})
			},
			want: []string{`did not report a pve-ssl.pem fingerprint for node "pve1"`},
		},
		{
			name: "API reports a blank fingerprint",
			arrange: func(_ *testing.T, f *testhelper.FakePVE, _ *cli.Deps) {
				f.HandleJSON("GET /api2/json/nodes/pve1/certificates/info", []any{
					map[string]any{"filename": "pve-ssl.pem", "fingerprint": ""},
				})
			},
			want: []string{`did not report a pve-ssl.pem fingerprint for node "pve1"`},
		},
		{
			name: "proxy on the route",
			arrange: func(t *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				conn, err := cli.ResolveConnection("sm", &config.Context{Host: "pve-0.example", Port: 8006}, cli.ConnectionOverrides{
					Proxy: "socks5://proxy.example:1080", ProxySource: "--api-proxy",
				})
				require.NoError(t, err)
				deps.Route = conn
			},
			want: []string{"will not run ssh while the API connection goes through a proxy", "--api-proxy"},
		},
	}
}

// TestDestroy_PurgeDataset_IdentityPreflight covers the check that runs before
// anything is deleted. A target that is the node passes, and every way the
// target can fail to be the node refuses the run with nothing deleted and no
// zfs command issued.
func TestDestroy_PurgeDataset_IdentityPreflight(t *testing.T) {
	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	path := writeConfig(t, cfg)

	// wire builds the destroy of one VM, with every deleting route logging to
	// events so the order against the preflight shows.
	wire := func(t *testing.T) (*testhelper.FakePVE, *cobra.Command, *cli.Deps, *exec.FakeRunner, *[]string) {
		t.Helper()

		f, ac := destroyFakeClient(t)
		destroyPurgeDatasetFixture(t, f)

		events := &[]string{}
		for _, route := range []string{
			"DELETE /api2/json/nodes/pve1/qemu/100", "DELETE /api2/json/pools", "DELETE /api2/json/storage/tank-lab-alpha",
		} {
			f.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
				*events = append(*events, "delete "+r.URL.Path)
				if strings.Contains(r.URL.Path, "/qemu/100") {
					testhelper.WriteData(w, "UPID:pve1:00000002:00000002:65000000:qmdestroy:100:root@pam:")
					return
				}
				testhelper.WriteData(w, nil)
			})
		}

		cmd := destroyTestCmd(t, path, ac, "pve1")
		deps, fake := destroyWireSSH(t, cmd)
		return f, cmd, deps, fake, events
	}

	t.Run("a target that is the node passes, before any deletion, over the dataset step's own host and flags", func(t *testing.T) {
		_, cmd, deps, fake, events := wire(t)
		deps.Ctx.SSH.Port = 2222
		deps.Ctx.SSH.Identity = "/keys/lab"
		deps.Runner.(*identityRunner).Reply = func(host string) (string, error) {
			*events = append(*events, "preflight "+host)
			return "PVE1\nSHA256 Fingerprint=" + strings.ToLower(preflightFingerprint("pve1")) + "\n", nil
		}

		_, _, err := destroyRun(t, cmd, "alpha", "--yes", "--purge-dataset")
		require.NoError(t, err)

		require.NotEmpty(t, *events)
		assert.Equal(t, "preflight 192.168.1.10", (*events)[0], "the preflight runs before the first deletion")
		assert.Greater(t, len(*events), 1, "and the deletions follow it")

		probes := identityProbes(t, deps)
		require.Len(t, probes, 1)
		require.Len(t, fake.Calls, 1)
		zfs := fake.Calls[0].Args
		probe := probes[0].Args
		assert.Equal(t, zfs[:len(zfs)-1][:len(probe)-4], probe[:len(probe)-4],
			"the same options as the dataset step, up to the connect timeout")
		assert.Equal(t, []string{"-o", "ConnectTimeout=10", "root@192.168.1.10", datasetIdentityCommand}, probe[len(probe)-4:])
		assert.Contains(t, probe, "/keys/lab")
		assert.Contains(t, probe, "2222")
	})

	for _, tc := range preflightCases() {
		t.Run("refuses on "+tc.name+" and deletes nothing", func(t *testing.T) {
			f, cmd, deps, fake, events := wire(t)
			tc.arrange(t, f, deps)

			_, _, err := destroyRun(t, cmd, "alpha", "--yes", "--purge-dataset")
			require.Error(t, err)
			for _, want := range tc.want {
				assert.ErrorContains(t, err, want)
			}
			assert.Empty(t, *events, "nothing is deleted before the preflight passes")
			assert.Empty(t, fake.Calls, "no zfs command is issued")
		})
	}

	t.Run("dry-run does not connect", func(t *testing.T) {
		_, cmd, deps, fake, events := wire(t)

		_, _, err := destroyRun(t, cmd, "alpha", "--dry-run", "--purge-dataset")
		require.NoError(t, err)
		assert.Empty(t, identityProbes(t, deps))
		assert.Empty(t, fake.Calls)
		assert.Empty(t, *events)
	})

	t.Run("the preflight comes before the confirmation prompt", func(t *testing.T) {
		f, cmd, deps, fake, events := wire(t)
		replyIdentity(deps, "pve7", preflightFingerprint("pve1"))
		_ = f

		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		cmd.SetIn(strings.NewReader("y\n"))
		cmd.SetArgs([]string{"alpha", "--purge-dataset"})
		require.Error(t, cmd.Execute())
		assert.NotContains(t, stderr.String(), "[y/N]", "the operator is not asked to confirm a run that will be refused")
		assert.Empty(t, *events)
		assert.Empty(t, fake.Calls)
	})
}

// TestLabQuota_IdentityPreflight covers the same check for quota set: it runs
// before the prompt and before zfs changes anything.
func TestLabQuota_IdentityPreflight(t *testing.T) {
	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": cleanLab("alpha")}}
	path := writeConfig(t, cfg)

	wire := func(t *testing.T, flags ...string) (*testhelper.FakePVE, *cobra.Command, *cli.Deps, *exec.FakeRunner) {
		t.Helper()

		f := testhelper.NewFakePVE(t)
		registerNodeCertificates(f)
		cmd := quotaTestCmd(t, path, flags...)
		var stdout, stderr bytes.Buffer
		fake := wireQuotaDepsOn(t, cmd, f, "", &stdout, &stderr)
		return f, cmd, cli.GetDeps(cmd), fake
	}

	run := func(t *testing.T, cmd *cobra.Command) error {
		t.Helper()

		refquotaGB, dryRun, yes := quotaFlagValues(t, cmd)
		return runQuotaSet(cmd, "alpha", refquotaGB, dryRun, yes)
	}

	t.Run("a target that is the node passes and zfs then runs", func(t *testing.T) {
		_, cmd, deps, fake := wire(t, "--yes")

		require.NoError(t, run(t, cmd))
		require.Len(t, identityProbes(t, deps), 1)
		require.Len(t, fake.Calls, 1)
		assert.Contains(t, identityProbes(t, deps)[0].Args, "root@"+quotaTestNodeIP)
		assert.Contains(t, identityProbes(t, deps)[0].Args, "2222")
	})

	for _, tc := range preflightCases() {
		t.Run("refuses on "+tc.name+" and changes nothing", func(t *testing.T) {
			f, cmd, deps, fake := wire(t, "--yes")
			tc.arrange(t, f, deps)

			err := run(t, cmd)
			require.Error(t, err)
			for _, want := range tc.want {
				assert.ErrorContains(t, err, want)
			}
			assert.Empty(t, fake.Calls, "zfs set never runs")
		})
	}

	t.Run("dry-run does not connect", func(t *testing.T) {
		_, cmd, deps, fake := wire(t, "--dry-run")

		require.NoError(t, run(t, cmd))
		assert.Empty(t, identityProbes(t, deps))
		assert.Empty(t, fake.Calls)
	})

	t.Run("a node whose kernel hostname is an FQDN passes on its first label", func(t *testing.T) {
		_, cmd, deps, fake := wire(t, "--yes")
		replyIdentity(deps, "PVE1.lab.example.net", strings.ToLower(preflightFingerprint("pve1")))

		require.NoError(t, run(t, cmd))
		require.Len(t, fake.Calls, 1, "zfs runs once the FQDN's first label matches the node")
	})

	t.Run("an FQDN with another first label is still refused", func(t *testing.T) {
		_, cmd, deps, fake := wire(t, "--yes")
		replyIdentity(deps, "pve7.lab.example.net", strings.ToLower(preflightFingerprint("pve1")))

		err := run(t, cmd)
		require.Error(t, err)
		assert.ErrorContains(t, err, `"pve7.lab.example.net"`)
		assert.Empty(t, fake.Calls)
	})

	t.Run("an FQDN with the right label is still refused on a fingerprint mismatch", func(t *testing.T) {
		_, cmd, deps, fake := wire(t, "--yes")
		replyIdentity(deps, "pve1.lab.example.net", preflightFingerprint("pve2"))

		err := run(t, cmd)
		require.Error(t, err)
		assert.ErrorContains(t, err, "fingerprint")
		assert.Empty(t, fake.Calls)
	})
}

// TestSameHostLabel pins the host name comparison of the identity preflight:
// only the label before the first dot counts, on both sides, without case.
func TestSameHostLabel(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"pve1", "pve1", true},
		{"PVE1", "pve1", true},
		{"pve1.example.net", "pve1", true},
		{"pve1", "pve1.example.net", true},
		{"pve1.a.example", "PVE1.b.example", true},
		{"pve1\n", "pve1", true},
		{"pve10", "pve1", false},
		{"pve1x.example", "pve1", false},
		{"pve2.example", "pve1.example", false},
		{".pve1", "pve1", false},
	} {
		assert.Equal(t, tc.want, sameHostLabel(tc.a, tc.b), "%q vs %q", tc.a, tc.b)
	}
}

// TestCreate_DatasetStep_RefusesAProxiedRoute covers the dataset step of create:
// ssh cannot follow the API's proxy, so the step refuses before any ssh runs.
func TestCreate_DatasetStep_RefusesAProxiedRoute(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := createTestLab("wayne")
	lab.Network.Mgmt.Subnet = "10.10.1.0/24"
	createRegisterContextHookFreshVMRoutes(f, t, lab)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})

	cmd, fake := buildCreateCmdWithSSH(t, path, f, "node1")
	deps := cli.GetDeps(cmd)
	conn, err := cli.ResolveConnection("sm", &config.Context{Host: "pve-0.example", Port: 8006}, cli.ConnectionOverrides{
		Proxy: "http://proxy.example:3128", ProxySource: "$PMX_API_PROXY",
	})
	require.NoError(t, err)
	deps.Route = conn

	_, err = runCreateCmd(t, cmd, "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "lab create will not run ssh while the API connection goes through a proxy")
	assert.ErrorContains(t, err, "$PMX_API_PROXY")
	assert.Empty(t, fake.Calls)
}

// TestCreate_DatasetStep_IdentityPreflight covers the check create makes before
// its first ssh command reaches the dataset target. A target that is the node
// passes and the probe and the zfs create follow it over the same options. A
// target that is not the node stops create before any step, and so before any
// API mutation, with no zfs command issued. A dry run connects to nothing.
func TestCreate_DatasetStep_IdentityPreflight(t *testing.T) {
	const node = "node1"

	// wire builds create against a lab whose shared resources exist and whose
	// VM does not, so the only mutating API call a run can make is the VM
	// create, which every test here either allows or forbids.
	wire := func(t *testing.T, allowVM bool, responses ...exec.FakeResponse) (*testhelper.FakePVE, *cobra.Command, *cli.Deps, *exec.FakeRunner) {
		t.Helper()

		f := testhelper.NewFakePVE(t)
		lab := createTestLab("wayne")
		createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
		f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
		f.HandleJSON("GET /api2/json/cluster/nextid", "9500")
		if allowVM {
			f.HandleJSON("POST /api2/json/nodes/node1/qemu", createTestUPID)
			createHandleTaskStatus(f)
		} else {
			createForbid(f, t, "POST /api2/json/nodes/node1/qemu")
		}

		path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
		fake := exec.Fake(responses...)
		cmd := buildCreateCmdWithZFSFake(t, path, f, node, fake)
		return f, cmd, cli.GetDeps(cmd), fake
	}

	t.Run("a target that is the node passes before the probe, over the dataset step's own options", func(t *testing.T) {
		_, cmd, deps, fake := wire(t, true,
			exec.FakeResponse{ExitCode: 1}, // remote "zfs list": dataset absent
			exec.FakeResponse{},            // zfs create
		)

		_, err := runCreateCmd(t, cmd, "wayne", "--node", node)
		require.NoError(t, err)

		probes := identityProbes(t, deps)
		require.Len(t, probes, 1)
		require.Len(t, fake.Calls, 2, "the zfs probe and the zfs create still run")
		probe := probes[0].Args
		assert.Equal(t, []string{"-o", "ConnectTimeout=10", "root@" + createDatasetTestNodeIP, datasetIdentityCommand},
			probe[len(probe)-4:])
		assert.Equal(t, fake.Calls[0].Args[:3], probe[:3], "the same port and ProxyJump option as the dataset ssh calls")
	})

	t.Run("an FQDN kernel hostname passes on its first label", func(t *testing.T) {
		_, cmd, deps, fake := wire(t, true, exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{})
		replyIdentity(deps, "NODE1.lab.example.net", strings.ToLower(preflightFingerprint(node)))

		_, err := runCreateCmd(t, cmd, "wayne", "--node", node)
		require.NoError(t, err)
		assert.Len(t, fake.Calls, 2)
	})

	for _, tc := range []struct {
		name    string
		arrange func(t *testing.T, f *testhelper.FakePVE, deps *cli.Deps)
		want    []string
	}{
		{
			name: "hostname mismatch",
			arrange: func(_ *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				replyIdentity(deps, "pve7", preflightFingerprint(node))
			},
			want: []string{`reached a machine named "pve7", not node "node1"`, "another cluster"},
		},
		{
			name: "fingerprint mismatch",
			arrange: func(_ *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				replyIdentity(deps, node, preflightFingerprint("somewhere else"))
			},
			want: []string{preflightFingerprint("somewhere else"), preflightFingerprint(node)},
		},
		{
			name: "unreachable target",
			arrange: func(_ *testing.T, _ *testhelper.FakePVE, deps *cli.Deps) {
				deps.Runner.(*identityRunner).Reply = func(string) (string, error) {
					return "", &exec.ExitError{Code: 255}
				}
			},
			want: []string{"could not be reached", "root@" + createDatasetTestNodeIP},
		},
		{
			name: "API reports no pve-ssl.pem",
			arrange: func(_ *testing.T, f *testhelper.FakePVE, _ *cli.Deps) {
				f.HandleJSON("GET /api2/json/nodes/node1/certificates/info", []any{
					map[string]any{"filename": "pve-root-ca.pem", "fingerprint": "00:11"},
				})
			},
			want: []string{`did not report a pve-ssl.pem fingerprint for node "node1"`},
		},
	} {
		t.Run("refuses on "+tc.name+" before any step and before any zfs command", func(t *testing.T) {
			f, cmd, deps, fake := wire(t, false)
			tc.arrange(t, f, deps)

			_, err := runCreateCmd(t, cmd, "wayne", "--node", node)
			require.Error(t, err)
			for _, want := range tc.want {
				assert.ErrorContains(t, err, want)
			}
			assert.ErrorContains(t, err, "lab create had not applied any step")
			assert.ErrorContains(t, err, "nothing to undo")
			assert.Empty(t, fake.Calls, "no zfs command is issued")
		})
	}

	t.Run("a dry run connects to nothing", func(t *testing.T) {
		_, cmd, deps, fake := wire(t, false)

		_, err := runCreateCmd(t, cmd, "wayne", "--node", node, "--dry-run")
		require.NoError(t, err)
		assert.Empty(t, identityProbes(t, deps))
		assert.Empty(t, fake.Calls)
	})
}

// TestDestroyHelp_PurgeDatasetFlagMatchesTheLongHelp keeps the flag from
// pointing at the context host, which the verb no longer uses.
func TestDestroyHelp_PurgeDatasetFlagMatchesTheLongHelp(t *testing.T) {
	cmd := newDestroyCmd()
	flag := cmd.Flags().Lookup("purge-dataset")
	require.NotNil(t, flag)

	assert.NotContains(t, flag.Usage, "context host")
	assert.Contains(t, flag.Usage, "node that hosts the lab's VMs")
	assert.Contains(t, cmd.Long, "pve-ssl.pem fingerprint")
	assert.Contains(t, cmd.Long, "goes through a proxy")
}
