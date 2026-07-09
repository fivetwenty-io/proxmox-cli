package pdm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestSubscriptionKeyLs_SortsByKey asserts that `subscription key ls` sorts
// entries by key and preserves the key field in Raw output (it is the row's
// identity, not secret material to strip).
func TestSubscriptionKeyLs_SortsByKey(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/subscriptions/keys", []map[string]any{
		{"key": "pve1c-zzzzzzzzzz", "product-type": "pve", "status": "active"},
		{"key": "pbsp-aaaaaaaaaa", "product-type": "pbs", "status": "notfound"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "pbsp-aaaaaaaaaa", got[0]["key"], "entries must sort by key")
	require.Equal(t, "pve1c-zzzzzzzzzz", got[1]["key"])
	require.Contains(t, got[0], "key", "the subscription key is the row identity and must not be stripped")
}

// TestSubscriptionKeyShow_RendersKey asserts that `subscription key show`
// renders the full key entry, including the key field itself.
func TestSubscriptionKeyShow_RendersKey(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/subscriptions/keys/pve1c-zzzzzzzzzz", map[string]any{
		"key": "pve1c-zzzzzzzzzz", "product-type": "pve", "status": "active", "level": "Standard",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyShowCmd(), "show", "pve1c-zzzzzzzzzz")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "pve1c-zzzzzzzzzz", got["key"])
	require.Equal(t, "Standard", got["level"])
}

// TestSubscriptionKeyAdd_EncodesRepeatableKeyFlag asserts that `subscription
// key add` sends each --key flag as a repeated "keys" form field.
func TestSubscriptionKeyAdd_EncodesRepeatableKeyFlag(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/subscriptions/keys", &rec, map[string]any{"added": 2, "deduplicated": 0})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyAddCmd(), "add",
		"--key", "pve1c-aaaaaaaaaa", "--key", "pbsp-bbbbbbbbbb")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, []string{"pve1c-aaaaaaaaaa", "pbsp-bbbbbbbbbb"}, rec.form["keys"])
	require.Contains(t, buf.String(), "2")
}

// TestSubscriptionKeyAdd_ForwardsDigestOnlyWhenSet asserts that --digest is
// omitted from the request unless explicitly passed.
func TestSubscriptionKeyAdd_ForwardsDigestOnlyWhenSet(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/subscriptions/keys", &rec, map[string]any{"added": 1, "deduplicated": 0})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyAddCmd(), "add", "--key", "pve1c-aaaaaaaaaa")
	require.NoError(t, err)
	require.NotContains(t, rec.form, "digest")
}

// TestSubscriptionKeyDelete_RefusesWithoutConfirmation asserts the --yes/-y
// gate on `subscription key delete` blocks the request entirely when unset.
func TestSubscriptionKeyDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyDeleteCmd(), "delete", "pve1c-aaaaaaaaaa")
	require.Error(t, err)
	require.ErrorContains(t, err,
		`refusing to delete subscription key "pve1c-aaaaaaaaaa" without confirmation: pass --yes/-y`)
}

// TestSubscriptionKeyDelete_SendsRequestWithConfirmation asserts that
// passing --yes issues the delete request.
func TestSubscriptionKeyDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/subscriptions/keys/pve1c-aaaaaaaaaa", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyDeleteCmd(), "delete", "pve1c-aaaaaaaaaa", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Subscription key "pve1c-aaaaaaaaaa" deleted.`)
}

// TestSubscriptionKeyAssign_SendsRemoteAndNode asserts that `subscription
// key assign` encodes --remote and --node onto the assignment request.
func TestSubscriptionKeyAssign_SendsRemoteAndNode(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/subscriptions/keys/pve1c-aaaaaaaaaa/assignment", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyAssignCmd(), "assign", "pve1c-aaaaaaaaaa",
		"--remote", "alpha", "--node", "pve-node-1")
	require.NoError(t, err)

	require.Equal(t, "alpha", rec.form.Get("remote"))
	require.Equal(t, "pve-node-1", rec.form.Get("node"))
	require.Contains(t, buf.String(), `Subscription key "pve1c-aaaaaaaaaa" assigned to remote "alpha" node "pve-node-1".`)
}

// TestSubscriptionKeyUnassign_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `subscription key unassign` blocks the request.
func TestSubscriptionKeyUnassign_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyUnassignCmd(), "unassign", "pve1c-aaaaaaaaaa")
	require.Error(t, err)
	require.ErrorContains(t, err,
		`refusing to unassign subscription key "pve1c-aaaaaaaaaa" without confirmation: pass --yes/-y`)
}

// TestSubscriptionKeyUnassign_SendsRequestWithConfirmation asserts that
// passing --yes issues the unassign request.
func TestSubscriptionKeyUnassign_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/subscriptions/keys/pve1c-aaaaaaaaaa/assignment", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionKeyUnassignCmd(), "unassign", "pve1c-aaaaaaaaaa", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Subscription key "pve1c-aaaaaaaaaa" unassigned.`)
}

