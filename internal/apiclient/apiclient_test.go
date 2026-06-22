package apiclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/tasks"
	pve "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/client"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
)

// sampleUPID is a syntactically valid Proxmox UPID used across test cases.
const sampleUPID = "UPID:pve:000A1B2C:0012D4E5:66F0A1B2:qmstart:100:root@pam:"

// ---------------------------------------------------------------------------
// UPIDFromRaw
// ---------------------------------------------------------------------------

func TestUPIDFromRaw_ValidQuotedString(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(sampleUPID)
	require.NoError(t, err)

	got, err := apiclient.UPIDFromRaw(raw)
	require.NoError(t, err)
	require.Equal(t, sampleUPID, got)
}

func TestUPIDFromRaw_EmptyRawMessage(t *testing.T) {
	t.Parallel()
	_, err := apiclient.UPIDFromRaw(json.RawMessage{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty raw message")
}

func TestUPIDFromRaw_EmptyStringValue(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`""`)
	_, err := apiclient.UPIDFromRaw(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty UPID string")
}

func TestUPIDFromRaw_NonUPIDString(t *testing.T) {
	t.Parallel()
	// A non-empty string that is not a UPID (e.g. a synchronous body that some
	// backends return) must be rejected, not mistaken for a task handle.
	raw := json.RawMessage(`"ok"`)
	_, err := apiclient.UPIDFromRaw(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not a UPID")
}

func TestUPIDFromRaw_NotAString_Numeric(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`42`)
	_, err := apiclient.UPIDFromRaw(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode UPID")
}

func TestUPIDFromRaw_NotAString_Object(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"upid":"value"}`)
	_, err := apiclient.UPIDFromRaw(raw)
	require.Error(t, err)
}

func TestUPIDFromRaw_MalformedJSON(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"unclosed`)
	_, err := apiclient.UPIDFromRaw(raw)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// BuildOptions
// ---------------------------------------------------------------------------

func TestBuildOptions_TokenAuth(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildOptions(
		"pve.example.com", 8006, "https",
		"root", "pam",
		"mytoken=supersecret",
		"", "", "",
		false, "",
	)

	require.Equal(t, "pve.example.com", opts.Host)
	require.Equal(t, 8006, opts.Port)
	require.Equal(t, "https", opts.Protocol)
	require.Equal(t, "root@pam!mytoken=supersecret", opts.APIToken)
	require.Empty(t, opts.Password)
	require.Empty(t, opts.Ticket)
}

func TestBuildOptions_PasswordAuth(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildOptions(
		"192.168.1.10", 8006, "https",
		"admin", "pve",
		"",
		"s3cr3t", "", "",
		false, "",
	)

	require.Equal(t, "admin@pve", opts.Username)
	require.Equal(t, "s3cr3t", opts.Password)
	require.Empty(t, opts.APIToken)
	require.Empty(t, opts.Ticket)
}

func TestBuildOptions_TicketAuth(t *testing.T) {
	t.Parallel()
	const ticketVal = "PVE:root@pam:66F0A1B2::fakeb64=="
	const csrfVal = "66F0A1B2:fakecsrf"

	opts := apiclient.BuildOptions(
		"pve.local", 0, "",
		"root", "pam",
		"",
		"",
		ticketVal,
		csrfVal,
		false, "",
	)

	require.Equal(t, ticketVal, opts.Ticket)
	require.Equal(t, csrfVal, opts.CSRFToken)
	require.Empty(t, opts.APIToken)
	require.Empty(t, opts.Password)
}

func TestBuildOptions_TokenPriorityOverPassword(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildOptions(
		"pve.local", 0, "",
		"root", "pam",
		"tok=secret",
		"pw123",
		"", "",
		false, "",
	)

	require.NotEmpty(t, opts.APIToken)
	require.Empty(t, opts.Password)
}

func TestBuildOptions_TicketPriorityOverPassword(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildOptions(
		"pve.local", 0, "",
		"root", "pam",
		"",
		"pw123",
		"PVE:root@pam:fake::",
		"",
		false, "",
	)

	require.NotEmpty(t, opts.Ticket)
	require.Empty(t, opts.APIToken)
	require.Empty(t, opts.Password)
}

func TestBuildOptions_InsecureSetsSSLNone(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildOptions(
		"pve.local", 0, "",
		"root", "pam",
		"tok=s",
		"", "", "",
		true,
		"",
	)

	require.NotNil(t, opts.SSLOptions)
	require.Equal(t, pve.SSLVerifyNone, opts.SSLOptions.VerifyMode)
	require.False(t, opts.SSLOptions.VerifyHostname)
}

func TestBuildOptions_FingerprintAddedToCache(t *testing.T) {
	t.Parallel()
	const fp = "AA:BB:CC:DD:EE:FF"

	opts := apiclient.BuildOptions(
		"pve.local", 0, "",
		"root", "pam",
		"tok=s",
		"", "", "",
		false,
		fp,
	)

	require.True(t, opts.CachedFingerprints[fp])
}

func TestBuildOptions_UsernameAlreadyQualified(t *testing.T) {
	t.Parallel()
	// Pre-qualified username — realm must NOT be appended a second time.
	opts := apiclient.BuildOptions(
		"pve.local", 0, "",
		"root@pam", "pam",
		"tok=s",
		"", "", "",
		false, "",
	)

	require.Equal(t, "root@pam!tok=s", opts.APIToken)
}

func TestBuildOptions_EmptyRealm(t *testing.T) {
	t.Parallel()
	opts := apiclient.BuildOptions(
		"pve.local", 0, "",
		"root", "",
		"tok=s",
		"", "", "",
		false, "",
	)

	require.Equal(t, "root!tok=s", opts.APIToken)
}

// ---------------------------------------------------------------------------
// NewAPIClient — constructor-only tests (no real HTTP connection required)
// ---------------------------------------------------------------------------

// newInsecureTLSTestServer returns a TLS httptest.Server that returns a
// minimal PVE-shaped JSON envelope for every request.
func newInsecureTLSTestServer(t *testing.T) (host string, port int) {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"data":null}`)
	}))
	t.Cleanup(srv.Close)

	h, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)

	p, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	return h, p
}

func TestNewAPIClient_TokenAuth_ServicesNonNil(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pam!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)
	require.NotNil(t, ac)
	require.NotNil(t, ac.Raw)
	require.NotNil(t, ac.Access)
	require.NotNil(t, ac.Cluster)
	require.NotNil(t, ac.ClusterStorage)
	require.NotNil(t, ac.Nodes)
	require.NotNil(t, ac.Pools)
	require.NotNil(t, ac.Storage)
	require.NotNil(t, ac.Tasks)
	require.NotNil(t, ac.Version)
}

func TestNewAPIClient_InvalidOptions_EmptyHost(t *testing.T) {
	t.Parallel()
	opts := pve.Options{}
	ac, err := apiclient.NewAPIClient(opts)
	require.Error(t, err)
	require.Nil(t, ac)
}

func TestNewAPIClient_MissingCredentials_Error(t *testing.T) {
	t.Parallel()
	opts := pve.Options{
		Host: "pve.example.com",
	}
	_, err := apiclient.NewAPIClient(opts)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Ticket / CSRF wiring (BUG-01)
// ---------------------------------------------------------------------------

// recordingServer is an httptest.Server that captures the headers of the last
// request it served. It replies with a minimal PVE-shaped envelope.
type recordingServer struct {
	srv     *httptest.Server
	mu      sync.Mutex
	lastHdr http.Header
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.lastHdr = r.Header.Clone()
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"data":null}`)
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *recordingServer) hostPort(t *testing.T) (string, int) {
	t.Helper()
	h, portStr, err := net.SplitHostPort(rs.srv.Listener.Addr().String())
	require.NoError(t, err)
	p, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return h, p
}

