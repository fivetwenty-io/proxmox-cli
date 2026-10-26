package node_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// The JSON shape of node cert acme status, mirrored here because this file
// tests the command from outside the package.
type acmeDomain struct {
	Domain     string `json:"domain"`
	Source     string `json:"source"`
	Plugin     string `json:"plugin"`
	Alias      string `json:"alias,omitempty"`
	PluginType string `json:"plugin_type,omitempty"`
	PluginAPI  string `json:"plugin_api,omitempty"`
}

type acmeAccountStatus struct {
	Name       string   `json:"name"`
	Registered bool     `json:"registered"`
	Status     string   `json:"status,omitempty"`
	Directory  string   `json:"directory,omitempty"`
	Contact    []string `json:"contact,omitempty"`
}

type acmeTimerStatus struct {
	LoadState   string `json:"load_state"`
	ActiveState string `json:"active_state"`
	NextRun     string `json:"next_run"`
	LastRun     string `json:"last_run"`
}

type acmeStatus struct {
	Node        string            `json:"node"`
	Account     acmeAccountStatus `json:"account"`
	Domains     []acmeDomain      `json:"domains"`
	Certificate *struct {
		File     string   `json:"file"`
		Issuer   string   `json:"issuer"`
		SAN      []string `json:"san"`
		DaysLeft *int     `json:"days_left"`
	} `json:"certificate"`
	Renewal struct {
		By    string           `json:"by"`
		Timer *acmeTimerStatus `json:"timer"`
		Note  string           `json:"note"`
	} `json:"renewal"`
	Problems []string `json:"problems"`
	Notes    []string `json:"notes"`
}

// acmeStatusFixture is the API state one status test serves.
type acmeStatusFixture struct {
	config    map[string]any
	accounts  []map[string]any
	account   map[string]any // nil answers GET /cluster/acme/account/{name} with 403
	plugins   []map[string]any
	pluginErr bool
	certs     []map[string]any
}

// healthyACMEFixture is a node whose ACME setup has nothing wrong with it.
func healthyACMEFixture() acmeStatusFixture {
	now := time.Now()
	return acmeStatusFixture{
		config: map[string]any{
			"acme":        "account=le-prod",
			"acmedomain0": "pve1.example.com,plugin=cf-dns",
			"acmedomain1": "domain=alt.example.com,plugin=cf-dns,alias=_acme.example.net",
		},
		accounts: []map[string]any{{"name": "default"}, {"name": "le-prod"}},
		account: map[string]any{
			"directory": "https://acme-v02.api.letsencrypt.org/directory",
			"account":   map[string]any{"status": "valid", "contact": []string{"mailto:ops@example.com"}},
		},
		plugins: []map[string]any{
			{"plugin": "cf-dns", "type": "dns", "api": "cf", "data": "Q0ZfVG9rZW49c2VjcmV0"},
			{"plugin": "standalone", "type": "standalone"},
		},
		certs: []map[string]any{
			{"filename": "pve-ssl.pem", "subject": "/CN=pve1", "issuer": "/CN=Proxmox Virtual Environment"},
			{
				"filename": "pveproxy-ssl.pem", "subject": "/CN=pve1.example.com", "issuer": "/C=US/O=Let's Encrypt/CN=R11",
				"san":       []string{"pve1.example.com", "alt.example.com"},
				"notbefore": now.Add(-30 * 24 * time.Hour).Unix(), "notafter": now.Add(60 * 24 * time.Hour).Unix(),
				"fingerprint": "AA:BB",
			},
		},
	}
}