// TestSubscriptionNodeStatus_SortsByRemoteThenNode asserts that
// `subscription node-status` sorts entries by remote then node and renders
// the expected table columns.
func TestSubscriptionNodeStatus_SortsByRemoteThenNode(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/subscriptions/node-status", []map[string]any{
		{"remote": "zeta", "node": "n1", "type": "pve", "status": "active", "level": "Standard", "pending-clear": false},
		{"remote": "alpha", "node": "n2", "type": "pbs", "status": "notfound", "level": "None", "pending-clear": true},
		{"remote": "alpha", "node": "n1", "type": "pve", "status": "active", "level": "Basic", "pending-clear": false},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionNodeStatusCmd(), "node-status")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 3)
	require.Equal(t, "alpha", got[0]["remote"])
	require.Equal(t, "n1", got[0]["node"])
	require.Equal(t, "alpha", got[1]["remote"])
	require.Equal(t, "n2", got[1]["node"])
	require.Equal(t, "zeta", got[2]["remote"])
}

// TestSubscriptionNodeStatus_ForwardsMaxAge asserts that --max-age is only
// sent when explicitly set.
func TestSubscriptionNodeStatus_ForwardsMaxAge(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/subscriptions/node-status", &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionNodeStatusCmd(), "node-status", "--max-age", "0")
	require.NoError(t, err)
	require.Equal(t, "0", rec.query.Get("max-age"))
}

// TestSubscriptionCheck_SendsRemoteAndNode asserts that `subscription check`
// requires and encodes --remote and --node.
func TestSubscriptionCheck_SendsRemoteAndNode(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/subscriptions/check", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionCheckCmd(), "check", "--remote", "alpha", "--node", "n1")
	require.NoError(t, err)

	require.Equal(t, "alpha", rec.form.Get("remote"))
	require.Equal(t, "n1", rec.form.Get("node"))
	require.Contains(t, buf.String(), `Subscription check triggered for remote "alpha" node "n1".`)
}

// TestSubscriptionAdoptKey_SendsRemoteAndNode asserts that `subscription
// adopt-key` encodes --remote and --node.
func TestSubscriptionAdoptKey_SendsRemoteAndNode(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/subscriptions/adopt-key", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionAdoptKeyCmd(), "adopt-key", "--remote", "alpha", "--node", "n1")
	require.NoError(t, err)

	require.Equal(t, "alpha", rec.form.Get("remote"))
	require.Equal(t, "n1", rec.form.Get("node"))
}

// TestSubscriptionAdoptAll_RefusesWithoutConfirmation asserts the --yes/-y
// gate on `subscription adopt-all` blocks the request.
func TestSubscriptionAdoptAll_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionAdoptAllCmd(), "adopt-all")
	require.Error(t, err)
	require.ErrorContains(t, err, "refusing to adopt every foreign live subscription without confirmation: pass --yes/-y")
}

// TestSubscriptionAdoptAll_SortsAndRendersEntries asserts that `subscription
// adopt-all` sorts the adopted entries by remote then node.
func TestSubscriptionAdoptAll_SortsAndRendersEntries(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("POST /api2/json/subscriptions/adopt-all", []map[string]any{
		{"remote": "zeta", "node": "n1", "key": "pve1c-zzzzzzzzzz"},
		{"remote": "alpha", "node": "n1", "key": "pve1c-aaaaaaaaaa"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionAdoptAllCmd(), "adopt-all", "--yes")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["remote"])
	require.Equal(t, "zeta", got[1]["remote"])
}

// TestSubscriptionAutoAssign_RendersAssignmentsAndDigests asserts that
// `subscription auto-assign` renders the assignment rows and preserves the
// keys-digest / node-status-digest snapshots in Raw output for later
// bulk-assign chaining.
func TestSubscriptionAutoAssign_RendersAssignmentsAndDigests(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("POST /api2/json/subscriptions/auto-assign", map[string]any{
		"assignments":        []map[string]any{{"key": "pve1c-aaaaaaaaaa", "node": "n1", "remote": "alpha"}},
		"keys-digest":        "a1b2c3",
		"node-status-digest": "d4e5f6",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionAutoAssignCmd(), "auto-assign")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "a1b2c3", got["keys-digest"])
	require.Equal(t, "d4e5f6", got["node-status-digest"])
}

// TestSubscriptionBulkAssign_RefusesWithoutConfirmation asserts the --yes/-y
// gate on `subscription bulk-assign` blocks the request.
func TestSubscriptionBulkAssign_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionBulkAssignCmd(), "bulk-assign", "--proposal", `{"assignments":[]}`)
	require.Error(t, err)
	require.ErrorContains(t, err, "refusing to bulk-assign subscriptions without confirmation: pass --yes/-y")
}

