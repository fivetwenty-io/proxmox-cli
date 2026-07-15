package pbs

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestPBS_AuditCommandTree verifies every documented pbs sub-command and verb
// is registered on the group.
func TestPBS_AuditCommandTree(t *testing.T) {
	groups := map[string][]string{
		"datastore": {"ls", "show", "create", "update", "delete", "status", "usage", "rrd"},
		"snapshot":  {"ls", "show", "files", "delete", "protect", "unprotect", "notes"},
		"group":     {"ls", "delete", "notes"},
		"prune":     {"run", "simulate", "job"},
		"gc":        {"run", "status", "ls"},
		"verify":    {"run", "job"},
		"sync":      {"ls", "job", "pull", "push"},
		"remote":    {"ls", "show", "add", "update", "delete", "scan"},
		"traffic":   {"ls", "show", "add", "update", "delete", "current"},
		"status":    {"datastore-usage"},
		"node": {
			"ls", "status", "reboot", "shutdown", "rrd", "report", "syslog", "journal",
			"dns", "time", "config", "subscription", "identity",
			"tasks", "services", "apt", "disks", "network", "certificates",
		},
		"user":           {"ls", "show", "add", "update", "delete", "unlock-tfa", "passwd", "token"},
		"acl":            {"ls", "update"},
		"role":           {"ls"},
		"permission":     {"ls"},
		"realm":          {"ls", "sync", "ad", "ldap", "openid", "pam", "pbs"},
		"metrics":        {"influxdb-http", "influxdb-udp", "data"},
		"notification":   {"endpoint", "matcher", "target"},
		"acme":           {"account", "plugin", "challenge-schema", "directories", "tos"},
		"tape":           {"drive", "changer", "media", "pool", "key", "job", "backup", "restore"},
		"encryption-key": {"ls", "add", "delete", "toggle-archive"},
	}
	nested := map[string][]string{
		"remote scan":                    {"ls", "groups", "namespaces"},
		"node tasks":                     {"ls", "show", "log", "delete"},
		"node services":                  {"ls", "show", "state", "start", "stop", "restart", "reload"},
		"node dns":                       {"show", "update"},
		"node time":                      {"show", "update"},
		"node config":                    {"show", "update"},
		"node subscription":              {"show", "set", "update", "delete"},
		"node apt":                       {"ls", "update", "repositories", "repo-add", "repo-update", "versions", "changelog"},
		"node disks":                     {"ls", "smart", "initgpt", "wipe", "directory", "zfs"},
		"node disks directory":           {"ls", "create", "delete"},
		"node disks zfs":                 {"ls", "show", "create"},
		"node network":                   {"ls", "show", "create", "update", "delete", "revert", "apply"},
		"node certificates":              {"info", "acme", "custom"},
		"node certificates acme":         {"order", "renew"},
		"node certificates custom":       {"upload", "delete"},
		"user token":                     {"ls", "show", "add", "update", "delete"},
		"realm ad":                       {"ls", "show", "add", "update", "delete"},
		"realm ldap":                     {"ls", "show", "add", "update", "delete"},
		"realm openid":                   {"ls", "show", "add", "update", "delete"},
		"realm pam":                      {"show", "update"},
		"realm pbs":                      {"show", "update"},
		"metrics influxdb-http":          {"ls", "show", "add", "update", "delete"},
		"metrics influxdb-udp":           {"ls", "show", "add", "update", "delete"},
		"notification endpoint":          {"gotify", "sendmail", "smtp", "webhook"},
		"notification endpoint gotify":   {"ls", "show", "add", "update", "delete"},
		"notification endpoint sendmail": {"ls", "show", "add", "update", "delete"},
		"notification endpoint smtp":     {"ls", "show", "add", "update", "delete"},
		"notification endpoint webhook":  {"ls", "show", "add", "update", "delete"},
		"notification matcher":           {"ls", "show", "add", "update", "delete", "fields", "field-values"},
		"notification target":            {"ls", "test"},
		"acme account":                   {"ls", "show", "add", "update", "delete"},
		"acme plugin":                    {"ls", "show", "add", "update", "delete"},
		"acme challenge-schema":          {"ls"},
		"acme directories":               {"ls"},
		"acme tos":                       {"show"},
		"tape drive": {
			"ls", "show", "add", "update", "delete", "scan", "status",
			"cartridge-memory", "volume-statistics", "read-label", "inventory",
			"load-media", "load-slot", "unload", "eject", "rewind", "clean",
			"format", "label", "barcode-label", "catalog", "export",
			"update-inventory", "restore-key",
		},
		"tape changer": {"ls", "show", "add", "update", "delete", "scan", "status", "transfer"},
		"tape media":   {"ls", "content", "sets", "move", "destroy", "set-status"},
		"tape pool":    {"ls", "show", "add", "update", "delete"},
		"tape key":     {"ls", "show", "add", "update", "delete"},
		"tape job":     {"ls", "show", "add", "update", "delete", "run", "status"},
	}
	jobVerbs := []string{"ls", "show", "add", "update", "delete", "run"}

	root := Group(&cli.Deps{})
	byName := make(map[string]bool)

	for _, c := range root.Commands() {
		byName[c.Name()] = true
	}

	for group, verbs := range groups {
		require.True(t, byName[group], "expected pbs sub-command %q to be registered", group)

		sub := findSubcommand(t, root, group)
		verbNames := make(map[string]bool)

		for _, c := range sub.Commands() {
			verbNames[c.Name()] = true
		}

		for _, verb := range verbs {
			require.True(t, verbNames[verb], "expected pbs %s verb %q to be registered", group, verb)
		}
	}

	for _, group := range []string{"prune", "verify", "sync"} {
		job := findSubcommand(t, findSubcommand(t, root, group), "job")
		verbNames := make(map[string]bool)

		for _, c := range job.Commands() {
			verbNames[c.Name()] = true
		}

		for _, verb := range jobVerbs {
			require.True(t, verbNames[verb], "expected pbs %s job verb %q to be registered", group, verb)
		}
	}

	for path, verbs := range nested {
		sub := root
		for _, name := range strings.Fields(path) {
			sub = findSubcommand(t, sub, name)
		}

		verbNames := make(map[string]bool)

		for _, c := range sub.Commands() {
			verbNames[c.Name()] = true
		}

		for _, verb := range verbs {
			require.True(t, verbNames[verb], "expected pbs %s verb %q to be registered", path, verb)
		}
	}
}

