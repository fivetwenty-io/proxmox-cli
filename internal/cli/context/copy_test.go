package context

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// TestContextCopy_PreservesEveryBlock guards against the copy verb silently
// dropping a block. The hand-written deepCopyContext it replaced dropped the
// whole ssh block (see internal/cli/context/copy.go history); this pins that
// ssh.jump, ssh.identity, proxy.url, proxy.password, and all three timeouts
// all survive `pmx context copy`.
func TestContextCopy_PreservesEveryBlock(t *testing.T) {
	src := labContext()
	src.SSH = config.SSHBlock{
		User:     "admin",
		Port:     2222,
		Identity: "/home/user/.ssh/id_ed25519",
		Jump:     "admin@bastion.example.com:22",
	}
	src.Proxy = config.ProxyBlock{
		URL:      "socks5h://proxy.example.com:1080",
		Username: "pmx",
		Password: "${PMX_PROXY_PASSWORD}",
		FromEnv:  new(true),
	}
	src.Timeout = config.TimeoutBlock{
		Connect:      "5s",
		TLSHandshake: "10s",
		Request:      "30s",
	}

	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": src,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	dst := loaded.Contexts["staging"]
	require.NotNil(t, dst)

	require.Equal(t, src.SSH.User, dst.SSH.User)
	require.Equal(t, src.SSH.Port, dst.SSH.Port)
	require.Equal(t, src.SSH.Identity, dst.SSH.Identity)
	require.Equal(t, src.SSH.Jump, dst.SSH.Jump)

	require.Equal(t, src.Proxy.URL, dst.Proxy.URL)
	require.Equal(t, src.Proxy.Username, dst.Proxy.Username)
	require.Equal(t, src.Proxy.Password, dst.Proxy.Password)
	require.NotNil(t, dst.Proxy.FromEnv)
	require.Equal(t, *src.Proxy.FromEnv, *dst.Proxy.FromEnv)

	require.Equal(t, src.Timeout.Connect, dst.Timeout.Connect)
	require.Equal(t, src.Timeout.TLSHandshake, dst.Timeout.TLSHandshake)
	require.Equal(t, src.Timeout.Request, dst.Timeout.Request)
}