func (rs *recordingServer) header(name string) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.lastHdr.Get(name)
}

// TestNewAPIClient_TicketAuth_WriteCarriesCSRFHeader verifies that a non-GET
// request issued under ticket (session) authentication carries the
// PVECSRFPreventionToken header. Without the CSRF wiring Proxmox rejects every
// write with HTTP 401, making password/session auth effectively read-only.
// freshTicket builds a syntactically valid PVE ticket whose embedded timestamp
// is the current time, so the seeded authenticator treats it as still valid and
// does not attempt a fresh login.
func freshTicket() string {
	return fmt.Sprintf("PVE:root@pam:%X::faketicketb64==", time.Now().Unix())
}

func TestNewAPIClient_TicketAuth_WriteCarriesCSRFHeader(t *testing.T) {
	t.Parallel()
	ticketVal := freshTicket()
	const csrfVal = "66F0A1B2:fakecsrftoken"

	rs := newRecordingServer(t)
	host, port := rs.hostPort(t)

	opts := apiclient.BuildOptions(
		host, port, "http",
		"root", "pam",
		"",
		"",
		ticketVal,
		csrfVal,
		false, "",
	)
	require.Equal(t, csrfVal, opts.CSRFToken)

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)

	// Issue a write (POST) and confirm the CSRF header reached the server.
	_, err = ac.Raw.PostRawCtx(context.Background(), "/nodes/pve1/qemu/100/status/start", nil)
	require.NoError(t, err)

	require.Equal(t, csrfVal, rs.header("CSRFPreventionToken"),
		"non-GET request under ticket auth must carry the CSRF prevention token header")
}

