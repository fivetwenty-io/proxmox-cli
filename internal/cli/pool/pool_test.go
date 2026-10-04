package pool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// recordedRequest captures a single HTTP request a command issued to the fake
// PVE server so tests can assert on method, path, and decoded body.
type recordedRequest struct {
	method string
	path   string
	query  string
	body   map[string]any
}

// writeConfig writes a token-auth config pointing the named target at the fake
// PVE server (HTTP, TLS verification off) and returns the config file path.
func writeConfig(t *testing.T, f *testhelper.FakePVE) string {
	t.Helper()

	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err, "split fake server host:port")

	cfg := fmt.Sprintf(`current-context: fake
contexts:
  fake:
    host: %s
    port: %s
    protocol: http
    realm: pam
    tls:
      insecure: true
    auth:
      type: token
      username: root
      token-id: test
      secret: 00000000-0000-0000-0000-000000000000
`, host, portStr)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

// run drives the real root command (so PersistentPreRunE wires live Deps) with
// the pool group attached, executes it with the given args, and returns stdout.
func run(t *testing.T, f *testhelper.FakePVE, in string, args ...string) (string, error) {
	t.Helper()

	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "")
	t.Setenv("PMX_CONTEXT", "")

	cfgPath := writeConfig(t, f)

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(Group(&cli.Deps{}))

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if in != "" {
		root.SetIn(strings.NewReader(in))
	}

	full := append([]string{"--config", cfgPath, "--output", "table", "pool"}, args...)
	root.SetArgs(full)

	err := root.Execute()
	return buf.String(), err
}

// runSplit drives the root command with separate stdout and stderr buffers so
// tests can assert that interactive prompts are written to stderr and never
// contaminate machine-readable stdout.
func runSplit(t *testing.T, f *testhelper.FakePVE, in, format string, args ...string) (
	stdout, stderr string, err error,
) {
	t.Helper()

	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "")
	t.Setenv("PMX_CONTEXT", "")

	cfgPath := writeConfig(t, f)

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(Group(&cli.Deps{}))

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	if in != "" {
		root.SetIn(strings.NewReader(in))
	}

	full := append([]string{"--config", cfgPath, "--output", format, "pool"}, args...)
	root.SetArgs(full)

	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func mustNewClientAndRecord(f *testhelper.FakePVE, rec *[]recordedRequest, pattern string, payload any, status int) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		// PVE write requests are form-urlencoded; capture each field as a string,
		// falling back to JSON for any non-form bodies.
		if err := r.ParseForm(); err == nil {
			for k, v := range r.PostForm {
				if len(v) > 0 {
					body[k] = v[0]
				}
			}
		}
		if len(body) == 0 {
			if b, _ := io.ReadAll(r.Body); len(b) > 0 {
				_ = json.Unmarshal(b, &body)
			}
		}
		*rec = append(*rec, recordedRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: body})
		if status >= 400 {
			testhelper.WriteError(w, status, "boom")
			return
		}
		testhelper.WriteData(w, payload)
	})
}

func TestPoolList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/pools", []any{
		map[string]any{"poolid": "prod", "comment": "production", "members": []any{
			map[string]any{"id": "qemu/100"},
			map[string]any{"id": "qemu/101"},
		}},
		map[string]any{"poolid": "dev", "comment": "", "members": []any{}},
	}, 200)

	out, err := run(t, f, "", "list")
	require.NoError(t, err)
	require.Contains(t, out, "prod")
	require.Contains(t, out, "production")
	require.Contains(t, out, "dev")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/pools", rec[0].path)
}

func TestPoolListError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/pools", nil, 500)

	_, err := run(t, f, "", "list")
	require.Error(t, err)
}