func (fx acmeStatusFixture) serve(f *testhelper.FakePVE) {
	f.HandleJSON("GET /api2/json/nodes/pve1/config", fx.config)
	f.HandleJSON("GET /api2/json/cluster/acme/account", fx.accounts)
	f.HandleFunc("GET /api2/json/cluster/acme/account/le-prod", func(w http.ResponseWriter, _ *http.Request) {
		if fx.account == nil {
			testhelper.WriteError(w, http.StatusForbidden, "Permission check failed (user != root@pam)")
			return
		}
		testhelper.WriteData(w, fx.account)
	})
	f.HandleFunc("GET /api2/json/cluster/acme/plugins", func(w http.ResponseWriter, _ *http.Request) {
		if fx.pluginErr {
			testhelper.WriteError(w, http.StatusForbidden, "Permission check failed (/, Sys.Modify)")
			return
		}
		testhelper.WriteData(w, fx.plugins)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/certificates/info", fx.certs)
}

// runACMEStatus runs node cert acme status against fx and returns the
// decoded JSON view.
func runACMEStatus(t *testing.T, fx acmeStatusFixture, runner exec.Runner, extra ...string) acmeStatus {
	t.Helper()
	f := testhelper.NewFakePVE(t)
	fx.serve(f)

	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, runner)
	root.SetArgs(append(append(prefix, "--node", "pve1", "node", "cert", "acme", "status"), extra...))
	require.NoError(t, root.Execute())

	// The test root writes stderr into the same buffer, so the JSON document
	// starts after the --insecure warning.
	out := buf.String()
	start := strings.Index(out, "{")
	require.GreaterOrEqual(t, start, 0, out)

	var got acmeStatus
	require.NoError(t, json.Unmarshal([]byte(out[start:]), &got), out)
	return got
}

func TestCertAcmeStatus_Healthy(t *testing.T) {
	got := runACMEStatus(t, healthyACMEFixture(), exec.Fake())

	require.Equal(t, "pve1", got.Node)
	require.Equal(t, acmeAccountStatus{
		Name: "le-prod", Registered: true, Status: "valid",
		Directory: "https://acme-v02.api.letsencrypt.org/directory",
		Contact:   []string{"mailto:ops@example.com"},
	}, got.Account)

	require.Equal(t, []acmeDomain{
		{Domain: "pve1.example.com", Source: "acmedomain0", Plugin: "cf-dns", PluginType: "dns", PluginAPI: "cf"},
		{
			Domain: "alt.example.com", Source: "acmedomain1", Plugin: "cf-dns", Alias: "_acme.example.net",
			PluginType: "dns", PluginAPI: "cf",
		},
	}, got.Domains)

	require.NotNil(t, got.Certificate)
	require.Equal(t, "pveproxy-ssl.pem", got.Certificate.File)
	require.Equal(t, "/C=US/O=Let's Encrypt/CN=R11", got.Certificate.Issuer)
	require.Equal(t, []string{"pve1.example.com", "alt.example.com"}, got.Certificate.SAN)
	require.NotNil(t, got.Certificate.DaysLeft)
	require.InDelta(t, 59, *got.Certificate.DaysLeft, 1)

	require.Equal(t, "pve-daily-update.timer", got.Renewal.By)
	require.Nil(t, got.Renewal.Timer, "the timer is never reported without --timer")
	require.Contains(t, got.Renewal.Note, "cannot see systemd timers")
	require.Empty(t, got.Problems)
	require.Empty(t, got.Notes)
}

func TestCertAcmeStatus_NeverShowsPluginData(t *testing.T) {
	for _, format := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
		f := testhelper.NewFakePVE(t)
		healthyACMEFixture().serve(f)

		root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
		root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "status"))
		require.NoError(t, root.Execute())
		require.NotContains(t, buf.String(), "Q0ZfVG9rZW49c2VjcmV0", "format %s", format)
		require.Contains(t, buf.String(), "pve-daily-update.timer", "format %s", format)
	}
}

func TestCertAcmeStatus_TableShowsEverySection(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	healthyACMEFixture().serve(f)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "status"))
	require.NoError(t, root.Execute())

	out := buf.String()
	for _, want := range []string{
		"le-prod", "domain.pve1.example.com", "plugin cf-dns (dns, api cf) [acmedomain0]",
		"alias _acme.example.net", "Let's Encrypt", "days left", "renewal.timer",
		"pass --timer", "none found",
	} {
		require.Contains(t, out, want)
	}
}

func TestCertAcmeStatus_TableListsProblemsInFull(t *testing.T) {
	fx := healthyACMEFixture()
	fx.accounts = []map[string]any{{"name": "default"}}
	fx.account = nil

	f := testhelper.NewFakePVE(t)
	fx.serve(f)
	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "status"))
	require.NoError(t, root.Execute())

	out := buf.String()
	require.Contains(t, out, "1, listed below")
	require.Contains(t, out, "Problems:\n  - ACME account \"le-prod\" is not registered, so orders and renewals "+
		"fail; register it with 'pmx pve cluster acme account create le-prod --contact <email>'")
	require.NotContains(t, out, "Notes:", "an unregistered account is never read, so there is nothing to note")
}

