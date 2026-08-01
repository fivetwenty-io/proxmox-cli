package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// writeUnknownKeysConfig writes body to a temp config file and returns its path.
func writeUnknownKeysConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

// TestUnknownKeys_ReportsTypos is the point of the whole pass: Load ignores a
// key it does not recognise, so a misspelling silently drops the setting. Here
// `fingerprnt` means TLS pinning is not happening, and nothing said so.
func TestUnknownKeys_ReportsTypos(t *testing.T) {
	path := writeUnknownKeysConfig(t, `
current-context: lab
contexts:
  lab:
    host: pve.example.com
    tls:
      fingerprnt: AA:BB
    auth:
      type: token
      usrname: root@pam
labs_dir: /srv/labs
retention: 30
`)

	keys, err := config.UnknownKeys(path)
	require.NoError(t, err)
	require.Equal(t, []string{
		"contexts.lab.auth.usrname",
		"contexts.lab.tls.fingerprnt",
		"retention",
	}, keys, "each unknown key must be named by its full path")
}

// TestUnknownKeys_AcceptsAValidConfig guards the other direction: a config
// exercising every top-level section must report nothing, or the check becomes
// noise the operator learns to ignore.
func TestUnknownKeys_AcceptsAValidConfig(t *testing.T) {
	path := writeUnknownKeysConfig(t, `
current-context: lab
previous-context: prod
default-output: json
warnings-as-errors: true
labs_dir: /srv/labs
include:
  - /srv/labs/*.yaml
log:
  layout: flat
  level: debug
  retention: 14
storage:
  nfs_reserved_gb: 100
contexts:
  lab:
    host: pve.example.com
    port: 8006
    protocol: https
    product: pve
    realm: pam
    default-node: pve-0
    auth:
      type: token
      username: root@pam
      token-id: cli
      secret: keychain:pmx-lab/root@pam
    tls:
      insecure: false
    ssh:
      user: root
      port: 22
`)

	keys, err := config.UnknownKeys(path)
	require.NoError(t, err)
	require.Empty(t, keys)
}

// TestUnknownKeys_MissingFileIsSilent matches Load, which treats an absent
// config as an empty one rather than an error.
func TestUnknownKeys_MissingFileIsSilent(t *testing.T) {
	keys, err := config.UnknownKeys(filepath.Join(t.TempDir(), "absent.yml"))
	require.NoError(t, err)
	require.Empty(t, keys)
}

// TestUnknownKeys_EmptyFileIsSilent covers a config that exists but holds
// nothing, which decodes to a nil document.
func TestUnknownKeys_EmptyFileIsSilent(t *testing.T) {
	keys, err := config.UnknownKeys(writeUnknownKeysConfig(t, ""))
	require.NoError(t, err)
	require.Empty(t, keys)
}

// TestUnknownKeys_UnparseableFileErrors distinguishes "this file has a bad
// key" from "this file is not YAML": no key list can be derived from the
// latter, so it must not be reported as a clean config.
func TestUnknownKeys_UnparseableFileErrors(t *testing.T) {
	_, err := config.UnknownKeys(writeUnknownKeysConfig(t, "contexts:\n  lab:\n   bad indent:\n  - x\n"))
	require.Error(t, err)
}

// TestUnknownKeys_NamesUnderMapsAreNotKeys pins that operator-chosen names —
// context names, lab names — are never mistaken for unknown fields, however
// unusual they look.
func TestUnknownKeys_NamesUnderMapsAreNotKeys(t *testing.T) {
	path := writeUnknownKeysConfig(t, `
contexts:
  not-a-field-name:
    host: pve.example.com
  another_weird.name:
    host: pbs.example.com
`)

	keys, err := config.UnknownKeys(path)
	require.NoError(t, err)
	require.Empty(t, keys)
}

// TestUnknownKeys_DescendsIntoLists covers the sequence case, where a bad key
// sits inside one element of a list and the path has to name which one.
func TestUnknownKeys_DescendsIntoLists(t *testing.T) {
	path := writeUnknownKeysConfig(t, `
labs:
  demo:
    name: demo
    network:
      vnets:
        - id: labA
          tag: 100
        - id: labB
          taag: 200
`)

	keys, err := config.UnknownKeys(path)
	require.NoError(t, err)
	require.Equal(t, []string{"labs.demo.network.vnets[1].taag"}, keys,
		"the path must name which element carries the bad key")
}