// findSubcommand returns parent's direct child named name, failing the test if
// it is missing.
func findSubcommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()

	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}

	t.Fatalf("sub-command %q not found under %q", name, parent.Name())

	return nil
}

// TestDatastoreCreate_AuditAllFlags verifies every `pbs datastore create` flag
// reaches the POST /config/datastore body together in one request, using the
// exact API-side keys from CreateDatastoreParams. Individual flags are also
// exercised in datastore_test.go; this proves they compose without clobbering
// each other.
func TestDatastoreCreate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)

	var rec recordedRequest

	recordJSON(f, "POST "+pathConfigDatastore, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer

	err := run(deps, &buf, newDatastoreCmd(), "datastore", "create", "audit-store",
		"--path", "/mnt/audit-store",
		"--comment", "audit comment",
		"--counter-reset-schedule", "monthly",
		"--gc-on-unmount",
		"--gc-schedule", "daily",
		"--keep-last", "5",
		"--keep-hourly", "24",
		"--keep-daily", "7",
		"--keep-weekly", "4",
		"--keep-monthly", "12",
		"--keep-yearly", "2",
		"--maintenance-mode", "read-only",
		"--notification-mode", "notification-system",
		"--notification-thresholds", "warning=80",
		"--notify", "gc=always",
		"--notify-user", "admin@pbs",
		"--prune-schedule", "weekly",
		"--tuning", "chunk-order=inode",
		"--verify-new",
		"--backend", "type=s3,client=minio,bucket=backups",
		"--backing-device", "9c6182d1-8e0e-4b47-9cb0-3b7ca4bfe11d",
		"--overwrite-in-use",
		"--reuse-datastore",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "audit-store", rec.form.Get("name"))
	require.Equal(t, "/mnt/audit-store", rec.form.Get("path"))

	want := map[string]string{
		"comment":                 "audit comment",
		"counter-reset-schedule":  "monthly",
		"gc-on-unmount":           "1",
		"gc-schedule":             "daily",
		"keep-last":               "5",
		"keep-hourly":             "24",
		"keep-daily":              "7",
		"keep-weekly":             "4",
		"keep-monthly":            "12",
		"keep-yearly":             "2",
		"maintenance-mode":        "read-only",
		"notification-mode":       "notification-system",
		"notification-thresholds": "warning=80",
		"notify":                  "gc=always",
		"notify-user":             "admin@pbs",
		"prune-schedule":          "weekly",
		"tuning":                  "chunk-order=inode",
		"verify-new":              "1",
		"backend":                 "type=s3,client=minio,bucket=backups",
		"backing-device":          "9c6182d1-8e0e-4b47-9cb0-3b7ca4bfe11d",
		"overwrite-in-use":        "1",
		"reuse-datastore":         "1",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// TestDatastoreUpdate_AuditAllFlags verifies every `pbs datastore update` flag
// reaches the PUT /config/datastore/{name} body together, including the
// repeatable --delete flag as repeated form entries and the update-only
// --digest guard.
func TestDatastoreUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)

	var rec recordedRequest

	recordJSON(f, "PUT "+fmt.Sprintf(pathConfigDatastoreFmt, "audit-store"), &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer

	err := run(deps, &buf, newDatastoreCmd(), "datastore", "update", "audit-store",
		"--comment", "updated comment",
		"--counter-reset-schedule", "monthly",
		"--gc-on-unmount",
		"--gc-schedule", "daily",
		"--keep-last", "5",
		"--keep-hourly", "24",
		"--keep-daily", "7",
		"--keep-weekly", "4",
		"--keep-monthly", "12",
		"--keep-yearly", "2",
		"--maintenance-mode", "offline",
		"--notification-mode", "legacy-sendmail",
		"--notification-thresholds", "warning=90",
		"--notify", "verify=error",
		"--notify-user", "ops@pbs",
		"--prune-schedule", "weekly",
		"--tuning", "sync-level=file",
		"--verify-new",
		"--delete", "backend",
		"--delete", "backing-device",
		"--digest", "abc123def456",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)

	want := map[string]string{
		"comment":                 "updated comment",
		"counter-reset-schedule":  "monthly",
		"gc-on-unmount":           "1",
		"gc-schedule":             "daily",
		"keep-last":               "5",
		"keep-hourly":             "24",
		"keep-daily":              "7",
		"keep-weekly":             "4",
		"keep-monthly":            "12",
		"keep-yearly":             "2",
		"maintenance-mode":        "offline",
		"notification-mode":       "legacy-sendmail",
		"notification-thresholds": "warning=90",
		"notify":                  "verify=error",
		"notify-user":             "ops@pbs",
		"prune-schedule":          "weekly",
		"tuning":                  "sync-level=file",
		"verify-new":              "1",
		"digest":                  "abc123def456",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}

	require.Equal(t, []string{"backend", "backing-device"}, rec.form["delete"],
		"repeatable --delete must produce repeated delete= form entries")
}

// TestDatastoreUpdate_OmitsUnsetFlags verifies that when only one flag is
// supplied, no other optional key leaks into the PUT body.
func TestDatastoreUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)

	var rec recordedRequest

	recordJSON(f, "PUT "+fmt.Sprintf(pathConfigDatastoreFmt, "audit-store"), &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer

	err := run(deps, &buf, newDatastoreCmd(), "datastore", "update", "audit-store", "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{
		"counter-reset-schedule", "gc-on-unmount", "gc-schedule",
		"keep-last", "keep-hourly", "keep-daily", "keep-weekly", "keep-monthly", "keep-yearly",
		"maintenance-mode", "notification-mode", "notification-thresholds", "notify", "notify-user",
		"prune-schedule", "tuning", "verify-new", "delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

// TestPruneRun_AuditAllFlags verifies every `pbs prune run` flag — including
// the full keep-* set — reaches the POST /admin/datastore/{store}/prune-datastore
// body together in one request.
func TestPruneRun_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)

	var rec recordedRequest

	recordJSON(f, "POST "+prunePruneDatastorePath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)

	var buf bytes.Buffer

	err := run(deps, &buf, newPruneCmd(), "prune", "run",
		"--store", pruneStore,
		"--ns", "audit-ns",
		"--max-depth", "3",
		"--keep-last", "1",
		"--keep-hourly", "2",
		"--keep-daily", "3",
		"--keep-weekly", "4",
		"--keep-monthly", "5",
		"--keep-yearly", "6",
		"--dry-run",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"ns":           "audit-ns",
		"max-depth":    "3",
		"keep-last":    "1",
		"keep-hourly":  "2",
		"keep-daily":   "3",
		"keep-weekly":  "4",
		"keep-monthly": "5",
		"keep-yearly":  "6",
		"dry-run":      "1",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// TestPruneJobAdd_AuditAllFlags verifies every `pbs prune job add` flag —
// including the full keep-* set — reaches the POST /config/prune body together
// in one request.
func TestPruneJobAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)

	var rec recordedRequest

	recordJSON(f, "POST "+pruneConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer

	err := run(deps, &buf, newPruneCmd(), "prune", "job", "add", "audit-job",
		"--store", pruneStore,
		"--schedule", "daily",
		"--ns", "audit-ns",
		"--max-depth", "3",
		"--keep-last", "1",
		"--keep-hourly", "2",
		"--keep-daily", "3",
		"--keep-weekly", "4",
		"--keep-monthly", "5",
		"--keep-yearly", "6",
		"--disable",
		"--comment", "audit job",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"id":           "audit-job",
		"store":        pruneStore,
		"schedule":     "daily",
		"ns":           "audit-ns",
		"max-depth":    "3",
		"keep-last":    "1",
		"keep-hourly":  "2",
		"keep-daily":   "3",
		"keep-weekly":  "4",
		"keep-monthly": "5",
		"keep-yearly":  "6",
		"disable":      "1",
		"comment":      "audit job",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// TestPruneJobAdd_OmitsUnsetFlags verifies that with only the required
// --store/--schedule flags, no optional key leaks into the POST body.
func TestPruneJobAdd_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)

	var rec recordedRequest

	recordJSON(f, "POST "+pruneConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer

	err := run(deps, &buf, newPruneCmd(), "prune", "job", "add", "audit-job",
		"--store", pruneStore, "--schedule", "daily")
	require.NoError(t, err)
	require.Equal(t, "audit-job", rec.form.Get("id"))
	require.Equal(t, pruneStore, rec.form.Get("store"))
	require.Equal(t, "daily", rec.form.Get("schedule"))

	for _, key := range []string{
		"ns", "max-depth",
		"keep-last", "keep-hourly", "keep-daily", "keep-weekly", "keep-monthly", "keep-yearly",
		"disable", "comment",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}