func TestCertAcmeStatus_ReportsProblems(t *testing.T) {
	fx := healthyACMEFixture()
	fx.config = map[string]any{
		"acme":        "domains=web.example.com",
		"acmedomain0": "pve1.example.com,plugin=gone-dns",
	}
	fx.accounts = []map[string]any{{"name": "other"}}
	fx.certs[1]["notafter"] = time.Now().Add(10 * 24 * time.Hour).Unix()

	got := runACMEStatus(t, fx, exec.Fake())

	require.Equal(t, "default", got.Account.Name, "the account defaults to default")
	require.False(t, got.Account.Registered)
	require.Len(t, got.Domains, 2)
	require.Equal(t, acmeDomain{
		Domain: "web.example.com", Source: "acme", Plugin: "standalone", PluginType: "standalone",
	}, got.Domains[1])

	problems := strings.Join(got.Problems, "\n")
	require.Contains(t, problems, `ACME account "default" is not registered`)
	require.Contains(t, problems, `uses ACME plugin "gone-dns", which does not exist`)
	require.Contains(t, problems, "inside the 30-day renewal window")
	require.Contains(t, problems, "does not cover web.example.com")
}

func TestCertAcmeStatus_NotesWhatItCannotRead(t *testing.T) {
	fx := healthyACMEFixture()
	fx.account = nil
	fx.pluginErr = true

	got := runACMEStatus(t, fx, exec.Fake())

	require.True(t, got.Account.Registered)
	require.Empty(t, got.Account.Directory)
	require.Empty(t, got.Domains[0].PluginAPI)
	notes := strings.Join(got.Notes, "\n")
	require.Contains(t, notes, "need root@pam with a password")
	require.Contains(t, notes, "needs Sys.Modify on /")
	require.Empty(t, got.Problems, "unreadable details are notes, not problems")
}

func TestCertAcmeStatus_SelfSignedOnly(t *testing.T) {
	fx := healthyACMEFixture()
	fx.certs = fx.certs[:1]

	got := runACMEStatus(t, fx, exec.Fake())

	require.Nil(t, got.Certificate)
	require.Contains(t, strings.Join(got.Problems, "\n"), "serves its self-signed pve-ssl.pem")
}

func TestCertAcmeStatus_TimerOverSSH(t *testing.T) {
	runner := exec.Fake(exec.FakeResponse{Stdout: "LoadState=loaded\nActiveState=active\n" +
		"NextElapseUSecRealtime=Tue 2026-10-27 01:12:00 UTC\nLastTriggerUSec=\n"})

	got := runACMEStatus(t, healthyACMEFixture(), runner, "--timer", "--port", "2222")

	require.Len(t, runner.Calls, 1)
	call := runner.Calls[0]
	require.Equal(t, "ssh", call.Name)
	require.Contains(t, call.Args, "BatchMode=yes")
	require.Contains(t, call.Args, "2222")
	require.Contains(t, call.Args, "root@192.168.1.10")
	require.Equal(t, []string{"systemctl", "show", "pve-daily-update.timer",
		"--property=LoadState,ActiveState,NextElapseUSecRealtime,LastTriggerUSec"}, call.Args[len(call.Args)-4:])

	require.Equal(t, &acmeTimerStatus{
		LoadState: "loaded", ActiveState: "active", NextRun: "Tue 2026-10-27 01:12:00 UTC", LastRun: "n/a",
	}, got.Renewal.Timer)
	require.Empty(t, got.Problems)
}

func TestCertAcmeStatus_TimerInactiveIsAProblem(t *testing.T) {
	runner := exec.Fake(exec.FakeResponse{Stdout: "LoadState=loaded\nActiveState=inactive\n" +
		"NextElapseUSecRealtime=\nLastTriggerUSec=\n"})

	got := runACMEStatus(t, healthyACMEFixture(), runner, "--timer")

	require.Contains(t, strings.Join(got.Problems, "\n"), "pve-daily-update.timer is inactive")
}

func TestCertAcmeStatus_TimerSSHFailureIsAnError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	healthyACMEFixture().serve(f)
	runner := exec.Fake(exec.FakeResponse{Err: errors.New("exit status 255")})

	root, _, prefix := newNodeRoot(t, f, output.FormatJSON, runner)
	root.SetArgs(append(prefix, "--node", "pve1", "node", "cert", "acme", "status", "--timer"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "read pve-daily-update.timer on node \"pve1\" over ssh")
	require.Contains(t, err.Error(), "run without --timer")
}