// TestNewAPIClient_TicketAuth_NoCSRF_OmitsHeader is the negative assertion: a
// ticket-only client with no CSRF token must not fabricate one.
func TestNewAPIClient_TicketAuth_NoCSRF_OmitsHeader(t *testing.T) {
	t.Parallel()
	ticketVal := freshTicket()

	rs := newRecordingServer(t)
	host, port := rs.hostPort(t)

	opts := apiclient.BuildOptions(
		host, port, "http",
		"root", "pam",
		"",
		"",
		ticketVal,
		"", // no csrf
		false, "",
	)
	require.Empty(t, opts.CSRFToken)

	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)

	_, err = ac.Raw.PostRawCtx(context.Background(), "/nodes/pve1/qemu/100/status/start", nil)
	require.NoError(t, err)

	require.Empty(t, rs.header("CSRFPreventionToken"),
		"a ticket-only client without a CSRF token must not emit the header")
}

// ---------------------------------------------------------------------------
// SetSlogLogger (SEC-2)
// ---------------------------------------------------------------------------

func TestSetSlogLogger_NonNilInstalls(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pam!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}
	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	require.True(t, ac.SetSlogLogger(logger), "a non-nil logger should be installed")

	// Redaction config should be applied: secret-bearing headers and params are
	// registered for redaction and bodies are not logged.
	cfg := ac.Raw.GetLogConfig()
	require.True(t, cfg.Enabled)
	require.False(t, cfg.LogBody)
	require.False(t, cfg.LogResponseBody)
	require.Contains(t, cfg.RedactHeaders, "authorization")
}

func TestSetSlogLogger_NilSkips(t *testing.T) {
	t.Parallel()
	host, port := newInsecureTLSTestServer(t)

	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTPS,
		APIToken: "root@pam!test=secret",
		SSLOptions: &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		},
	}
	ac, err := apiclient.NewAPIClient(opts)
	require.NoError(t, err)

	require.False(t, ac.SetSlogLogger(nil), "a nil logger should be skipped")
}

// ---------------------------------------------------------------------------
// WaitTask delegation via mockTaskService
// ---------------------------------------------------------------------------

// mockTaskService implements tasks.Service for unit tests.
type mockTaskService struct {
	waitErr    error
	waitStatus *tasks.Status
}

func (m *mockTaskService) Wait(_ context.Context, _, _ string, _ *tasks.WaitOptions) (*tasks.Status, error) {
	return m.waitStatus, m.waitErr
}

func (m *mockTaskService) WaitForUPID(_ context.Context, _ string, _ *tasks.WaitOptions) (*tasks.Status, error) {
	return m.waitStatus, m.waitErr
}

func (m *mockTaskService) GetStatus(_ context.Context, _, _ string) (*tasks.Status, error) {
	return m.waitStatus, m.waitErr
}

func TestWaitTask_Success(t *testing.T) {
	t.Parallel()
	ac := &apiclient.APIClient{
		Tasks: &mockTaskService{
			waitStatus: &tasks.Status{Status: "stopped", ExitStatus: "OK"},
		},
	}

	err := apiclient.WaitTask(context.Background(), ac, sampleUPID, nil)
	require.NoError(t, err)
}

func TestWaitTask_TaskServiceError(t *testing.T) {
	t.Parallel()
	ac := &apiclient.APIClient{
		Tasks: &mockTaskService{
			waitErr: fmt.Errorf("task timed out"),
		},
	}

	err := apiclient.WaitTask(context.Background(), ac, sampleUPID, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait task")
	require.Contains(t, err.Error(), sampleUPID)
}

func TestWaitTask_NilStatus_Error(t *testing.T) {
	t.Parallel()
	// Defensive guard: service returns nil status with nil error.
	ac := &apiclient.APIClient{
		Tasks: &mockTaskService{
			waitStatus: nil,
			waitErr:    nil,
		},
	}

	err := apiclient.WaitTask(context.Background(), ac, sampleUPID, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil status")
}