// handlePoolsLikePVE serves GET /pools the way PVE 9 does. Without a poolid the
// reply lists poolid and comment only, with no members. With a poolid it adds
// that pool's members, limited to the type filter when one is passed, and a
// type without a poolid is refused. Every request is recorded.
func handlePoolsLikePVE(f *testhelper.FakePVE, rec *[]recordedRequest, members map[string][]map[string]any) {
	f.HandleFunc("GET /api2/json/pools", func(w http.ResponseWriter, r *http.Request) {
		*rec = append(*rec, recordedRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery})
		q := r.URL.Query()
		poolid, typ := q.Get("poolid"), q.Get("type")
		if typ != "" && poolid == "" {
			testhelper.WriteError(w, http.StatusBadRequest, "poolid: missing property required by 'type'")
			return
		}
		ids := make([]string, 0, len(members))
		for id := range members {
			if poolid == "" || poolid == id {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		out := make([]any, 0, len(ids))
		for _, id := range ids {
			entry := map[string]any{"poolid": id, "comment": "pool " + id}
			if poolid != "" {
				kept := make([]any, 0)
				for _, m := range members[id] {
					if typ == "" || m["type"] == typ {
						kept = append(kept, m)
					}
				}
				entry["members"] = kept
			}
			out = append(out, entry)
		}
		testhelper.WriteData(w, out)
	})
}

// TestPoolList_CountsMembersLikePVE is the regression case for every pool
// showing 0 members. PVE leaves members out of the bare list, so the command
// must read each pool by its poolid to count them.
func TestPoolList_CountsMembersLikePVE(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	handlePoolsLikePVE(f, &rec, map[string][]map[string]any{
		"bosh-templates": {
			{"id": "qemu/30531", "type": "qemu"},
			{"id": "qemu/30709", "type": "qemu"},
			{"id": "qemu/30772", "type": "qemu"},
		},
		"cpitest-parker": {{"id": "qemu/90548", "type": "qemu"}, {"id": "qemu/90821", "type": "qemu"}},
		"empty":          {},
	})

	out, err := run(t, f, "", "list")
	require.NoError(t, err)
	require.Regexp(t, `bosh-templates\s+\S.*\s3\s`, out)
	require.Regexp(t, `cpitest-parker\s+\S.*\s2\s`, out)
	require.Regexp(t, `empty\s+\S.*\s0\s`, out)
	require.Len(t, rec, 4, "one bare list plus one read per pool")
	require.Empty(t, rec[0].query, "the first request is the bare list")
}

// TestPoolList_JSONCarriesMembers verifies -o json reports the member entries,
// not null, for a pool whose members come from the per-pool read.
func TestPoolList_JSONCarriesMembers(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	handlePoolsLikePVE(f, &rec, map[string][]map[string]any{
		"prod": {{"id": "qemu/100", "type": "qemu"}},
	})

	stdout, _, err := runSplit(t, f, "", "json", "list")
	require.NoError(t, err)
	var got []struct {
		Poolid  string           `json:"poolid"`
		Members []map[string]any `json:"members"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	require.Equal(t, "prod", got[0].Poolid)
	require.Len(t, got[0].Members, 1)
	require.Equal(t, "qemu/100", got[0].Members[0]["id"])
}

// TestPoolList_TypeFilterWithoutPoolid is the regression case for `list --type`
// failing on PVE 9, which refuses a type without a poolid. The command must
// filter per pool and leave out pools holding no member of that type.
func TestPoolList_TypeFilterWithoutPoolid(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	handlePoolsLikePVE(f, &rec, map[string][]map[string]any{
		"vms":     {{"id": "qemu/100", "type": "qemu"}, {"id": "lxc/200", "type": "lxc"}},
		"cts":     {{"id": "lxc/201", "type": "lxc"}},
		"storage": {{"id": "storage/local", "type": "storage"}},
	})

	out, err := run(t, f, "", "list", "--type", "qemu")
	require.NoError(t, err)
	require.Contains(t, out, "vms")
	require.NotContains(t, out, "cts")
	require.NotContains(t, out, "storage")
	require.Regexp(t, `vms\s+\S.*\s1\s`, out, "only the qemu member is counted")
	require.Len(t, rec, 4, "one bare list plus one typed read per pool")
	require.Empty(t, rec[0].query, "the bare list must not carry type")
	for _, r := range rec[1:] {
		require.Contains(t, r.query, "type=qemu")
		require.Contains(t, r.query, "poolid=")
	}
}

// TestPoolList_PoolidSingleRequest verifies --poolid is answered by one request,
// since the poolid read already reports the members.
func TestPoolList_PoolidSingleRequest(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	handlePoolsLikePVE(f, &rec, map[string][]map[string]any{
		"prod": {{"id": "qemu/100", "type": "qemu"}, {"id": "qemu/101", "type": "qemu"}},
		"dev":  {},
	})

	out, err := run(t, f, "", "list", "--poolid", "prod")
	require.NoError(t, err)
	require.Regexp(t, `prod\s+\S.*\s2\s`, out)
	require.NotContains(t, out, "dev")
	require.Len(t, rec, 1)
}

// TestPoolList_MemberReadError verifies a failure on the per-pool read is
// surfaced rather than printing a wrong count.
func TestPoolList_MemberReadError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/pools", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("poolid") != "" {
			testhelper.WriteError(w, http.StatusInternalServerError, "boom")
			return
		}
		testhelper.WriteData(w, []any{map[string]any{"poolid": "prod", "comment": "production"}})
	})

	_, err := run(t, f, "", "list")
	require.Error(t, err)
	require.ErrorContains(t, err, `members of pool "prod"`)
}

// TestPoolGet verifies pool get uses GET /pools?poolid=<id> (non-deprecated endpoint).
func TestPoolGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	// Response is a list (GET /pools) filtered by poolid, not the single-object
	// shape returned by the deprecated GET /pools/{poolid}.
	mustNewClientAndRecord(f, &rec, "GET /api2/json/pools", []any{
		map[string]any{
			"poolid":  "prod",
			"comment": "production",
			"members": []any{
				map[string]any{"id": "qemu/100", "type": "qemu", "vmid": 100},
			},
		},
	}, 200)

	out, err := run(t, f, "", "get", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "prod")
	require.Contains(t, out, "production")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/pools", rec[0].path)
	require.Contains(t, rec[0].query, "poolid=prod")
}

// TestPoolGetNotFound verifies pool get returns an error when the filtered list
// is empty (pool does not exist but API returns 200 with zero elements).
func TestPoolGetNotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/pools", []any{}, 200)

	_, err := run(t, f, "", "get", "ghost")
	require.Error(t, err)
	require.ErrorContains(t, err, "not found")
	require.Len(t, rec, 1)
	require.Contains(t, rec[0].query, "poolid=ghost")
}

func TestPoolGetError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/pools", nil, 404)

	_, err := run(t, f, "", "get", "missing")
	require.Error(t, err)
}

func TestPoolCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "POST /api2/json/pools", map[string]any{}, 200)

	out, err := run(t, f, "", "create", "--poolid", "staging", "--comment", "staging pool")
	require.NoError(t, err)
	require.Contains(t, out, "staging")
	require.Contains(t, out, "created")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPost, rec[0].method)
	require.Equal(t, "/api2/json/pools", rec[0].path)
	require.Equal(t, "staging", rec[0].body["poolid"])
	require.Equal(t, "staging pool", rec[0].body["comment"])
}

func TestPoolCreateMissingPoolid(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "", "create")
	require.Error(t, err)
}

// TestPoolSet verifies pool set uses PUT /pools (non-deprecated endpoint).
// poolid is transmitted in the request body, not the URL path.
func TestPoolSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/pools", map[string]any{}, 200)

	out, err := run(t, f, "", "set", "prod", "--comment", "updated", "--vms", "100,101", "--storage", "local")
	require.NoError(t, err)
	require.Contains(t, out, "prod")
	require.Contains(t, out, "updated")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "/api2/json/pools", rec[0].path)
	require.Equal(t, "prod", rec[0].body["poolid"])
	require.Equal(t, "updated", rec[0].body["comment"])
	require.Equal(t, "100,101", rec[0].body["vms"])
	require.Equal(t, "local", rec[0].body["storage"])
}

// TestPoolSetDelete verifies --delete flag reaches the PUT /pools body.
func TestPoolSetDelete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "set", "prod", "--vms", "101", "--delete")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].body["delete"])
	require.Equal(t, "101", rec[0].body["vms"])
}

// TestPoolSetAllowMove verifies --allow-move flag reaches the PUT /pools body.
func TestPoolSetAllowMove(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/pools", map[string]any{}, 200)

	_, err := run(t, f, "", "set", "prod", "--vms", "100", "--allow-move")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "100", rec[0].body["vms"])
	require.Equal(t, "1", rec[0].body["allow-move"])
}

func TestPoolListPoolidFilter(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "GET /api2/json/pools", []any{
		map[string]any{"poolid": "prod", "comment": "production", "members": []any{}},
	}, 200)

	out, err := run(t, f, "", "list", "--poolid", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "prod")
	require.Len(t, rec, 1)
	require.Contains(t, rec[0].query, "poolid=prod")
}

func TestPoolSetError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "PUT /api2/json/pools", nil, 500)

	_, err := run(t, f, "", "set", "prod", "--comment", "x")
	require.Error(t, err)
}

// TestPoolDeleteWithYes verifies pool delete uses DELETE /pools (non-deprecated endpoint).
// poolid is transmitted in the request body, not the URL path.
func TestPoolDeleteWithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "DELETE /api2/json/pools", map[string]any{}, 200)

	out, err := run(t, f, "", "delete", "prod", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "prod")
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
	require.Equal(t, "/api2/json/pools", rec[0].path)
}

func TestPoolDeleteConfirmYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "DELETE /api2/json/pools", map[string]any{}, 200)

	out, err := run(t, f, "y\n", "delete", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "deleted")
	require.Len(t, rec, 1)
}

func TestPoolDeleteConfirmAbort(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "DELETE /api2/json/pools", map[string]any{}, 200)

	out, err := run(t, f, "n\n", "delete", "prod")
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(out), "aborted")
	require.Empty(t, rec, "no request must be issued when confirmation is declined")
}

func TestPoolDeleteError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "DELETE /api2/json/pools", nil, 500)

	_, err := run(t, f, "", "delete", "prod", "--yes")
	require.Error(t, err)
	// The failure must come from the API DELETE call, not earlier validation.
	require.ErrorContains(t, err, "delete pool")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodDelete, rec[0].method)
}

// TestPoolDeleteConfirmPromptToStderr verifies the y/N confirmation prompt is
// written to stderr, leaving stdout free of prompt text so piped/JSON output is
// not corrupted.
func TestPoolDeleteConfirmPromptToStderr(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	mustNewClientAndRecord(f, &rec, "DELETE /api2/json/pools", map[string]any{}, 200)

	stdout, stderr, err := runSplit(t, f, "y\n", "json", "delete", "prod")
	require.NoError(t, err)
	require.NotContains(t, stdout, "[y/N]", "prompt must not appear on stdout")
	require.Contains(t, stderr, "[y/N]", "prompt must be written to stderr")
	require.Len(t, rec, 1)
}

// TestPoolDeleteRejectsDestroyFlags verifies the documented --destroy-vms /
// --destroy-storage flags are rejected with a clear message because the client
// library's delete method cannot express member destruction.
func TestPoolDeleteRejectsDestroyFlags(t *testing.T) {
	for _, flag := range []string{"--destroy-vms", "--destroy-storage"} {
		t.Run(flag, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec []recordedRequest
			mustNewClientAndRecord(f, &rec, "DELETE /api2/json/pools", map[string]any{}, 200)

			_, err := run(t, f, "", "delete", "prod", "--yes", flag)
			require.Error(t, err)
			require.ErrorContains(t, err, "not supported")
			require.Empty(t, rec, "no delete request must be issued when an unsupported flag is set")
		})
	}
}

// ensure the package self-registers a factory of the right shape.
var _ func(*cli.Deps) *cobra.Command = Group