// TestSubscriptionBulkAssign_SendsProposalAsPreEncodedJSONString asserts
// that `subscription bulk-assign` bypasses the generated CreateBulkAssign
// binding and sends the whole proposal as one pre-encoded JSON-text form
// value (the array-of-object / nested-object body-encoder workaround), and
// that the server's response entries are decoded and rendered.
func TestSubscriptionBulkAssign_SendsProposalAsPreEncodedJSONString(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	proposal := `{"assignments":[{"key":"pve1c-aaaaaaaaaa","node":"n1","remote":"alpha"}],` +
		`"keys-digest":"a1b2c3","node-status-digest":"d4e5f6"}`

	var rec recordedRequest
	f.HandleFunc("POST /api2/json/subscriptions/bulk-assign", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.form = r.PostForm
		testhelper.WriteData(w, []map[string]any{
			{"key": "pve1c-aaaaaaaaaa", "node": "n1", "remote": "alpha"},
		})
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionBulkAssignCmd(), "bulk-assign", "--yes", "--proposal", proposal)
	require.NoError(t, err)

	// The proposal must reach the wire as the exact JSON text, not a
	// Proxmox comma-separated option-string.
	require.Equal(t, proposal, rec.form.Get("proposal"))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(rec.form.Get("proposal")), &decoded))

	require.Contains(t, buf.String(), "pve1c-aaaaaaaaaa")
}

// TestSubscriptionBulkAssign_RejectsInvalidProposalJSON asserts that
// `subscription bulk-assign` validates --proposal before issuing any request.
func TestSubscriptionBulkAssign_RejectsInvalidProposalJSON(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionBulkAssignCmd(), "bulk-assign", "--yes", "--proposal", "not-json")
	require.Error(t, err)
	require.ErrorContains(t, err, "--proposal is not valid JSON")
}

// TestSubscriptionApplyPending_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `subscription apply-pending` blocks the request.
func TestSubscriptionApplyPending_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionApplyPendingCmd(), "apply-pending")
	require.Error(t, err)
	require.ErrorContains(t, err, "refusing to apply pending subscription changes without confirmation: pass --yes/-y")
}

// TestSubscriptionApplyPending_NullResponseReportsNoPending asserts that a
// null response (nothing pending) is reported as a plain success message
// rather than treated as a UPID parse failure.
func TestSubscriptionApplyPending_NullResponseReportsNoPending(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("POST /api2/json/subscriptions/apply-pending", nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionApplyPendingCmd(), "apply-pending", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "No pending subscription changes to apply.")
}

// TestSubscriptionApplyPending_UPIDResponseAwaitsTask asserts that a UPID
// response is awaited via finishAsync before reporting success.
func TestSubscriptionApplyPending_UPIDResponseAwaitsTask(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("POST /api2/json/subscriptions/apply-pending", validUPID)
	handleTaskStatus(f, validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionApplyPendingCmd(), "apply-pending", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "Pending subscription changes applied.")
}

// TestSubscriptionApplyPending_Async asserts that with --async the command
// prints the UPID immediately without waiting for the task.
func TestSubscriptionApplyPending_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, true)

	f.HandleJSON("POST /api2/json/subscriptions/apply-pending", validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionApplyPendingCmd(), "apply-pending", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

// TestSubscriptionClearPending_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `subscription clear-pending` blocks the request.
func TestSubscriptionClearPending_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionClearPendingCmd(), "clear-pending")
	require.Error(t, err)
	require.ErrorContains(t, err, "refusing to clear pending subscription changes without confirmation: pass --yes/-y")
}

// TestSubscriptionClearPending_RendersClearedCount asserts that
// `subscription clear-pending` renders the cleared count from the response.
func TestSubscriptionClearPending_RendersClearedCount(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("POST /api2/json/subscriptions/clear-pending", map[string]any{"cleared": 3})

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionClearPendingCmd(), "clear-pending", "--yes")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.InDelta(t, float64(3), got["cleared"], 0)
}

// TestSubscriptionQueueClear_RefusesWithoutConfirmation asserts the
// --yes/-y gate on `subscription queue-clear` blocks the request.
func TestSubscriptionQueueClear_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionQueueClearCmd(), "queue-clear", "--remote", "alpha", "--node", "n1")
	require.Error(t, err)
	require.ErrorContains(t, err,
		`refusing to queue a clear for remote "alpha" node "n1" without confirmation: pass --yes/-y`)
}

// TestSubscriptionQueueClear_SendsRequestWithConfirmation asserts that
// passing --yes issues the queue-clear request with the expected fields.
func TestSubscriptionQueueClear_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/subscriptions/queue-clear", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionQueueClearCmd(), "queue-clear",
		"--remote", "alpha", "--node", "n1", "--yes")
	require.NoError(t, err)

	require.Equal(t, "alpha", rec.form.Get("remote"))
	require.Equal(t, "n1", rec.form.Get("node"))
	require.Contains(t, buf.String(), `Clear queued for remote "alpha" node "n1".`)
}

// TestSubscriptionRevertPendingClear_SendsRemoteAndNode asserts that
// `subscription revert-pending-clear` is not gated and encodes --remote and
// --node onto the request.
func TestSubscriptionRevertPendingClear_SendsRemoteAndNode(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/subscriptions/revert-pending-clear", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newSubscriptionRevertPendingClearCmd(), "revert-pending-clear",
		"--remote", "alpha", "--node", "n1")
	require.NoError(t, err)

	require.Equal(t, "alpha", rec.form.Get("remote"))
	require.Equal(t, "n1", rec.form.Get("node"))
	require.Contains(t, buf.String(), `Pending clear reverted for remote "alpha" node "n1".`)
}
