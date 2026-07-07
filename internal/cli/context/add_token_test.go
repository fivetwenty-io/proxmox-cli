package context

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/config"
)

// TestSplitTokenID exercises the identity normalisation used by `context add`
// for token auth, covering pass-through, full-identifier splitting, and the
// malformed-input error paths.
func TestSplitTokenID(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		tokenID   string
		wantUser  string
		wantToken string
		wantErr   string
	}{
		{
			name:      "plain token name passes through",
			username:  "root@pam",
			tokenID:   "backup",
			wantUser:  "root@pam",
			wantToken: "backup",
		},
		{
			name:      "full identifier is split, empty username filled",
			username:  "",
			tokenID:   "root@pam!backup",
			wantUser:  "root@pam",
			wantToken: "backup",
		},
		{
			name:      "full identifier agrees with explicit username",
			username:  "root@pam",
			tokenID:   "root@pam!backup",
			wantUser:  "root@pam",
			wantToken: "backup",
		},
		{
			name:     "conflicting username is rejected",
			username: "admin@pve",
			tokenID:  "root@pam!backup",
			wantErr:  "conflicts",
		},
		{
			name:    "missing user before bang",
			tokenID: "!backup",
			wantErr: "missing the user@realm",
		},
		{
			name:    "missing token name after bang",
			tokenID: "root@pam!",
			wantErr: "missing the token name",
		},
		{
			name:    "too many bangs",
			tokenID: "root@pam!a!b",
			wantErr: `must not contain "!"`,
		},
		{
			name:     "username with bang is rejected",
			username: "root@pam!backup",
			tokenID:  "backup",
			wantErr:  `must not contain "!"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, token, err := splitTokenID(tt.username, tt.tokenID)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantUser, user)
			require.Equal(t, tt.wantToken, token)
		})
	}
}

// TestAdd_TokenAuth_RequiresUsername pins that a token context can no longer be
// added without a username: without it the client would send an unusable
// "@realm!tokenid=secret" header and the API would answer 401.
func TestAdd_TokenAuth_RequiresUsername(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "nouser",
		"--host", "10.0.0.5",
		"--auth-type", "token",
		"--token-id", "backup",
		"--secret", "${SECRET}",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--username is required for token auth")
}

// TestAdd_TokenID_FullIdentifierSplit confirms that pasting the full Proxmox
// token id (user@realm!name) into --token-id populates username and token-id
// separately, so the persisted context authenticates.
func TestAdd_TokenID_FullIdentifierSplit(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "pasted",
		"--host", "10.0.0.5",
		"--auth-type", "token",
		"--token-id", "root@pam!backup",
		"--secret", "${SECRET}",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	ctx := updated.Contexts["pasted"]
	require.Equal(t, "root@pam", ctx.Auth.Username)
	require.Equal(t, "backup", ctx.Auth.TokenID)
}

// TestAdd_TokenID_ConflictingUsername rejects an explicit --username that
// disagrees with the user embedded in a full --token-id identifier.
func TestAdd_TokenID_ConflictingUsername(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "conflict",
		"--host", "10.0.0.5",
		"--auth-type", "token",
		"--username", "admin@pve",
		"--token-id", "root@pam!backup",
		"--secret", "${SECRET}",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "conflicts")
}
