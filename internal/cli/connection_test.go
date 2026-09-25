package cli_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	yaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// testFingerprint returns a well-formed colon-separated SHA-256 fingerprint
// whose every pair is b.
func testFingerprint(b byte) string {
	pairs := make([]string, 32)
	for i := range pairs {
		pairs[i] = fmt.Sprintf("%02X", b)
	}

	return strings.Join(pairs, ":")
}

var (
	fpFlag    = testFingerprint(0x11)
	fpEnv     = testFingerprint(0x22)
	fpContext = testFingerprint(0x33)
)

// labContext returns a fully specified pve context with no connection
// settings of its own.
func labContext() *config.Context {
	return &config.Context{
		Host:     "pve1",
		Port:     8006,
		Protocol: "https",
		Realm:    "pam",
		Product:  config.ProductPVE,
		Auth: config.AuthBlock{
			Type:     "token",
			Username: "root@pam",
			TokenID:  "pmx",
			Secret:   "${PMX_TEST_TOKEN_SECRET}",
		},
	}
}

// withContext returns labContext after mutate has changed it.
func withContext(mutate func(*config.Context)) *config.Context {
	c := labContext()
	mutate(c)

	return c
}

// overridesFromArgs runs a two-level command tree, whose root carries a
// persistent --insecure and the nine connection flags exactly as the real
// root registers them, and returns what OverridesFromCommand reads on the
// leaf.
func overridesFromArgs(t *testing.T, args ...string) (cli.ConnectionOverrides, error) {
	t.Helper()

	var (
		ov    cli.ConnectionOverrides
		ovErr error
		ran   bool
	)

	root := &cobra.Command{Use: "pmx", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("insecure", false, "disable TLS certificate verification")
	cli.RegisterConnectionFlags(root.PersistentFlags())

	leaf := &cobra.Command{
		Use: "leaf",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ran = true
			ov, ovErr = cli.OverridesFromCommand(cmd)

			return nil
		},
	}
	root.AddCommand(leaf)
	root.SetArgs(append([]string{"leaf"}, args...))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	if err := root.Execute(); err != nil {
		return cli.ConnectionOverrides{}, err
	}

	require.True(t, ran, "the leaf command never ran")

	return ov, ovErr
}

// resolveArgs resolves ctx as context "lab" under the overrides args and the
// current environment produce.
func resolveArgs(t *testing.T, ctx *config.Context, args ...string) (cli.Connection, error) {
	t.Helper()

	ov, err := overridesFromArgs(t, args...)
	if err != nil {
		return cli.Connection{}, err
	}

	return cli.ResolveConnection("lab", ctx, ov)
}

// mustResolve is resolveArgs for a resolve that must succeed.
func mustResolve(t *testing.T, ctx *config.Context, args ...string) cli.Connection {
	t.Helper()

	conn, err := resolveArgs(t, ctx, args...)
	require.NoError(t, err)

	return conn
}

// setEnv sets every variable in env for the rest of the test.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()

	for k, v := range env {
		t.Setenv(k, v)
	}
}

// unsetEnv removes name for the rest of the test and restores it afterwards.
func unsetEnv(t *testing.T, name string) {
	t.Helper()

	t.Setenv(name, "")
	require.NoError(t, os.Unsetenv(name))
}

// proxyURL returns the string form of conn's proxy URL, or "" for none.
func proxyURL(conn cli.Connection) string {
	if conn.Proxy.URL == nil {
		return ""
	}

	return conn.Proxy.URL.String()
}

type precedenceLevel struct {
	name string
	run  func(t *testing.T)
}

// TestResolveConnection walks every parameter of the precedence table in
// flag, environment, context, and default order. Each level sets every
// lower-priority source too, so it proves it beats them, and a level the
// table marks "none" proves that nothing at that level has any effect.
// The proxy ladder follows, one case per step.
func TestResolveConnection(t *testing.T) {
	rows := []struct {
		param  string
		levels []precedenceLevel
	}{
		{"host", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_ENDPOINT", "pve-env")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Host = "pve-ctx" }),
					"--api-endpoint", "pve-flag")
				require.Equal(t, "pve-flag", conn.Host)
				require.Equal(t, "--api-endpoint", conn.EndpointSource)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_ENDPOINT", "pve-env")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Host = "pve-ctx" }))
				require.Equal(t, "pve-env", conn.Host)
				require.Equal(t, "$PMX_API_ENDPOINT", conn.EndpointSource)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Host = "pve-ctx" }))
				require.Equal(t, "pve-ctx", conn.Host)
				require.Empty(t, conn.EndpointSource)
			}},
			{"default", func(t *testing.T) {
				_, err := resolveArgs(t, withContext(func(c *config.Context) { c.Host = "" }))
				require.EqualError(t, err,
					`context "lab" has no host; set host on the context or pass --api-endpoint`)
			}},
		}},
		{"port", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_ENDPOINT", "pve1:1002")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Port = 1003 }),
					"--api-endpoint", "pve1:1001")
				require.Equal(t, 1001, conn.Port)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_ENDPOINT", "pve1:1002")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Port = 1003 }))
				require.Equal(t, 1002, conn.Port)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Port = 1003 }))
				require.Equal(t, 1003, conn.Port)
			}},
			{"default", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Port = 0 }))
				require.Equal(t, 8006, conn.Port)

				pbs := mustResolve(t, withContext(func(c *config.Context) { c.Port, c.Product = 0, config.ProductPBS }))
				require.Equal(t, 8007, pbs.Port)
			}},
		}},
		{"protocol", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_ENDPOINT", "http://pve1")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Protocol = "http" }),
					"--api-endpoint", "https://pve1")
				require.Equal(t, "https", conn.Protocol)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_ENDPOINT", "https://pve1")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Protocol = "http" }))
				require.Equal(t, "https", conn.Protocol)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Protocol = "http" }))
				require.Equal(t, "http", conn.Protocol)
			}},
			{"default", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Protocol = "" }))
				require.Equal(t, "https", conn.Protocol)
			}},
		}},
		{"insecure", []precedenceLevel{
			{"flag", func(t *testing.T) {
				conn := mustResolve(t, labContext(), "--insecure")
				require.True(t, conn.Insecure)
			}},
			{"environment has no mirror", func(t *testing.T) {
				t.Setenv("PMX_API_INSECURE", "true")
				conn := mustResolve(t, labContext())
				require.False(t, conn.Insecure)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Insecure = true }))
				require.True(t, conn.Insecure)
			}},
			{"default", func(t *testing.T) {
				require.False(t, mustResolve(t, labContext()).Insecure)
			}},
		}},
		{"fingerprint", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_FINGERPRINT", fpEnv)
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Fingerprint = fpContext }),
					"--api-fingerprint", fpFlag)
				require.Equal(t, fpFlag, conn.Fingerprint)
				require.Equal(t, "--api-fingerprint", conn.FingerprintSource)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_FINGERPRINT", fpEnv)
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Fingerprint = fpContext }))
				require.Equal(t, fpEnv, conn.Fingerprint)
				require.Equal(t, "$PMX_API_FINGERPRINT", conn.FingerprintSource)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Fingerprint = fpContext }))
				require.Equal(t, fpContext, conn.Fingerprint)
				require.Equal(t, "tls.fingerprint", conn.FingerprintSource)
			}},
			{"default", func(t *testing.T) {
				conn := mustResolve(t, labContext())
				require.Empty(t, conn.Fingerprint)
				require.Empty(t, conn.FingerprintSource)
			}},
		}},
		{"ca-cert", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_CA_CERT", "/env.pem")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.CACert = "/ctx.pem" }),
					"--api-ca-cert", "/flag.pem")
				require.Equal(t, "/flag.pem", conn.CACert)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_CA_CERT", "/env.pem")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.CACert = "/ctx.pem" }))
				require.Equal(t, "/env.pem", conn.CACert)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.CACert = "/ctx.pem" }))
				require.Equal(t, "/ctx.pem", conn.CACert)
			}},
			{"default", func(t *testing.T) {
				require.Empty(t, mustResolve(t, labContext()).CACert)
			}},
		}},
		{"tofu", []precedenceLevel{
			{"flag has none", func(t *testing.T) {
				_, err := overridesFromArgs(t, "--api-tofu")
				require.ErrorContains(t, err, "unknown flag: --api-tofu")
			}},
			{"environment has none", func(t *testing.T) {
				t.Setenv("PMX_API_TOFU", "false")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Tofu = true }))
				require.True(t, conn.TOFU)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Tofu = true }))
				require.True(t, conn.TOFU)
				require.False(t, conn.TOFUReadOnly)
			}},
			{"default", func(t *testing.T) {
				require.False(t, mustResolve(t, labContext()).TOFU)
			}},
		}},
		{"realm", []precedenceLevel{
			{"flag has none", func(t *testing.T) {
				_, err := overridesFromArgs(t, "--api-realm", "pve")
				require.ErrorContains(t, err, "unknown flag: --api-realm")
			}},
			{"environment has none", func(t *testing.T) {
				want := mustResolve(t, labContext())
				t.Setenv("PMX_API_REALM", "pve")
				require.Equal(t, want, mustResolve(t, labContext()))
			}},
			{"context", func(t *testing.T) {
				stored := withContext(func(c *config.Context) { c.Realm = "pve" })
				mustResolve(t, stored)
				require.Equal(t, "pve", stored.Realm)
			}},
			{"default", func(t *testing.T) {
				stored := withContext(func(c *config.Context) { c.Realm = "" })
				mustResolve(t, stored)
				require.Empty(t, stored.Realm, "the pam default belongs on a clone, never on the stored context")
			}},
		}},
		{"jump chain", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_JUMP", "jump-env")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "jump-ctx" }),
					"--api-jump", "jump-flag")
				require.Equal(t, "jump-flag", conn.Jump.Chain)
				require.Equal(t, "--api-jump", conn.JumpSource)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_JUMP", "jump-env")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "jump-ctx" }))
				require.Equal(t, "jump-env", conn.Jump.Chain)
				require.Equal(t, "$PMX_API_JUMP", conn.JumpSource)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "jump-ctx" }))
				require.Equal(t, "jump-ctx", conn.Jump.Chain)
				require.Empty(t, conn.JumpSource)
			}},
			{"default", func(t *testing.T) {
				require.Equal(t, apiclient.JumpSpec{}, mustResolve(t, labContext()).Jump)
			}},
		}},
		{"bastion user and port", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_JUMP", "bob@jump-env:2202")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "carol@jump-ctx:2203" }),
					"--api-jump", "alice@jump-flag:2201")
				require.Equal(t, "alice@jump-flag:2201", conn.Jump.Chain)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_JUMP", "bob@jump-env:2202")
				conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "carol@jump-ctx:2203" }))
				require.Equal(t, "bob@jump-env:2202", conn.Jump.Chain)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "carol@jump-ctx:2203" }))
				require.Equal(t, "carol@jump-ctx:2203", conn.Jump.Chain)
			}},
			{"default leaves both to ~/.ssh/config", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.SSH.Jump = "bastion"
					c.SSH.User, c.SSH.Port = "node-user", 2222
				}))
				require.Equal(t, "bastion", conn.Jump.Chain,
					"the node's ssh.user and ssh.port must never reach the bastion hop")
			}},
		}},
		{"bastion key", []precedenceLevel{
			{"flag has none", func(t *testing.T) {
				_, err := overridesFromArgs(t, "--api-jump-identity", "/k")
				require.ErrorContains(t, err, "unknown flag: --api-jump-identity")
			}},
			{"environment has none", func(t *testing.T) {
				jumpCtx := withContext(func(c *config.Context) { c.SSH.Jump = "bastion" })
				want := mustResolve(t, jumpCtx)
				t.Setenv("PMX_API_JUMP_IDENTITY", "/k")
				require.Equal(t, want, mustResolve(t, jumpCtx))
			}},
			{"context has none", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.SSH.Jump = "bastion"
					c.SSH.Identity = "/home/op/.ssh/node_key"
				}))
				require.Equal(t, apiclient.JumpSpec{
					Chain:            "bastion",
					ConnectTimeout:   5 * time.Second,
					FirstByteTimeout: 15 * time.Second,
				}, conn.Jump, "ssh.identity is the node's key and never the bastion's")
			}},
			{"default is ~/.ssh/config and the agent", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "bastion" }))
				require.Empty(t, conn.Jump.Program, "an empty program runs the operator's own ssh")
			}},
		}},
		{"proxy", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_PROXY", "socks5://proxy-env:1080")
				conn := mustResolve(t,
					withContext(func(c *config.Context) { c.Proxy.URL = "socks5://proxy-ctx:1080" }),
					"--api-proxy", "socks5://proxy-flag:1080")
				require.Equal(t, "socks5://proxy-flag:1080", proxyURL(conn))
				require.Equal(t, "--api-proxy", conn.ProxySource)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_PROXY", "socks5://proxy-env:1080")
				conn := mustResolve(t,
					withContext(func(c *config.Context) { c.Proxy.URL = "socks5://proxy-ctx:1080" }))
				require.Equal(t, "socks5://proxy-env:1080", proxyURL(conn))
				require.Equal(t, "$PMX_API_PROXY", conn.ProxySource)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t,
					withContext(func(c *config.Context) { c.Proxy.URL = "socks5://proxy-ctx:1080" }))
				require.Equal(t, "socks5://proxy-ctx:1080", proxyURL(conn))
				require.Empty(t, conn.ProxySource)
			}},
			{"default", func(t *testing.T) {
				require.Equal(t, apiclient.ProxySpec{}, mustResolve(t, labContext()).Proxy)
			}},
		}},
		{"proxy credentials", []precedenceLevel{
			{"flag has none", func(t *testing.T) {
				_, err := resolveArgs(t, labContext(), "--api-proxy", "socks5://u:p@h:1080")
				require.EqualError(t, err, apiProxyCredentialsMessage)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_PROXY", "socks5://env-user:env-pw@proxy-env:1080")
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.Proxy = config.ProxyBlock{
						URL: "socks5://proxy-ctx:1080", Username: "ctx-user", Password: "ctx-pw",
					}
				}))
				require.Equal(t, "env-user", conn.Proxy.URL.User.Username())
				require.Empty(
					t, conn.Proxy.Username, "the context's credentials never join a URL the operator supplied")
				require.Empty(t, conn.Proxy.PasswordRef)

				creds, err := conn.ProxyCredentials()
				require.NoError(t, err)
				require.Nil(t, creds, "the environment URL keeps its own userinfo")
			}},
			{"context", func(t *testing.T) {
				t.Setenv("PMX_TEST_PROXY_PASSWORD", "ctx-pw")
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.Proxy = config.ProxyBlock{
						URL: "socks5://proxy-ctx:1080", Username: "ctx-user", Password: "${PMX_TEST_PROXY_PASSWORD}",
					}
				}))

				creds, err := conn.ProxyCredentials()
				require.NoError(t, err)
				require.Equal(t, url.UserPassword("ctx-user", "ctx-pw"), creds)
			}},
			{"default", func(t *testing.T) {
				creds, err := mustResolve(t, labContext()).ProxyCredentials()
				require.NoError(t, err)
				require.Nil(t, creds)
			}},
		}},
		timeoutRow("connect timeout", "--api-connect-timeout", "PMX_API_CONNECT_TIMEOUT",
			func(c *config.Context, v string) { c.Timeout.Connect = v },
			func(conn cli.Connection) (time.Duration, bool) {
				return conn.Timeouts.Connect, conn.TimeoutsSet.Connect
			},
			5*time.Second),
		timeoutRow("tls-handshake timeout", "--api-tls-handshake-timeout", "PMX_API_TLS_HANDSHAKE_TIMEOUT",
			func(c *config.Context, v string) { c.Timeout.TLSHandshake = v },
			func(conn cli.Connection) (time.Duration, bool) {
				return conn.Timeouts.TLSHandshake, conn.TimeoutsSet.TLSHandshake
			},
			10*time.Second),
		timeoutRow("request timeout", "--api-request-timeout", "PMX_API_REQUEST_TIMEOUT",
			func(c *config.Context, v string) { c.Timeout.Request = v },
			func(conn cli.Connection) (time.Duration, bool) {
				return conn.Timeouts.Request, conn.TimeoutsSet.Request
			},
			30*time.Second),
		{"jump connect timeout", []precedenceLevel{
			{"flag", func(t *testing.T) {
				t.Setenv("PMX_API_CONNECT_TIMEOUT", "2s")
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.SSH.Jump, c.Timeout.Connect = "bastion", "3s"
				}), "--api-connect-timeout", "1s")
				require.Equal(t, time.Second, conn.Jump.ConnectTimeout)
			}},
			{"environment", func(t *testing.T) {
				t.Setenv("PMX_API_CONNECT_TIMEOUT", "2s")
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.SSH.Jump, c.Timeout.Connect = "bastion", "3s"
				}))
				require.Equal(t, 2*time.Second, conn.Jump.ConnectTimeout)
			}},
			{"context", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.SSH.Jump, c.Timeout.Connect = "bastion", "3s"
				}))
				require.Equal(t, 3*time.Second, conn.Jump.ConnectTimeout)
			}},
			{"default", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "bastion" }))
				require.Equal(t, 5*time.Second, conn.Jump.ConnectTimeout)
			}},
		}},
		{"proxy ladder", []precedenceLevel{
			{"step 1 --api-proxy none is direct", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.Proxy = config.ProxyBlock{URL: "socks5://proxy-ctx:1080", Username: "u", Password: "p"}
				}), "--api-proxy", "none", "--api-proxy-from-env")
				require.Equal(t, apiclient.ProxySpec{}, conn.Proxy)
				require.Equal(t, "--api-proxy", conn.ProxySource)
			}},
			{"step 2 a URL with the toggle conflicts", func(t *testing.T) {
				_, err := resolveArgs(t, labContext(), "--api-proxy", "socks5://p:1080", "--api-proxy-from-env")
				require.EqualError(t, err,
					"--api-proxy and --api-proxy-from-env conflict; pass a proxy URL or the "+
						"environment toggle, not both")
			}},
			{"step 3 a URL is used as given", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.Proxy = config.ProxyBlock{URL: "socks5://proxy-ctx:1080", Username: "u", Password: "p"}
				}), "--api-proxy", "http://proxy-flag:3128")
				require.Equal(t, apiclient.ProxySpec{URL: mustParseURL(t, "http://proxy-flag:3128")}, conn.Proxy)
			}},
			{"step 4 the toggle beats proxy.url", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.Proxy.URL = "socks5://proxy-ctx:1080"
				}), "--api-proxy-from-env")
				require.Equal(t, apiclient.ProxySpec{FromEnv: true}, conn.Proxy)
				require.Equal(t, "--api-proxy-from-env", conn.ProxySource)
			}},
			{"step 5 proxy.url carries its credentials separately", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) {
					c.Proxy = config.ProxyBlock{URL: "socks5h://proxy-ctx:1080", Username: "u", Password: "${PW}"}
				}))
				require.Equal(t, apiclient.ProxySpec{
					URL:         mustParseURL(t, "socks5h://proxy-ctx:1080"),
					Username:    "u",
					PasswordRef: "${PW}",
				}, conn.Proxy)
				require.Empty(t, conn.ProxySource)
			}},
			{"step 6 proxy.from-env true", func(t *testing.T) {
				conn := mustResolve(t, withContext(func(c *config.Context) { c.Proxy.FromEnv = new(true) }))
				require.Equal(t, apiclient.ProxySpec{FromEnv: true}, conn.Proxy)
			}},
			{"step 7 direct", func(t *testing.T) {
				require.Equal(t, apiclient.ProxySpec{}, mustResolve(t, labContext()).Proxy)
			}},
		}},
	}

	for _, row := range rows {
		t.Run(row.param, func(t *testing.T) {
			for _, level := range row.levels {
				t.Run(level.name, level.run)
			}
		})
	}
}

// timeoutRow builds the four precedence levels of one timeout, each proving
// its value and whether TimeoutsSet records an explicit choice.
func timeoutRow(
	param, flag, env string,
	store func(*config.Context, string),
	read func(cli.Connection) (time.Duration, bool),
	def time.Duration,
) struct {
	param  string
	levels []precedenceLevel
} {
	check := func(t *testing.T, conn cli.Connection, want time.Duration, wantSet bool) {
		t.Helper()

		got, set := read(conn)
		require.Equal(t, want, got)
		require.Equal(t, wantSet, set)
	}

	return struct {
		param  string
		levels []precedenceLevel
	}{param, []precedenceLevel{
		{"flag", func(t *testing.T) {
			t.Setenv(env, "2s")
			conn := mustResolve(t, withContext(func(c *config.Context) { store(c, "3s") }), flag, "1s")
			check(t, conn, time.Second, true)
		}},
		{"environment", func(t *testing.T) {
			t.Setenv(env, "2s")
			conn := mustResolve(t, withContext(func(c *config.Context) { store(c, "3s") }))
			check(t, conn, 2*time.Second, true)
		}},
		{"context", func(t *testing.T) {
			conn := mustResolve(t, withContext(func(c *config.Context) { store(c, "3s") }))
			check(t, conn, 3*time.Second, true)
		}},
		{"default", func(t *testing.T) {
			check(t, mustResolve(t, labContext()), def, false)
		}},
	}}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	return u
}

// apiProxyCredentialsMessage is the refusal of any userinfo in --api-proxy.
const apiProxyCredentialsMessage = "--api-proxy must not carry credentials, which would show in the process list; " +
	"use $PMX_API_PROXY or the context's proxy.username and proxy.password"

// TestResolveConnection_EndpointResolvesAsUnit proves the flag's endpoint
// wins whole: a port the environment names never leaks into a flag that
// omitted one.
func TestResolveConnection_EndpointResolvesAsUnit(t *testing.T) {
	t.Setenv("PMX_API_ENDPOINT", "https://pve3:9999")

	conn := mustResolve(t, withContext(func(c *config.Context) { c.Port = 8443 }), "--api-endpoint", "pve2")
	require.Equal(t, "pve2", conn.Host)
	require.Equal(t, 8443, conn.Port, "the context's port stands, never the environment's 9999")
	require.Equal(t, "https", conn.Protocol)
	require.Equal(t, "--api-endpoint", conn.EndpointSource)
	require.Empty(t, conn.Notes, "an environment value the flag shadowed is never announced")
}

// TestResolveConnection_EmptyEnvMeansUnset proves each of the eight
// variables, exported as an empty string, resolves exactly as if it were
// unset, against a context that configures everything each one could
// override.
func TestResolveConnection_EmptyEnvMeansUnset(t *testing.T) {
	stored := withContext(func(c *config.Context) {
		c.SSH.Jump = "bastion"
		c.Proxy.URL = "socks5://proxy-ctx:1080"
		c.TLS.Fingerprint = fpContext
		c.TLS.CACert = "/ctx.pem"
		c.Timeout = config.TimeoutBlock{Connect: "3s", TLSHandshake: "4s", Request: "20s"}
	})

	want := mustResolve(t, stored)

	for _, name := range connectionEnvVars {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "")

			ov, err := overridesFromArgs(t)
			require.NoError(t, err)
			require.Equal(t, cli.ConnectionOverrides{}, ov)
			require.Equal(t, want, mustResolve(t, stored))
		})
	}
}

// TestResolveConnection_ProxyFromEnvFlagCanDisableContext proves the
// toggle's explicit false beats a context that enables it.
func TestResolveConnection_ProxyFromEnvFlagCanDisableContext(t *testing.T) {
	stored := withContext(func(c *config.Context) { c.Proxy.FromEnv = new(true) })

	require.Equal(t, apiclient.ProxySpec{FromEnv: true}, mustResolve(t, stored).Proxy)

	conn := mustResolve(t, stored, "--api-proxy-from-env=false")
	require.Equal(t, apiclient.ProxySpec{}, conn.Proxy)
	require.Equal(t, "direct", conn.Via())
	require.Equal(t, "--api-proxy-from-env", conn.ProxySource)

	for _, ctx := range []*config.Context{
		labContext(),
		withContext(func(c *config.Context) { c.Proxy.FromEnv = new(false) }),
	} {
		conn = mustResolve(t, ctx, "--api-proxy-from-env=false")
		require.Equal(t, apiclient.ProxySpec{}, conn.Proxy)
		require.Empty(t, conn.ProxySource, "an explicit false that changed nothing is not the source of the route")
	}
}

// TestResolveConnection_ProxyFromEnvTriState walks the context key's three
// states. Absent and explicit false both take the ladder's last step and
// connect direct, while explicit true takes the environment step. An
// explicit true beside proxy.url is the one combination the block checker
// refuses.
func TestResolveConnection_ProxyFromEnvTriState(t *testing.T) {
	absent := mustResolve(t, labContext())
	require.Equal(t, apiclient.ProxySpec{}, absent.Proxy)

	explicitFalse := mustResolve(t, withContext(func(c *config.Context) { c.Proxy.FromEnv = new(false) }))
	require.Equal(t, apiclient.ProxySpec{}, explicitFalse.Proxy)

	explicitTrue := mustResolve(t, withContext(func(c *config.Context) { c.Proxy.FromEnv = new(true) }))
	require.Equal(t, apiclient.ProxySpec{FromEnv: true}, explicitTrue.Proxy)

	withURL := func(fromEnv *bool) *config.Context {
		return withContext(func(c *config.Context) {
			c.Proxy.URL = "socks5://proxy-ctx:1080"
			c.Proxy.FromEnv = fromEnv
		})
	}

	require.Equal(t, "socks5://proxy-ctx:1080", proxyURL(mustResolve(t, withURL(nil))))
	require.Equal(t, "socks5://proxy-ctx:1080", proxyURL(mustResolve(t, withURL(new(false)))))

	_, err := resolveArgs(t, withURL(new(true)))
	require.EqualError(t, err, `context "lab": proxy.url and proxy.from-env are both set; use one or the other`)
}

// TestResolveConnection_ProxyConflictNamesItsSource proves the conflict
// between a proxy URL and the toggle names whichever source supplied the
// URL.
func TestResolveConnection_ProxyConflictNamesItsSource(t *testing.T) {
	_, err := resolveArgs(t, labContext(), "--api-proxy", "socks5://p:1080", "--api-proxy-from-env")
	require.EqualError(t, err,
		"--api-proxy and --api-proxy-from-env conflict; pass a proxy URL or the environment toggle, not both")

	t.Setenv("PMX_API_PROXY", "socks5://p:1080")

	_, err = resolveArgs(t, labContext(), "--api-proxy-from-env")
	require.EqualError(t, err,
		"$PMX_API_PROXY and --api-proxy-from-env conflict; pass a proxy URL or the environment toggle, not both")

	conn := mustResolve(t, labContext(), "--api-proxy-from-env=false")
	require.Equal(t, "socks5://p:1080", proxyURL(conn), "an explicit false toggle does not conflict with a URL")
}

// TestResolveConnection_JumpLadder covers the jump chain's own ladder: the
// flag beats the environment, which beats ssh.jump, "none" from either
// source forces a direct dial, an empty value from either falls through,
// and a rejected chain names its source.
func TestResolveConnection_JumpLadder(t *testing.T) {
	stored := withContext(func(c *config.Context) { c.SSH.Jump = "jump-ctx" })

	t.Run("flag beats environment beats context", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "jump-env")
		require.Equal(t, "jump-flag", mustResolve(t, stored, "--api-jump", "jump-flag").Jump.Chain)
		require.Equal(t, "jump-env", mustResolve(t, stored).Jump.Chain)
	})

	t.Run("none from the flag", func(t *testing.T) {
		conn := mustResolve(t, stored, "--api-jump", "none")
		require.Equal(t, apiclient.JumpSpec{}, conn.Jump)
		require.Equal(t, "--api-jump", conn.JumpSource)
		require.Equal(t, "direct", conn.Via())
	})

	t.Run("none from the environment", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "none")

		conn := mustResolve(t, stored)
		require.Equal(t, apiclient.JumpSpec{}, conn.Jump)
		require.Equal(t, "$PMX_API_JUMP", conn.JumpSource)
	})

	t.Run("none from the flag beats a chain from the environment", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "jump-env")
		require.Equal(t, apiclient.JumpSpec{}, mustResolve(t, stored, "--api-jump", "none").Jump)
	})

	t.Run("an empty flag falls through to the environment", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "jump-env")
		require.Equal(t, "jump-env", mustResolve(t, stored, "--api-jump", "").Jump.Chain)
	})

	t.Run("an empty environment falls through to the context", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "")
		require.Equal(t, "jump-ctx", mustResolve(t, stored).Jump.Chain)
	})

	chainErr := apiclient.ValidateJumpChain("x;id")
	require.Error(t, chainErr)

	t.Run("a rejected ssh.jump", func(t *testing.T) {
		_, err := resolveArgs(t, withContext(func(c *config.Context) { c.SSH.Jump = "x;id" }))
		require.EqualError(t, err, fmt.Sprintf("context %q: ssh.jump %q is not valid: %v", "lab", "x;id", chainErr))
	})

	t.Run("a rejected --api-jump", func(t *testing.T) {
		_, err := resolveArgs(t, stored, "--api-jump", "x;id")
		require.EqualError(t, err, fmt.Sprintf("--api-jump %q is not valid: %v", "x;id", chainErr))
	})

	t.Run("a rejected $PMX_API_JUMP", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "x;id")

		_, err := resolveArgs(t, stored)
		require.EqualError(t, err, fmt.Sprintf("$PMX_API_JUMP %q is not valid: %v", "x;id", chainErr))
	})

	t.Run("none disables a malformed ssh.jump without parsing it", func(t *testing.T) {
		conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "x;id" }), "--api-jump", "none")
		require.Equal(t, apiclient.JumpSpec{}, conn.Jump)
	})

	blankErr := apiclient.ValidateJumpChain(" ")
	require.Error(t, blankErr)

	t.Run("a blank --api-jump never drops the bastion", func(t *testing.T) {
		_, err := resolveArgs(t, stored, "--api-jump", " ")
		require.EqualError(t, err, fmt.Sprintf("--api-jump %q is not valid: %v", " ", blankErr))
	})

	t.Run("a blank $PMX_API_JUMP never drops the bastion", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "  ")

		_, err := resolveArgs(t, stored)
		require.EqualError(t, err, fmt.Sprintf("$PMX_API_JUMP %q is not valid: %v", "  ",
			apiclient.ValidateJumpChain("  ")))
	})

	t.Run("a blank ssh.jump means no bastion", func(t *testing.T) {
		conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "  " }))
		require.Equal(t, apiclient.JumpSpec{}, conn.Jump)
	})

	t.Run("a rejected chain never echoes a password", func(t *testing.T) {
		for _, chain := range []string{
			"u:s3cret@bastion",
			"ssh://u:s3cret@bastion",
			"ok@first,u:s3cret@second",
			"u:s3@cret@bastion",
			"ssh://u:s3@cret@bastion",
		} {
			t.Setenv("PMX_API_JUMP", chain)

			_, err := resolveArgs(t, stored)
			require.Error(t, err, chain)
			require.NotContains(t, err.Error(), "s3", chain)
			require.NotContains(t, err.Error(), "cret", chain)
			require.Contains(t, err.Error(), "u:<redacted>", chain)
		}
	})
}

// TestResolveConnection_RejectsBadOverrides proves a malformed fingerprint
// or a non-positive timeout fails before anything is built, from the flag,
// from the environment, and from a hand-built override.
func TestResolveConnection_RejectsBadOverrides(t *testing.T) {
	_, err := resolveArgs(t, labContext(), "--api-fingerprint", "garbage")
	require.EqualError(t, err,
		`--api-fingerprint "garbage" must be a colon-separated hex SHA-256 (e.g. AA:BB:..., 32 pairs)`)

	_, err = cli.ResolveConnection("lab", labContext(), cli.ConnectionOverrides{Fingerprint: "garbage"})
	require.EqualError(t, err,
		`--api-fingerprint "garbage" must be a colon-separated hex SHA-256 (e.g. AA:BB:..., 32 pairs)`)

	for _, arg := range []string{"--api-connect-timeout=0s", "--api-connect-timeout=-1s"} {
		_, err = resolveArgs(t, labContext(), arg)
		require.EqualError(t, err, "--api-connect-timeout must be greater than zero", arg)
	}

	_, err = resolveArgs(t, labContext(), "--api-connect-timeout", "-1s")
	require.EqualError(t, err, "--api-connect-timeout must be greater than zero")

	_, err = cli.ResolveConnection("lab", labContext(), cli.ConnectionOverrides{Request: -time.Second})
	require.EqualError(t, err, "--api-request-timeout must be greater than zero")

	t.Run("environment", func(t *testing.T) {
		t.Setenv("PMX_API_FINGERPRINT", "garbage")

		_, err := resolveArgs(t, labContext())
		require.EqualError(t, err,
			`$PMX_API_FINGERPRINT "garbage" must be a colon-separated hex SHA-256 (e.g. AA:BB:..., 32 pairs)`)
	})
}

// TestResolveConnection_TimeoutsSetOnlyWhenChosen proves TimeoutsSet is true
// for exactly the bounds a flag, a variable, or a timeout key supplied.
func TestResolveConnection_TimeoutsSetOnlyWhenChosen(t *testing.T) {
	require.Equal(t, cli.TimeoutsSet{}, mustResolve(t, labContext()).TimeoutsSet)

	conn := mustResolve(t, labContext(), "--api-connect-timeout", "1s")
	require.Equal(t, cli.TimeoutsSet{Connect: true}, conn.TimeoutsSet)

	t.Setenv("PMX_API_TLS_HANDSHAKE_TIMEOUT", "2s")

	conn = mustResolve(t, labContext())
	require.Equal(t, cli.TimeoutsSet{TLSHandshake: true}, conn.TimeoutsSet)

	conn = mustResolve(t, withContext(func(c *config.Context) { c.Timeout.Request = "40s" }))
	require.Equal(t, cli.TimeoutsSet{TLSHandshake: true, Request: true}, conn.TimeoutsSet)
	require.Equal(t, apiclient.TimeoutSpec{
		Connect: 5 * time.Second, TLSHandshake: 2 * time.Second, Request: 40 * time.Second,
	}, conn.Timeouts)
}

// TestResolveConnection_RefusesHTTPSDowngrade proves an http override of an
// https context is refused from the flag and from the environment, each
// naming its source, and that an http context accepts one.
func TestResolveConnection_RefusesHTTPSDowngrade(t *testing.T) {
	_, err := resolveArgs(t, labContext(), "--api-endpoint", "http://pve1")
	require.EqualError(t, err,
		`--api-endpoint would downgrade context "lab" from https to http; `+
			`set protocol: http on the context to allow it`)

	defaulted := withContext(func(c *config.Context) { c.Protocol = "" })
	_, err = resolveArgs(t, defaulted, "--api-endpoint", "http://pve1")
	require.Error(t, err, "a context whose protocol defaults to https is protected too")

	httpCtx := withContext(func(c *config.Context) { c.Protocol = "http" })
	require.Equal(t, "http", mustResolve(t, httpCtx, "--api-endpoint", "http://pve1").Protocol)

	upper := withContext(func(c *config.Context) { c.Protocol = "HTTPS" })
	_, err = resolveArgs(t, upper, "--api-endpoint", "http://pve1")
	require.EqualError(t, err,
		`--api-endpoint would downgrade context "lab" from https to http; `+
			`set protocol: http on the context to allow it`,
		"a hand-edited protocol in upper case is protected too")

	t.Setenv("PMX_API_ENDPOINT", "http://pve1")

	_, err = resolveArgs(t, labContext())
	require.EqualError(t, err,
		`$PMX_API_ENDPOINT would downgrade context "lab" from https to http; `+
			`set protocol: http on the context to allow it`)
}

// TestResolveConnection_StoredProtocolIgnoresCase proves a hand-edited
// protocol in any case resolves as its lower-case form, so the first-byte
// timer, the route, and the environment proxy all see the scheme the kit
// really dials.
func TestResolveConnection_StoredProtocolIgnoresCase(t *testing.T) {
	jumpHTTPS := func(protocol string) *config.Context {
		return withContext(func(c *config.Context) {
			c.Host, c.Protocol, c.SSH.Jump = "pve.example", protocol, "bastion"
		})
	}

	for _, protocol := range []string{"HTTPS", "Https"} {
		conn := mustResolve(t, jumpHTTPS(protocol))
		require.Equal(t, "https", conn.Protocol, protocol)
		require.Equal(t, 15*time.Second, conn.Jump.FirstByteTimeout, "an https jump arms the timer: %s", protocol)
	}

	fromEnv := withContext(func(c *config.Context) {
		c.Host, c.Protocol = "pve.example", "HTTPS"
		c.Proxy.FromEnv = new(true)
	})
	require.Equal(t, "proxy http://envuser:<redacted>@env-proxy.test:3128 (from environment)",
		mustResolve(t, fromEnv).Via(), "an upper-case https context takes $HTTPS_PROXY")

	require.Equal(t, "http", mustResolve(t, withContext(func(c *config.Context) { c.Protocol = "HTTP" })).Protocol)
}

// TestResolveConnection_PerformsNoSecretIO proves the resolve never touches
// the proxy password, and that both appliers are where it fails.
func TestResolveConnection_PerformsNoSecretIO(t *testing.T) {
	unsetEnv(t, "PMX_TEST_UNSET_PROXY_PASSWORD")

	stored := withContext(func(c *config.Context) {
		c.Proxy = config.ProxyBlock{
			URL:      "socks5h://proxy:1080",
			Username: "pmx",
			Password: "${PMX_TEST_UNSET_PROXY_PASSWORD}",
		}
	})

	conn, err := cli.ResolveConnection("lab", stored, cli.ConnectionOverrides{})
	require.NoError(t, err)

	const prefix = `resolve proxy.password for context "lab": `

	_, err = conn.ApplyToOptions(pve.Options{})
	require.ErrorContains(t, err, prefix)
	require.True(t, strings.HasPrefix(err.Error(), prefix), err.Error())

	tr, err := conn.ApplyToHTTPTransport(&http.Transport{})
	require.Nil(t, tr)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), prefix), err.Error())
}

// TestResolveConnection_StoredProxyURLNeverLeaks proves a stored proxy.url
// that does not parse, or that embeds credentials, fails through the
// resolver with the block checker's text, named to the context it came
// from, and never with the password.
func TestResolveConnection_StoredProxyURLNeverLeaks(t *testing.T) {
	cases := []struct {
		raw      string
		password string
		want     string
	}{
		{
			raw:      "socks5://pmx:s3cr3t/x@proxy:1080",
			password: "s3cr3t",
			want:     "proxy.url socks5://<redacted> is not a valid URL",
		},
		{
			raw:      "socks5://pmx:s3%zzt@proxy:1080",
			password: "s3%zzt",
			want:     "proxy.url socks5://<redacted> is not a valid URL",
		},
		{
			raw:      "socks5://u:s3cret@[::1",
			password: "s3cret",
			want:     "proxy.url socks5://<redacted> is not a valid URL",
		},
		{
			raw:      "socks5://pmx:s3cr3t@proxy:1080",
			password: "s3cr3t",
			want: "proxy.url socks5://<redacted>@proxy:1080 must not embed credentials; " +
				"use proxy.username and proxy.password",
		},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			stored := withContext(func(c *config.Context) { c.Proxy.URL = tc.raw })

			_, err := cli.ResolveConnection("lab", stored, cli.ConnectionOverrides{})
			require.Error(t, err)
			require.Equal(t, fmt.Sprintf("context %q: %s", "lab", tc.want), err.Error())
			require.Equal(t,
				fmt.Sprintf("context %q: %s", "lab", strings.Join(config.ValidateProxyBlock(&stored.Proxy), "; ")),
				err.Error())
			require.NotContains(t, err.Error(), tc.password)
			require.NotContains(t, err.Error(), "pmx:", "the embed-credentials message masks the whole userinfo")
		})
	}
}

// TestResolveConnection_EnvOverrideNotes asserts every note an ambient
// variable earns, and that the same values from flags earn none.
func TestResolveConnection_EnvOverrideNotes(t *testing.T) {
	trustModes := []struct {
		name  string
		setup func(*config.Context)
	}{
		{"fingerprint pin", func(c *config.Context) { c.TLS.Fingerprint = fpContext }},
		{"CA bundle", func(c *config.Context) { c.TLS.CACert = "/ctx.pem" }},
		{"insecure", func(c *config.Context) { c.TLS.Insecure = true }},
		{"trust on first use", func(c *config.Context) { c.TLS.Tofu = true }},
		{"system roots", func(*config.Context) {}},
	}

	type noteCase struct {
		name   string
		env    map[string]string
		stored *config.Context
		want   string
	}

	cases := []noteCase{
		{
			name:   "endpoint",
			env:    map[string]string{"PMX_API_ENDPOINT": "https://pve9:9999"},
			stored: labContext(),
			want:   `note: $PMX_API_ENDPOINT (https://pve9:9999) overrides the endpoint of context "lab"`,
		},
		{
			name:   "proxy",
			env:    map[string]string{"PMX_API_PROXY": "socks5h://proxy-env:1080"},
			stored: labContext(),
			want:   `note: $PMX_API_PROXY (socks5h://proxy-env:1080) overrides the proxy of context "lab"`,
		},
		{
			name:   "proxy none",
			env:    map[string]string{"PMX_API_PROXY": "none"},
			stored: withContext(func(c *config.Context) { c.Proxy.URL = "socks5://proxy-ctx:1080" }),
			want:   `note: $PMX_API_PROXY=none disables the proxy of context "lab"`,
		},
		{
			name:   "jump",
			env:    map[string]string{"PMX_API_JUMP": "bastion"},
			stored: labContext(),
			want:   `note: $PMX_API_JUMP (bastion) overrides the bastion of context "lab"`,
		},
		{
			name:   "jump none",
			env:    map[string]string{"PMX_API_JUMP": "none"},
			stored: withContext(func(c *config.Context) { c.SSH.Jump = "bastion" }),
			want:   `note: $PMX_API_JUMP=none disables the bastion of context "lab"`,
		},
		{
			name:   "connect timeout",
			env:    map[string]string{"PMX_API_CONNECT_TIMEOUT": "2s"},
			stored: labContext(),
			want:   `note: $PMX_API_CONNECT_TIMEOUT (2s) overrides the connect timeout of context "lab"`,
		},
		{
			name:   "tls-handshake timeout",
			env:    map[string]string{"PMX_API_TLS_HANDSHAKE_TIMEOUT": "3s"},
			stored: labContext(),
			want:   `note: $PMX_API_TLS_HANDSHAKE_TIMEOUT (3s) overrides the tls-handshake timeout of context "lab"`,
		},
		{
			name:   "request timeout",
			env:    map[string]string{"PMX_API_REQUEST_TIMEOUT": "500ms"},
			stored: labContext(),
			want:   `note: $PMX_API_REQUEST_TIMEOUT (500ms) overrides the request timeout of context "lab"`,
		},
	}

	for _, mode := range trustModes {
		cases = append(cases,
			noteCase{
				name:   "ca-cert over " + mode.name,
				env:    map[string]string{"PMX_API_CA_CERT": "/env.pem"},
				stored: withContext(mode.setup),
				want: fmt.Sprintf(`note: $PMX_API_CA_CERT (/env.pem) replaces the trust settings of context "lab" (%s)`,
					mode.name),
			},
			noteCase{
				name:   "fingerprint over " + mode.name,
				env:    map[string]string{"PMX_API_FINGERPRINT": fpEnv},
				stored: withContext(mode.setup),
				want: fmt.Sprintf(`note: $PMX_API_FINGERPRINT replaces the trust settings of context "lab" (%s)`,
					mode.name),
			})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.env)
			require.Equal(t, []string{tc.want}, mustResolve(t, tc.stored).Notes)
		})
	}

	t.Run("flags earn no note", func(t *testing.T) {
		stored := withContext(func(c *config.Context) {
			c.SSH.Jump = "bastion"
			c.Proxy.URL = "socks5://proxy-ctx:1080"
		})

		for _, args := range [][]string{
			{"--api-endpoint", "https://pve9:9999"},
			{"--api-proxy", "socks5h://proxy-flag:1080"},
			{"--api-proxy", "none"},
			{"--api-jump", "other-bastion"},
			{"--api-jump", "none"},
			{"--api-ca-cert", "/flag.pem"},
			{"--api-fingerprint", fpFlag},
			{"--api-connect-timeout", "2s", "--api-tls-handshake-timeout", "3s", "--api-request-timeout", "4s"},
		} {
			require.Empty(t, mustResolve(t, stored, args...).Notes, args)
		}
	})

	t.Run("none disables nothing on a context without one", func(t *testing.T) {
		setEnv(t, map[string]string{"PMX_API_PROXY": "none", "PMX_API_JUMP": "none"})
		require.Empty(t, mustResolve(t, labContext()).Notes)
	})

	t.Run("none that cancels --api-proxy-from-env is announced", func(t *testing.T) {
		t.Setenv("PMX_API_PROXY", "none")

		conn := mustResolve(t, labContext(), "--api-proxy-from-env")
		require.Equal(t, apiclient.ProxySpec{}, conn.Proxy)
		require.Equal(t, []string{`note: $PMX_API_PROXY=none disables the proxy of context "lab"`}, conn.Notes)

		require.Empty(t, mustResolve(t, labContext(), "--api-proxy-from-env=false").Notes,
			"an explicit false asks for no proxy, so none cancels nothing")
	})

	t.Run("a timeout note echoes the value as exported", func(t *testing.T) {
		setEnv(t, map[string]string{
			"PMX_API_CONNECT_TIMEOUT":       "1500ms",
			"PMX_API_TLS_HANDSHAKE_TIMEOUT": "90s",
			"PMX_API_REQUEST_TIMEOUT":       "1m",
		})

		require.Equal(t, []string{
			`note: $PMX_API_CONNECT_TIMEOUT (1500ms) overrides the connect timeout of context "lab"`,
			`note: $PMX_API_TLS_HANDSHAKE_TIMEOUT (90s) overrides the tls-handshake timeout of context "lab"`,
			`note: $PMX_API_REQUEST_TIMEOUT (1m) overrides the request timeout of context "lab"`,
		}, mustResolve(t, labContext()).Notes)
	})

	t.Run("a hand-built environment timeout without its raw text", func(t *testing.T) {
		conn, err := cli.ResolveConnection("lab", labContext(), cli.ConnectionOverrides{
			Request: time.Minute, RequestSource: "$PMX_API_REQUEST_TIMEOUT",
		})
		require.NoError(t, err)
		require.Equal(t,
			[]string{`note: $PMX_API_REQUEST_TIMEOUT (1m0s) overrides the request timeout of context "lab"`},
			conn.Notes)
	})

	t.Run("a proxy password never reaches a note", func(t *testing.T) {
		t.Setenv("PMX_API_PROXY", "socks5://u:s3cret@proxy-env:1080")

		notes := mustResolve(t, labContext()).Notes
		require.Equal(t,
			[]string{`note: $PMX_API_PROXY (socks5://u:<redacted>@proxy-env:1080) overrides the proxy of ` +
				`context "lab"`},
			notes)
		require.NotContains(t, strings.Join(notes, "\n"), "s3cret")
	})
}

// TestResolveConnection_TrustOverrides covers the three trust rules.
func TestResolveConnection_TrustOverrides(t *testing.T) {
	everything := func(c *config.Context) {
		c.TLS = config.TLSBlock{Insecure: true, Fingerprint: fpContext, CACert: "/ctx.pem", Tofu: true}
	}

	t.Run("an endpoint override keeps the context's pin", func(t *testing.T) {
		conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Fingerprint = fpContext }),
			"--api-endpoint", "other")
		require.Equal(t, "other", conn.Host)
		require.Equal(t, fpContext, conn.Fingerprint)
		require.Equal(t, "tls.fingerprint", conn.FingerprintSource)
		require.Equal(t, "--api-endpoint", conn.EndpointSource)
	})

	for _, source := range []string{"flag", "environment"} {
		t.Run("a fingerprint from the "+source+" replaces the whole trust mode", func(t *testing.T) {
			args, wantSource := []string{"--api-fingerprint", fpFlag}, "--api-fingerprint"
			if source == "environment" {
				t.Setenv("PMX_API_FINGERPRINT", fpFlag)
				args, wantSource = nil, "$PMX_API_FINGERPRINT"
			}

			conn := mustResolve(t, withContext(everything), args...)
			require.False(t, conn.Insecure)
			require.Equal(t, fpFlag, conn.Fingerprint)
			require.Empty(t, conn.CACert)
			require.False(t, conn.TOFU)
			require.False(t, conn.TOFUReadOnly)
			require.Equal(t, wantSource, conn.FingerprintSource)
		})

		t.Run("a CA bundle from the "+source+" replaces the whole trust mode", func(t *testing.T) {
			var args []string
			if source == "environment" {
				t.Setenv("PMX_API_CA_CERT", "/override.pem")
			} else {
				args = []string{"--api-ca-cert", "/override.pem"}
			}

			conn := mustResolve(t, withContext(everything), args...)
			require.False(t, conn.Insecure)
			require.Empty(t, conn.Fingerprint)
			require.Empty(t, conn.FingerprintSource)
			require.Equal(t, "/override.pem", conn.CACert)
			require.False(t, conn.TOFU)
			require.False(t, conn.TOFUReadOnly)
		})
	}

	t.Run("conflicts", func(t *testing.T) {
		_, err := resolveArgs(t, labContext(), "--api-fingerprint", fpFlag, "--api-ca-cert", "/x.pem")
		require.EqualError(t, err, "--api-fingerprint and --api-ca-cert conflict; pass one trust override, not both")

		_, err = resolveArgs(t, labContext(), "--api-fingerprint", fpFlag, "--insecure")
		require.EqualError(t, err, "--api-fingerprint cannot be combined with --insecure")

		_, err = resolveArgs(t, labContext(), "--api-ca-cert", "/x.pem", "--insecure")
		require.EqualError(t, err, "--api-ca-cert cannot be combined with --insecure")

		t.Setenv("PMX_API_FINGERPRINT", fpEnv)
		t.Setenv("PMX_API_CA_CERT", "/env.pem")

		_, err = resolveArgs(t, labContext())
		require.EqualError(t, err,
			"$PMX_API_FINGERPRINT and $PMX_API_CA_CERT conflict; pass one trust override, not both")

		_, err = resolveArgs(t, labContext(), "--api-ca-cert", "/x.pem")
		require.EqualError(t, err,
			"$PMX_API_FINGERPRINT and --api-ca-cert conflict; pass one trust override, not both")
	})

	t.Run("a context's insecure alone is not a conflict", func(t *testing.T) {
		conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Insecure = true }),
			"--api-fingerprint", fpFlag)
		require.False(t, conn.Insecure)
	})

	t.Run("an environment variable with --insecure", func(t *testing.T) {
		t.Setenv("PMX_API_CA_CERT", "/env.pem")

		_, err := resolveArgs(t, labContext(), "--insecure")
		require.EqualError(t, err, "$PMX_API_CA_CERT cannot be combined with --insecure")
	})

	t.Run("an endpoint override makes trust on first use read-only", func(t *testing.T) {
		pinnedTOFU := withContext(func(c *config.Context) { c.TLS.Fingerprint, c.TLS.Tofu = fpContext, true })
		tofuOnly := withContext(func(c *config.Context) { c.TLS.Tofu = true })

		for _, stored := range []*config.Context{pinnedTOFU, tofuOnly} {
			conn := mustResolve(t, stored, "--api-endpoint", "pve9")
			require.False(t, conn.TOFU)
			require.True(t, conn.TOFUReadOnly)

			unchanged := mustResolve(t, stored)
			require.True(t, unchanged.TOFU)
			require.False(t, unchanged.TOFUReadOnly)
		}

		t.Setenv("PMX_API_ENDPOINT", "pve9:8007")

		conn := mustResolve(t, tofuOnly)
		require.False(t, conn.TOFU)
		require.True(t, conn.TOFUReadOnly)
	})

	t.Run("insecure outranks the read-only cache under an endpoint override", func(t *testing.T) {
		tofuOnly := withContext(func(c *config.Context) { c.TLS.Tofu = true })

		conn := mustResolve(t, tofuOnly, "--insecure", "--api-endpoint", "pve9")
		require.True(t, conn.Insecure)
		require.False(t, conn.TOFU)
		require.False(t, conn.TOFUReadOnly, "--insecure must never be cancelled by a fingerprint cache")

		storedInsecure := withContext(func(c *config.Context) { c.TLS.Insecure, c.TLS.Tofu = true, true })

		conn = mustResolve(t, storedInsecure, "--api-endpoint", "pve9")
		require.True(t, conn.Insecure)
		require.False(t, conn.TOFU)
		require.False(t, conn.TOFUReadOnly, "tls.insecure must never be cancelled by a fingerprint cache")
	})

	t.Run("a trust override leaves trust on first use off, not read-only", func(t *testing.T) {
		stored := withContext(func(c *config.Context) { c.TLS.Tofu = true })

		conn := mustResolve(t, stored, "--api-endpoint", "pve9", "--api-fingerprint", fpFlag)
		require.False(t, conn.TOFU)
		require.False(t, conn.TOFUReadOnly)
	})

	t.Run("a context without trust on first use never becomes read-only", func(t *testing.T) {
		conn := mustResolve(t, labContext(), "--api-endpoint", "pve9")
		require.False(t, conn.TOFU)
		require.False(t, conn.TOFUReadOnly)
	})
}

// TestConnection_WrapPinMismatch covers both sources of a pin failure, the
// read-only trust-on-first-use failure, the precedence of the pin rule, and
// every case that must come back unchanged.
func TestConnection_WrapPinMismatch(t *testing.T) {
	const pinText = `context "lab" pins a certificate that pve9:8006 (from --api-endpoint) does not present; ` +
		`pass --api-fingerprint for that host`

	const readOnlyText = `context "lab" has no trusted certificate for pve9:8006 (from --api-endpoint), ` +
		`and trust on first use is off under an endpoint override; pass --api-fingerprint for that host`

	kitMismatch := func() error {
		return fmt.Errorf(`request failed after 1 attempt(s): Get "https://pve9:8006/api2/json/version": %w`,
			errors.New("cannot verify certificate fingerprint: "+fpFlag))
	}

	probeMismatch := func() error {
		return &url.Error{
			Op:  "Get",
			URL: "https://pve9:8006/api2/json/version",
			Err: fmt.Errorf("tls fingerprint pin: peer certificate is %s, context pins %s: %w",
				fpFlag, fpContext, cli.ErrPinMismatch),
		}
	}

	kitUnknown := func() error {
		return fmt.Errorf(`Get "https://pve9:8006/api2/json/version": %w`,
			errors.New("unknown certificate fingerprint (manual verification required): "+fpFlag))
	}

	pinned := withContext(func(c *config.Context) { c.TLS.Fingerprint = fpContext })
	pinnedTOFU := withContext(func(c *config.Context) { c.TLS.Fingerprint, c.TLS.Tofu = fpContext, true })
	tofuOnly := withContext(func(c *config.Context) { c.TLS.Tofu = true })

	overridden := mustResolve(t, pinned, "--api-endpoint", "pve9")

	for name, mk := range map[string]func() error{"kit text": kitMismatch, "probe sentinel": probeMismatch} {
		t.Run(name, func(t *testing.T) {
			orig := mk()

			got := overridden.WrapPinMismatch(orig)
			require.EqualError(t, got, pinText)
			require.ErrorIs(t, got, orig)

			if name == "probe sentinel" {
				require.ErrorIs(t, got, cli.ErrPinMismatch)

				var ue *url.Error
				require.ErrorAs(t, got, &ue)
			}

			require.Same(t, got, overridden.WrapPinMismatch(got), "a rewritten error is never rewritten again")

			for label, conn := range map[string]cli.Connection{
				"without an override":      mustResolve(t, pinned),
				"on a context with no pin": mustResolve(t, labContext(), "--api-endpoint", "pve9"),
				"with a pin from the flag": mustResolve(
					t, labContext(), "--api-endpoint", "pve9", "--api-fingerprint", fpFlag),
				"with a pin and trust override": mustResolve(
					t, pinned, "--api-endpoint", "pve9", "--api-ca-cert", "/x.pem"),
			} {
				require.Equal(t, orig, conn.WrapPinMismatch(orig), label)
			}
		})
	}

	t.Run("read-only trust on first use", func(t *testing.T) {
		conn := mustResolve(t, tofuOnly, "--api-endpoint", "pve9")
		require.True(t, conn.TOFUReadOnly)

		orig := kitUnknown()

		got := conn.WrapPinMismatch(orig)
		require.EqualError(t, got, readOnlyText)
		require.ErrorIs(t, got, orig)

		require.Equal(t, orig, mustResolve(t, tofuOnly).WrapPinMismatch(orig), "unchanged without the override")

		mismatch := kitMismatch()
		require.Equal(t, mismatch, conn.WrapPinMismatch(mismatch),
			"only the unknown-certificate text means the read-only cache refused")
	})

	t.Run("the pin rule takes precedence over the read-only rule", func(t *testing.T) {
		conn := mustResolve(t, pinnedTOFU, "--api-endpoint", "pve9")
		require.True(t, conn.TOFUReadOnly)
		require.Equal(t, "tls.fingerprint", conn.FingerprintSource)

		orig := kitUnknown()

		got := conn.WrapPinMismatch(orig)
		require.EqualError(t, got, pinText)
		require.ErrorIs(t, got, orig)
	})

	t.Run("the environment's endpoint is named as the source", func(t *testing.T) {
		t.Setenv("PMX_API_ENDPOINT", "pve9")

		got := mustResolve(t, pinned).WrapPinMismatch(kitMismatch())
		require.EqualError(t, got, `context "lab" pins a certificate that pve9:8006 (from $PMX_API_ENDPOINT) `+
			`does not present; pass --api-fingerprint for that host`)
	})

	t.Run("nil and unrelated errors", func(t *testing.T) {
		require.NoError(t, overridden.WrapPinMismatch(nil))

		other := errors.New("connection refused")
		require.Equal(t, other, overridden.WrapPinMismatch(other))
	})
}

// quietTLSServer starts a TLS test server that answers /api2/json/version
// and logs no handshake errors, which the pin tests provoke on purpose.
func quietTLSServer(t *testing.T) (*httptest.Server, string, int) {
	t.Helper()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"9.0","release":"9.0","repoid":"x"}}`))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)

	u := mustParseURL(t, srv.URL)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	return srv, u.Hostname(), port
}

// TestConnection_WrapPinMismatchKitTexts pins the kit's two fingerprint
// failure texts by provoking both from a real kit client, and proves
// WrapPinMismatch rewrites the errors the kit returns at run time. A kit
// upgrade that changes either text fails here.
func TestConnection_WrapPinMismatchKitTexts(t *testing.T) {
	_, host, port := quietTLSServer(t)
	endpoint := net.JoinHostPort(host, strconv.Itoa(port))

	get := func(t *testing.T, fingerprint string, cache bool) error {
		t.Helper()

		opts := apiclient.BuildOptions(host, port, "https", "root@pam", "pam", "pmx=secret", "", "", "", false,
			fingerprint)
		if cache {
			opts.FingerprintCachePath = filepath.Join(t.TempDir(), "fingerprints.json")
		}

		client, err := pve.NewClient(opts)
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })

		_, err = client.GetCtx(pve.WithRetries(context.Background(), 0), "/version", nil)
		require.Error(t, err)

		return err
	}

	wantPin := fmt.Sprintf(`context "lab" pins a certificate that %s (from --api-endpoint) does not present; `+
		`pass --api-fingerprint for that host`, endpoint)

	t.Run("a pin the certificate fails", func(t *testing.T) {
		err := get(t, fpContext, false)
		require.ErrorContains(t, err, "cannot verify certificate fingerprint")

		conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Fingerprint = fpContext }),
			"--api-endpoint", endpoint)
		require.EqualError(t, conn.WrapPinMismatch(err), wantPin)
	})

	t.Run("a read-only cache that holds nothing", func(t *testing.T) {
		err := get(t, "", true)
		require.ErrorContains(t, err, "unknown certificate fingerprint (manual verification required)")

		conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Tofu = true }), "--api-endpoint", endpoint)
		require.EqualError(t, conn.WrapPinMismatch(err), fmt.Sprintf(`context "lab" has no trusted certificate `+
			`for %s (from --api-endpoint), and trust on first use is off under an endpoint override; `+
			`pass --api-fingerprint for that host`, endpoint))
	})

	t.Run("a pin beside a cache reports the unknown text", func(t *testing.T) {
		err := get(t, fpContext, true)
		require.ErrorContains(t, err, "unknown certificate fingerprint (manual verification required)")

		conn := mustResolve(t, withContext(func(c *config.Context) { c.TLS.Fingerprint, c.TLS.Tofu = fpContext, true }),
			"--api-endpoint", endpoint)
		require.EqualError(t, conn.WrapPinMismatch(err), wantPin)
	})
}

// TestResolveConnection_IPv6EndpointBuildsParseableURL feeds a bracketed
// IPv6 override through the kit's own URL builder.
func TestResolveConnection_IPv6EndpointBuildsParseableURL(t *testing.T) {
	conn := mustResolve(t, labContext(), "--api-endpoint", "[::1]:8006")
	require.Equal(t, "[::1]", conn.Host)

	opts := apiclient.BuildOptions(conn.Host, conn.Port, conn.Protocol, "root@pam", "pam", "pmx=secret",
		"", "", "", conn.Insecure, conn.Fingerprint)

	u, err := url.Parse(opts.GetBaseURL())
	require.NoError(t, err)
	require.Equal(t, "::1", u.Hostname())
	require.Equal(t, "8006", u.Port())
}

// TestOverridesFromCommand_RejectsCredentialsInAPIProxy proves the flag may
// carry no userinfo at all, while the environment may.
func TestOverridesFromCommand_RejectsCredentialsInAPIProxy(t *testing.T) {
	for _, raw := range []string{"socks5://u:p@h:1080", "socks5://u@h:1080"} {
		_, err := overridesFromArgs(t, "--api-proxy", raw)
		require.EqualError(t, err, apiProxyCredentialsMessage, raw)
	}

	t.Setenv("PMX_API_PROXY", "socks5://u:p@h:1080")

	ov, err := overridesFromArgs(t)
	require.NoError(t, err)
	require.Equal(t, "socks5://u:p@h:1080", ov.Proxy)
	require.Equal(t, "$PMX_API_PROXY", ov.ProxySource)
}

// TestOverridesFromCommand_RejectsMalformedProxy proves the URL rules of a
// stored proxy.url apply to both override sources, with the source named
// and the URL redacted.
func TestOverridesFromCommand_RejectsMalformedProxy(t *testing.T) {
	_, err := overridesFromArgs(t, "--api-proxy", "https://proxy:3128")
	require.EqualError(t, err, "--api-proxy https://proxy:3128 must use scheme socks5, socks5h, or http")

	_, err = overridesFromArgs(t, "--api-proxy", "socks5:proxy")
	require.EqualError(t, err, "--api-proxy "+redact.ProxyURL("socks5:proxy")+" must include a host")

	_, err = overridesFromArgs(t, "--api-proxy", "socks5://:1080")
	require.EqualError(t, err, "--api-proxy socks5://:1080 must include a host")

	_, err = overridesFromArgs(t, "--api-proxy", "ftp://")
	require.EqualError(t, err,
		"--api-proxy ftp:// must use scheme socks5, socks5h, or http; --api-proxy ftp:// must include a host")

	for _, raw := range []string{"socks5://h:99999", "socks5://h:0", "http://h:65536"} {
		_, err = overridesFromArgs(t, "--api-proxy", raw)
		require.EqualError(t, err, "--api-proxy "+raw+" must use a port from 1 to 65535")
	}

	for _, raw := range []string{"socks5://h:1", "socks5://h:65535", "socks5://h", "socks5://[::1]:1080"} {
		_, err = overridesFromArgs(t, "--api-proxy", raw)
		require.NoError(t, err, raw)
	}

	t.Run("the environment names itself and hides the password", func(t *testing.T) {
		t.Setenv("PMX_API_PROXY", "socks5://u:s3cret@h:0")

		_, err := overridesFromArgs(t)
		require.EqualError(t, err, "$PMX_API_PROXY socks5://u:<redacted>@h:0 must use a port from 1 to 65535")

		_, err = cli.ResolveConnection("lab", labContext(), cli.ConnectionOverrides{
			Proxy: "socks5://u:s3cret@h:99999", ProxySource: "$PMX_API_PROXY",
		})
		require.EqualError(t, err, "$PMX_API_PROXY socks5://u:<redacted>@h:99999 must use a port from 1 to 65535")
	})

	t.Run("a stored proxy.url names its key", func(t *testing.T) {
		for _, raw := range []string{"socks5://proxy:0", "socks5://proxy:99999"} {
			_, err := cli.ResolveConnection("lab",
				withContext(func(c *config.Context) {
					c.Proxy = config.ProxyBlock{URL: raw, Username: "pmx", Password: "literal-s3cret"}
				}), cli.ConnectionOverrides{})
			require.EqualError(t, err, `context "lab": proxy.url `+raw+" must use a port from 1 to 65535")
			require.NotContains(t, err.Error(), "s3cret")
		}
	})

	t.Setenv("PMX_API_PROXY", "socks5://u:s3cret@[::1")

	_, err = overridesFromArgs(t)
	require.EqualError(t, err, "$PMX_API_PROXY socks5://<redacted> is not a valid URL")
	require.NotContains(t, err.Error(), "s3cret")
}

// proxyPathHint is the tail every proxy URL path, query, or fragment message
// carries after the source and the redacted URL.
const proxyPathHint = " must not carry a path, a query, or a fragment; " +
	"a password that contains a reserved character such as /, ?, or # must be percent-encoded"

// TestProxyURLRejectsPathQueryFragment proves a password whose unescaped
// "/", "?", or "#" ends the authority early, so url.Parse reads "pmx:4711"
// as the proxy's host and port, is refused from the flag, the environment,
// and a stored proxy.url alike, with the password nowhere in the text.
func TestProxyURLRejectsPathQueryFragment(t *testing.T) {
	passwordForms := []string{
		"socks5://pmx:4711/x@proxy:1080",
		"socks5://pmx:4711?x@proxy:1080",
		"socks5://pmx:4711#x@proxy:1080",
	}

	t.Run("the flag", func(t *testing.T) {
		// The flag refuses any "@" before it looks at the path, so the
		// password forms fail on credentials and the path rule is shown
		// with URLs that carry none.
		for _, raw := range passwordForms {
			_, err := overridesFromArgs(t, "--api-proxy", raw)
			require.EqualError(t, err, apiProxyCredentialsMessage, raw)
			require.NotContains(t, err.Error(), "4711", raw)
		}

		for _, raw := range []string{"socks5://proxy:1080/x", "socks5://proxy:1080?x", "socks5://proxy:1080#x"} {
			_, err := overridesFromArgs(t, "--api-proxy", raw)
			require.EqualError(t, err, "--api-proxy "+raw+proxyPathHint, raw)

			_, err = cli.ResolveConnection("lab", labContext(), cli.ConnectionOverrides{
				Proxy: raw, ProxySource: "--api-proxy",
			})
			require.EqualError(t, err, "--api-proxy "+raw+proxyPathHint, raw)
		}

		for _, raw := range []string{"socks5://proxy:1080", "socks5://proxy:1080/"} {
			_, err := overridesFromArgs(t, "--api-proxy", raw)
			require.NoError(t, err, raw)
		}
	})

	t.Run("the environment", func(t *testing.T) {
		for _, raw := range passwordForms {
			t.Setenv("PMX_API_PROXY", raw)

			_, err := overridesFromArgs(t)
			require.EqualError(t, err, "$PMX_API_PROXY socks5://<redacted>"+proxyPathHint, raw)
			require.NotContains(t, err.Error(), "4711", raw)

			_, err = cli.ResolveConnection("lab", labContext(), cli.ConnectionOverrides{
				Proxy: raw, ProxySource: "$PMX_API_PROXY",
			})
			require.EqualError(t, err, "$PMX_API_PROXY socks5://<redacted>"+proxyPathHint, raw)
			require.NotContains(t, err.Error(), "4711", raw)
		}

		t.Setenv("PMX_API_PROXY", "socks5://u:s3cret@proxy:1080/")

		_, err := overridesFromArgs(t)
		require.NoError(t, err)
	})

	t.Run("a stored proxy.url", func(t *testing.T) {
		for _, raw := range passwordForms {
			_, err := cli.ResolveConnection("lab",
				withContext(func(c *config.Context) { c.Proxy = config.ProxyBlock{URL: raw} }),
				cli.ConnectionOverrides{})
			require.EqualError(t, err, `context "lab": proxy.url socks5://<redacted>`+proxyPathHint, raw)
			require.NotContains(t, err.Error(), "4711", raw)
		}
	})
}

// TestOverridesFromCommand_InsecureFromRootPersistentFlag proves only the
// root's persistent --insecure counts, never a leaf's local one.
func TestOverridesFromCommand_InsecureFromRootPersistentFlag(t *testing.T) {
	var got []cli.ConnectionOverrides

	build := func() *cobra.Command {
		root := &cobra.Command{Use: "pmx", SilenceUsage: true, SilenceErrors: true}
		root.PersistentFlags().Bool("insecure", false, "disable TLS certificate verification")
		cli.RegisterConnectionFlags(root.PersistentFlags())

		record := func(cmd *cobra.Command, _ []string) error {
			ov, err := cli.OverridesFromCommand(cmd)
			if err != nil {
				return err
			}

			got = append(got, ov)

			return nil
		}

		update := &cobra.Command{Use: "update", RunE: record}
		update.Flags().Bool("insecure", false, "store tls.insecure on the context")

		root.AddCommand(update, &cobra.Command{Use: "get", RunE: record})

		return root
	}

	for _, args := range [][]string{{"get", "--insecure"}, {"update", "--insecure"}, {"get"}} {
		root := build()
		root.SetArgs(args)
		require.NoError(t, root.Execute(), args)
	}

	require.Len(t, got, 3)
	require.True(t, got[0].Insecure, "the root's persistent --insecure")
	require.False(t, got[1].Insecure, "a leaf's local --insecure stores tls.insecure and never overrides")
	require.False(t, got[2].Insecure)
}

// TestOverridesFromCommand_SourcesAndParsing proves every flag and variable
// lands in its field with its source, and that malformed values fail with
// their source named.
func TestOverridesFromCommand_SourcesAndParsing(t *testing.T) {
	ov, err := overridesFromArgs(t,
		"--api-endpoint", "https://[::1]:8443",
		"--api-jump", "alice@bastion:2222",
		"--api-proxy", "socks5h://proxy:1080",
		"--api-ca-cert", "/ca.pem",
		"--api-connect-timeout", "1s",
		"--api-tls-handshake-timeout", "2s",
		"--api-request-timeout", "3s",
	)
	require.NoError(t, err)
	require.Equal(t, cli.ConnectionOverrides{
		Host: "[::1]", Port: 8443, Protocol: "https", EndpointSource: "--api-endpoint",
		Jump: "alice@bastion:2222", JumpSource: "--api-jump",
		Proxy: "socks5h://proxy:1080", ProxySource: "--api-proxy",
		CACert: "/ca.pem", CACertSource: "--api-ca-cert",
		Connect: time.Second, ConnectRaw: "1s", ConnectSource: "--api-connect-timeout",
		TLSHandshake: 2 * time.Second, TLSHandshakeRaw: "2s", TLSHandshakeSource: "--api-tls-handshake-timeout",
		Request: 3 * time.Second, RequestRaw: "3s", RequestSource: "--api-request-timeout",
	}, ov)

	ov, err = overridesFromArgs(t, "--api-fingerprint", fpFlag, "--api-proxy-from-env=false")
	require.NoError(t, err)
	require.Equal(t, cli.ConnectionOverrides{
		Fingerprint: fpFlag, FingerprintSource: "--api-fingerprint", ProxyFromEnvSet: true,
	}, ov)

	_, err = overridesFromArgs(t, "--api-connect-timeout", "5")
	require.EqualError(t, err, `--api-connect-timeout "5" is not a duration (e.g. 5s, 500ms)`)

	_, err = overridesFromArgs(t, "--api-endpoint", "ftp://pve1")
	require.EqualError(t, err, `invalid --api-endpoint "ftp://pve1": scheme must be https or http`)

	t.Run("environment", func(t *testing.T) {
		setEnv(t, map[string]string{
			"PMX_API_ENDPOINT":              "pve9",
			"PMX_API_JUMP":                  "none",
			"PMX_API_PROXY":                 "none",
			"PMX_API_FINGERPRINT":           fpEnv,
			"PMX_API_CONNECT_TIMEOUT":       "100ms",
			"PMX_API_TLS_HANDSHAKE_TIMEOUT": "200ms",
			"PMX_API_REQUEST_TIMEOUT":       "1m",
		})

		ov, err := overridesFromArgs(t)
		require.NoError(t, err)
		require.Equal(t, cli.ConnectionOverrides{
			Host: "pve9", EndpointSource: "$PMX_API_ENDPOINT",
			Jump: "none", JumpSource: "$PMX_API_JUMP",
			Proxy: "none", ProxySource: "$PMX_API_PROXY",
			Fingerprint: fpEnv, FingerprintSource: "$PMX_API_FINGERPRINT",
			Connect: 100 * time.Millisecond, ConnectRaw: "100ms", ConnectSource: "$PMX_API_CONNECT_TIMEOUT",
			TLSHandshake: 200 * time.Millisecond, TLSHandshakeRaw: "200ms",
			TLSHandshakeSource: "$PMX_API_TLS_HANDSHAKE_TIMEOUT",
			Request:            time.Minute, RequestRaw: "1m", RequestSource: "$PMX_API_REQUEST_TIMEOUT",
		}, ov)
	})

	t.Run("malformed environment values", func(t *testing.T) {
		t.Setenv("PMX_API_CONNECT_TIMEOUT", "5 seconds")

		_, err := overridesFromArgs(t)
		require.EqualError(t, err, `$PMX_API_CONNECT_TIMEOUT "5 seconds" is not a duration (e.g. 5s, 500ms)`)

		t.Setenv("PMX_API_CONNECT_TIMEOUT", "0s")

		_, err = overridesFromArgs(t)
		require.EqualError(t, err, "$PMX_API_CONNECT_TIMEOUT must be greater than zero")
	})

	t.Run("nil command", func(t *testing.T) {
		_, err := cli.OverridesFromCommand(nil)
		require.Error(t, err)
	})

	t.Run("a command without the flags reads only the environment", func(t *testing.T) {
		t.Setenv("PMX_API_JUMP", "bastion")

		ov, err := cli.OverridesFromCommand(&cobra.Command{Use: "bare"})
		require.NoError(t, err)
		require.Equal(t, cli.ConnectionOverrides{Jump: "bastion", JumpSource: "$PMX_API_JUMP"}, ov)
	})
}

// TestRegisterConnectionFlags pins the nine flags' names, types, defaults,
// and help text, and proves none takes a shorthand.
func TestRegisterConnectionFlags(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	cli.RegisterConnectionFlags(fs)

	want := []struct {
		name, typ, def, usage string
	}{
		{"api-endpoint", "string", "",
			"override the context's API endpoint for this invocation, as [scheme://]host[:port] ($PMX_API_ENDPOINT)"},
		{"api-jump", "string", "",
			`tunnel the API connection through this ssh jump host, as [user@]host[:port] (comma-separated for a ` +
				`chain); "none" dials direct ($PMX_API_JUMP); -J on ssh commands is separate`},
		{"api-proxy", "string", "",
			`send API requests through this socks5, socks5h, or http proxy URL, with no credentials in the URL; ` +
				`"none" disables a configured proxy ($PMX_API_PROXY)`},
		{"api-proxy-from-env", "bool", "false",
			"honour $HTTPS_PROXY (or $HTTP_PROXY) and $NO_PROXY for API requests; use --api-proxy-from-env=false " +
				"to override a context that enables it"},
		{"api-ca-cert", "string", "",
			"verify the server against this CA certificate (PEM) for this invocation, replacing the context's " +
				"trust settings; not with --insecure or --api-fingerprint ($PMX_API_CA_CERT)"},
		{"api-fingerprint", "string", "",
			"pin the server's TLS certificate to this hex SHA-256 fingerprint for this invocation, replacing the " +
				"context's trust settings with no trust-on-first-use prompt; not with --insecure or --api-ca-cert " +
				"($PMX_API_FINGERPRINT)"},
		{"api-connect-timeout", "string", "",
			"bound TCP connection setup, e.g. 5s, in whole seconds rounded up; through a proxy, only the connect " +
				"to the proxy ($PMX_API_CONNECT_TIMEOUT)"},
		{"api-tls-handshake-timeout", "string", "",
			"bound the TLS handshake, e.g. 10s, in whole seconds rounded up; through a jump, the connect bound " +
				"is added to it ($PMX_API_TLS_HANDSHAKE_TIMEOUT)"},
		{"api-request-timeout", "string", "",
			"bound each attempt of an API request, including an upload's whole body, e.g. 30s; an idempotent " +
				"request may make four attempts ($PMX_API_REQUEST_TIMEOUT)"},
	}

	var names []string

	fs.VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	require.Len(t, names, len(want))

	for _, w := range want {
		f := fs.Lookup(w.name)
		require.NotNil(t, f, w.name)
		require.Equal(t, w.typ, f.Value.Type(), w.name)
		require.Equal(t, w.def, f.DefValue, w.name)
		require.Equal(t, w.usage, f.Usage, w.name)
		require.Empty(t, f.Shorthand, w.name)
	}
}

// TestConnection_ProxyCredentialsSpecialCharacters proves a stored password
// holding any URL delimiter survives the credential join exactly, and
// survives the round trip through the URL the transport dials.
func TestConnection_ProxyCredentialsSpecialCharacters(t *testing.T) {
	for _, password := range []string{"p@ss@word", "p/ss/word", "p:ss:word", "p#ss#word", "p%ss%20word"} {
		t.Run(password, func(t *testing.T) {
			t.Setenv("PMX_TEST_PROXY_PASSWORD", password)

			conn := mustResolve(t, withContext(func(c *config.Context) {
				c.Proxy = config.ProxyBlock{
					URL: "socks5h://proxy:1080", Username: "pmx", Password: "${PMX_TEST_PROXY_PASSWORD}",
				}
			}))

			creds, err := conn.ProxyCredentials()
			require.NoError(t, err)
			require.Equal(t, "pmx", creds.Username())

			got, ok := creds.Password()
			require.True(t, ok)
			require.Equal(t, password, got)

			joined := *conn.Proxy.URL
			joined.User = creds

			reparsed, err := url.Parse(joined.String())
			require.NoError(t, err)

			roundTripped, _ := reparsed.User.Password()
			require.Equal(t, password, roundTripped)
			require.Nil(t, conn.Proxy.URL.User, "the resolved secret never lands on the connection itself")
		})
	}
}

// TestConnection_StringRedactsProxyPassword proves no printed or logged form
// of a Connection carries a proxy password or any part of a literal
// PasswordRef. Each password is a distinctive head and tail around one URL
// delimiter, so a leak of either half is caught.
func TestConnection_StringRedactsProxyPassword(t *testing.T) {
	const head, tail = "qzhead", "vktail"

	for _, delimiter := range []string{"@", "/", ":", "#", "%"} {
		password := head + delimiter + tail

		t.Run(password, func(t *testing.T) {
			stored := withContext(func(c *config.Context) {
				c.Proxy = config.ProxyBlock{URL: "socks5h://proxy:1080", Username: "pmx", Password: password}
			})

			conn := mustResolve(t, stored)
			require.Equal(t, password, conn.Proxy.PasswordRef)

			t.Setenv("PMX_API_PROXY", "socks5://envuser:"+url.QueryEscape(password)+"@proxy-env:1080")
			envConn := mustResolve(t, stored)

			for _, c := range []cli.Connection{conn, envConn} {
				var textOut bytes.Buffer
				slog.New(slog.NewTextHandler(&textOut, nil)).Info("resolved", "conn", c)

				for _, rendered := range []string{
					fmt.Sprintf("%v", c),
					fmt.Sprintf("%+v", c),
					fmt.Sprintf("%#v", c),
					c.String(),
					logged(t, c),
					textOut.String(),
					serialised(t, json.Marshal, c),
					serialised(t, yaml.Marshal, c),
				} {
					require.NotContains(t, rendered, head)
					require.NotContains(t, rendered, tail)
				}
			}

			require.Contains(t, envConn.String(), "socks5://envuser:<redacted>@proxy-env:1080")
			require.Contains(t, logged(t, conn), `"route":"proxy socks5h://proxy:1080"`)

			b, err := json.Marshal(envConn)
			require.NoError(t, err)

			var envView map[string]string
			require.NoError(t, json.Unmarshal(b, &envView))
			require.Equal(t, "proxy socks5://envuser:<redacted>@proxy-env:1080", envView["route"])
			require.Contains(t, serialised(t, yaml.Marshal, conn), "route: proxy socks5h://proxy:1080")
		})
	}
}

// serialised renders conn through marshal as a value, as a pointer, and as a
// field of a value and of a pointer, which are the four shapes a caller that
// embeds a Connection in an output document can hand an encoder, and returns
// every rendering joined.
func serialised(t *testing.T, marshal func(any) ([]byte, error), conn cli.Connection) string {
	t.Helper()

	type holder struct {
		Value   cli.Connection
		Pointer *cli.Connection
	}

	var out []string

	for _, v := range []any{conn, &conn, holder{Value: conn, Pointer: &conn}, &holder{Value: conn, Pointer: &conn}} {
		b, err := marshal(v)
		require.NoError(t, err)

		out = append(out, string(b))
	}

	return strings.Join(out, "\n")
}

// TestConnection_SerialisedFields pins the fields a Connection serialises
// to, which are exactly the fields LogValue logs.
func TestConnection_SerialisedFields(t *testing.T) {
	conn := mustResolve(t, withContext(func(c *config.Context) {
		c.Proxy = config.ProxyBlock{URL: "socks5h://proxy:1080", Username: "pmx", Password: "literal-password"}
	}), "--api-jump", "bastion")

	b, err := json.Marshal(conn)
	require.NoError(t, err)

	var got map[string]string
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, map[string]string{
		"context":               "lab",
		"endpoint":              "https://pve1:8006",
		"route":                 "jump bastion + proxy socks5h://proxy:1080",
		"trust":                 "system roots",
		"connect_timeout":       "5s",
		"tls_handshake_timeout": "10s",
		"request_timeout":       "30s",
	}, got)

	y, err := yaml.Marshal(conn)
	require.NoError(t, err)

	var fromYAML map[string]string
	require.NoError(t, yaml.Unmarshal(y, &fromYAML))
	require.Equal(t, got, fromYAML)
}

// TestConnection_HandBuiltJumpIsMasked pins that every rendering of a
// Connection masks a misused jump password, even for a Connection built by
// hand rather than resolved, and that a valid chain still prints as written.
func TestConnection_HandBuiltJumpIsMasked(t *testing.T) {
	conn := cli.Connection{
		ContextName: "lab", Host: "pve1", Port: 8006, Protocol: "https",
		Jump: apiclient.JumpSpec{Chain: "ssh://admin:Zq9alpha,Xk7bravo@bastion"},
	}

	renderings := strings.Join([]string{
		conn.String(), conn.GoString(), conn.Via(), logged(t, conn),
		fmt.Sprintf("%v %+v %#v %s", conn, conn, conn, conn),
		serialised(t, json.Marshal, conn), serialised(t, yaml.Marshal, conn),
	}, "\n")

	require.Contains(t, renderings, "jump ssh://admin:<redacted>@bastion")

	for _, run := range []string{"Zq9alpha", "Xk7bravo"} {
		require.NotContains(t, renderings, run)
	}

	conn.Jump.Chain = "edge:2222,admin@inner"
	require.Equal(t, "jump edge:2222,admin@inner", conn.Via())
}

// logged renders conn through slog's JSON handler.
func logged(t *testing.T, conn cli.Connection) string {
	t.Helper()

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("resolved", "conn", conn)

	return buf.String()
}

// TestConnection_ViaEnvironmentProxy proves Via evaluates the proxy
// environment TestMain pinned against the resolved API URL.
func TestConnection_ViaEnvironmentProxy(t *testing.T) {
	fromEnv := func(host, protocol string) *config.Context {
		return withContext(func(c *config.Context) {
			c.Host, c.Protocol = host, protocol
			c.Proxy.FromEnv = new(true)
		})
	}

	require.Equal(t, "proxy http://envuser:<redacted>@env-proxy.test:3128 (from environment)",
		mustResolve(t, fromEnv("pve.example", "https")).Via())

	require.Equal(t, "proxy http://env-http-proxy.test:3128 (from environment)",
		mustResolve(t, fromEnv("pve.example", "http")).Via())

	require.Equal(t, "direct (environment proxy not applicable)",
		mustResolve(t, fromEnv("127.0.0.1", "https")).Via())

	require.Equal(t, "direct (environment proxy not applicable)",
		mustResolve(t, fromEnv(testEnvNoProxy, "https")).Via(), "NO_PROXY excludes the host")

	require.Equal(t, "jump bastion + proxy http://envuser:<redacted>@env-proxy.test:3128 (from environment)",
		mustResolve(t, fromEnv("pve.example", "https"), "--api-jump", "bastion").Via())

	require.Equal(t, "jump bastion (environment proxy not applicable)",
		mustResolve(t, fromEnv("127.0.0.1", "https"), "--api-jump", "bastion").Via())

	require.NotContains(t, mustResolve(t, fromEnv("pve.example", "https")).Via(), "envs3cret")
}

// TestConnection_Via covers the routes that need no environment.
func TestConnection_Via(t *testing.T) {
	require.Equal(t, "direct", mustResolve(t, labContext()).Via())
	require.Equal(t, "jump alice@bastion:2222",
		mustResolve(t, labContext(), "--api-jump", "alice@bastion:2222").Via())
	require.Equal(t, "proxy socks5h://proxy:1080",
		mustResolve(t, labContext(), "--api-proxy", "socks5h://proxy:1080").Via())
	require.Equal(t, "jump b1,b2 + proxy socks5h://proxy:1080",
		mustResolve(t, labContext(), "--api-jump", "b1,b2", "--api-proxy", "socks5h://proxy:1080").Via())

	t.Setenv("PMX_API_PROXY", "socks5://u:s3cret@proxy:1080")
	require.Equal(t, "proxy socks5://u:<redacted>@proxy:1080", mustResolve(t, labContext()).Via())
}

// TestResolveConnection_FirstByteTimeoutByRoute proves the first-byte timer
// is armed only where a TLS handshake or a SOCKS negotiation comes first,
// and that its cap always leaves it short of the request bound.
func TestResolveConnection_FirstByteTimeoutByRoute(t *testing.T) {
	route := func(protocol string, mutate func(*config.Context)) *config.Context {
		return withContext(func(c *config.Context) {
			c.Protocol = protocol
			c.SSH.Jump = "bastion"
			mutate(c)
		})
	}

	none := func(*config.Context) {}
	sum := 15 * time.Second

	cases := []struct {
		name   string
		stored *config.Context
		args   []string
		want   time.Duration
	}{
		{"https jump", route("https", none), nil, sum},
		{"http jump through socks5h", route("http", func(c *config.Context) { c.Proxy.URL = "socks5h://p:1080" }),
			nil, sum},
		{"http jump through socks5", route("http", func(c *config.Context) { c.Proxy.URL = "socks5://p:1080" }),
			nil, sum},
		{"http jump", route("http", none), nil, 0},
		{"http jump through an http proxy", route("http", func(c *config.Context) { c.Proxy.URL = "http://p:3128" }),
			nil, 0},
		{
			"http jump under the environment toggle",
			route("http", func(c *config.Context) { c.Proxy.FromEnv = new(true) }),
			nil, 0,
		},
		{"https jump through an http proxy", route("https", func(c *config.Context) { c.Proxy.URL = "http://p:3128" }),
			nil, sum},
		{"no jump", withContext(none), nil, 0},
		{"a 5s request bound", route("https", func(c *config.Context) { c.Timeout.Request = "5s" }),
			nil, 4 * time.Second},
		{"a 2s request bound", route("https", func(c *config.Context) { c.Timeout.Request = "2s" }),
			nil, 1500 * time.Millisecond},
		{"a 1s request bound", route("https", func(c *config.Context) { c.Timeout.Request = "1s" }),
			nil, 750 * time.Millisecond},
		{"an uncapped sum below one second", route("https", none),
			[]string{"--api-connect-timeout", "300ms", "--api-tls-handshake-timeout", "400ms"}, time.Second},
		{"an uncapped sum", route("https", none),
			[]string{"--api-connect-timeout", "1s", "--api-tls-handshake-timeout", "2s"}, 3 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := mustResolve(t, tc.stored, tc.args...)
			require.Equal(t, tc.want, conn.Jump.FirstByteTimeout)
		})
	}
}

// TestResolveConnection_DoesNotMutateStoredContext proves a resolve never
// writes a default, or anything else, back into cfg.Contexts.
func TestResolveConnection_DoesNotMutateStoredContext(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: "pve1",
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "pmx", Secret: "${S}"},
				TLS:  config.TLSBlock{Tofu: true, Fingerprint: fpContext},
				SSH:  config.SSHBlock{Jump: "bastion"},
				Proxy: config.ProxyBlock{
					URL: "socks5h://proxy:1080", Username: "u", Password: "${P}",
				},
				Timeout: config.TimeoutBlock{Connect: "2s"},
			},
		},
	}

	before, err := yaml.Marshal(cfg.Contexts["lab"])
	require.NoError(t, err)

	mustResolve(t, cfg.Contexts["lab"])
	mustResolve(t, cfg.Contexts["lab"], "--api-endpoint", "pve9:9", "--api-proxy", "none")
	mustResolve(t, cfg.Contexts["lab"], "--api-endpoint", "pve9", "--api-jump", "none", "--api-fingerprint", fpFlag)

	after, err := yaml.Marshal(cfg.Contexts["lab"])
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	require.Zero(t, cfg.Contexts["lab"].Port)
	require.Empty(t, cfg.Contexts["lab"].Protocol)
	require.Empty(t, cfg.Contexts["lab"].Realm)
	require.Empty(t, cfg.Contexts["lab"].Product)
}

// TestResolveConnection_RawAndDefaultedAgree proves a raw stored context and
// a defaults-applied clone of it resolve to the same Connection.
func TestResolveConnection_RawAndDefaultedAgree(t *testing.T) {
	raw := &config.Context{
		Host:    "pve1",
		Product: config.ProductPBS,
		Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "pmx", Secret: "${S}"},
		TLS:     config.TLSBlock{Tofu: true},
		SSH:     config.SSHBlock{Jump: "bastion"},
		Proxy:   config.ProxyBlock{FromEnv: new(true)},
	}

	defaulted := config.CloneContext(raw)
	config.ApplyDefaults(defaulted)

	for _, args := range [][]string{nil, {"--api-endpoint", "pve9"}, {"--api-request-timeout", "7s"}} {
		require.Equal(t, mustResolve(t, defaulted, args...), mustResolve(t, raw, args...), args)
	}

	require.Equal(t, 8007, mustResolve(t, raw).Port)
}

// TestResolveConnection_NilContext proves a missing context fails rather
// than panicking.
func TestResolveConnection_NilContext(t *testing.T) {
	_, err := cli.ResolveConnection("lab", nil, cli.ConnectionOverrides{})
	require.EqualError(t, err, `context "lab" is not defined`)
}

// TestConnection_ApplyToOptions proves the proxy, the timeouts, and the jump
// all land on the kit's options, and that a direct connection leaves a
// caller's own proxy function alone. A SOCKS5 proxy lands on the dial rather
// than on the proxy function, so pmx negotiates it itself, and the joined
// credential reaches the proxy.
func TestConnection_ApplyToOptions(t *testing.T) {
	t.Setenv("PMX_TEST_PROXY_PASSWORD", "pw")

	socks := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{ConnectReply: 0x04})

	conn := mustResolve(t, withContext(func(c *config.Context) {
		c.Proxy = config.ProxyBlock{
			URL: "socks5h://" + socks.Addr, Username: "pmx", Password: "${PMX_TEST_PROXY_PASSWORD}",
		}
		c.Timeout = config.TimeoutBlock{Connect: "1500ms", TLSHandshake: "2s", Request: "45s"}
	}))

	opts, err := conn.ApplyToOptions(pve.Options{})
	require.NoError(t, err)
	require.Equal(t, 2, opts.DialTimeoutSec)
	require.Equal(t, 2, opts.TLSHandshakeTimeoutSec)
	require.Equal(t, 45*time.Second, opts.Timeout)
	require.Nil(t, opts.Proxy, "net/http must not run its own SOCKS negotiation")
	require.NotNil(t, opts.DialContext)

	_, err = opts.DialContext(context.Background(), "tcp", "pve1:8006")
	require.ErrorContains(t, err, "could not connect to the target: host unreachable")

	connections := socks.Connections()
	require.Len(t, connections, 1)
	require.Equal(t, "pve1:8006", connections[0].Target)
	require.Equal(t, "pmx", connections[0].Username)
	require.Equal(t, "pw", connections[0].Password)
	require.Nil(t, conn.Proxy.URL.User, "the credential join never writes back to the connection")

	direct := mustResolve(t, labContext())
	callerProxy := func(*http.Request) (*url.URL, error) { return nil, nil }

	opts, err = direct.ApplyToOptions(pve.Options{Proxy: callerProxy})
	require.NoError(t, err)
	require.NotNil(t, opts.Proxy)
	require.Nil(t, opts.DialContext)
	require.Equal(t, 5, opts.DialTimeoutSec)
	require.Equal(t, 10, opts.TLSHandshakeTimeoutSec)
	require.Equal(t, 30*time.Second, opts.Timeout)

	zero, err := cli.Connection{}.ApplyToOptions(pve.Options{})
	require.NoError(t, err)
	require.Equal(t, 5, zero.DialTimeoutSec, "a zero connection falls back to the defaults, never to one second")
}

// TestConnection_ApplyToHTTPTransport proves the bare-transport applier
// installs the proxy, the dialer, and the handshake bound, and that a
// direct connection clears an ambient proxy function.
func TestConnection_ApplyToHTTPTransport(t *testing.T) {
	direct := mustResolve(t, labContext(), "--api-tls-handshake-timeout", "3s")

	tr, err := direct.ApplyToHTTPTransport(&http.Transport{Proxy: http.ProxyFromEnvironment})
	require.NoError(t, err)
	require.Nil(t, tr.Proxy, "a direct connection never honours the ambient proxy environment")
	require.NotNil(t, tr.DialContext)
	require.Equal(t, 3*time.Second, tr.TLSHandshakeTimeout)

	proxied := mustResolve(t, labContext(), "--api-proxy", "http://proxy:3128")

	tr, err = proxied.ApplyToHTTPTransport(&http.Transport{})
	require.NoError(t, err)

	got, err := tr.Proxy(httptest.NewRequest(http.MethodGet, "https://pve1:8006/", nil))
	require.NoError(t, err)
	require.Equal(t, "http://proxy:3128", got.String())

	tr, err = direct.ApplyToHTTPTransport(nil)
	require.Nil(t, tr)
	require.Error(t, err)
}

// TestConnection_JumpHandshakeBoundIncludesConnect proves a jump's handshake
// bound covers the bastion's own connect, and that a slow bastion still
// succeeds inside it.
func TestConnection_JumpHandshakeBoundIncludesConnect(t *testing.T) {
	conn := mustResolve(t, withContext(func(c *config.Context) { c.SSH.Jump = "bastion" }),
		"--api-connect-timeout", "1.4s", "--api-tls-handshake-timeout", "1.4s")

	opts, err := conn.ApplyToOptions(pve.Options{})
	require.NoError(t, err)
	require.Equal(t, 4, opts.TLSHandshakeTimeoutSec, "1.4s + 1.4s + 1s = 3.8s, summed first and rounded up")
	require.Equal(t, 2, opts.DialTimeoutSec)
	require.NotNil(t, opts.DialContext)
	require.Equal(t, 2800*time.Millisecond, conn.Jump.FirstByteTimeout)
	require.Equal(t, 1400*time.Millisecond, conn.Jump.ConnectTimeout)

	noJump := mustResolve(t, labContext(), "--api-connect-timeout", "1.4s", "--api-tls-handshake-timeout", "1.4s")

	opts, err = noJump.ApplyToOptions(pve.Options{})
	require.NoError(t, err)
	require.Equal(t, 2, opts.TLSHandshakeTimeoutSec)

	// The stand-in's 800ms delay is well past the 300ms handshake bound, so
	// the request succeeds only because the connect bound is added to it.
	// The 3s connect bound leaves the first-byte timer at 3.3s and the
	// transport's handshake bound at 4.3s, which is 2.5s above the delay: the
	// in-process TLS server under -race and the stand-in's bash relay can
	// use a second of that on a busy host. SSHStandIn has already paid the
	// first exec of the fresh script, which on a loaded macOS host can stall
	// for far longer than any of these bounds.
	t.Run("a slow bastion inside the bound", func(t *testing.T) {
		_, host, port := quietTLSServer(t)

		script := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
			Mode:       testhelper.SSHForward,
			StartDelay: 800 * time.Millisecond,
		})

		slow := mustResolve(t, withContext(func(c *config.Context) {
			c.Host, c.Port, c.SSH.Jump = host, port, "bastion"
		}), "--api-connect-timeout", "3s", "--api-tls-handshake-timeout", "300ms")
		slow.Jump.Program = script.Program

		require.Equal(t, 3300*time.Millisecond, slow.Jump.FirstByteTimeout)

		apiclient.ReopenJumps()

		tr, err := slow.ApplyToHTTPTransport(&http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // a local test server
			DisableKeepAlives: true,
		})
		require.NoError(t, err)
		require.Equal(t, 4300*time.Millisecond, tr.TLSHandshakeTimeout)
		t.Cleanup(tr.CloseIdleConnections)

		client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			fmt.Sprintf("https://%s/api2/json/version", net.JoinHostPort(host, strconv.Itoa(port))), nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, script.Invocations(t), 1)
	})
}

// TestConnection_JumpFailureCostsOneSSH proves a bastion that starts and
// never forwards costs exactly one ssh per request through a real kit
// client, on every route where the first-byte timer is armed.
func TestConnection_JumpFailureCostsOneSSH(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		proxy bool
	}{
		{
			name: "jump alone",
			args: []string{"--api-connect-timeout", "1s", "--api-tls-handshake-timeout", "1s"},
		},
		{
			name:  "jump and a SOCKS proxy",
			args:  []string{"--api-connect-timeout", "1s", "--api-tls-handshake-timeout", "1s"},
			proxy: true,
		},
		{
			name: "a request bound below the connect and handshake bounds",
			args: []string{
				"--api-connect-timeout", "1s", "--api-tls-handshake-timeout", "2s", "--api-request-timeout", "2s",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
				Mode:      testhelper.SSHHang,
				IgnoreEOF: true,
			})

			args := append([]string{"--api-jump", "bastion"}, tc.args...)

			if tc.proxy {
				socks := testhelper.SOCKS5StandIn(t)
				args = append(args, "--api-proxy", "socks5h://"+socks.Addr)
			}

			conn := mustResolve(t, labContext(), args...)
			conn.Jump.Program = script.Program

			require.Less(t, conn.Jump.FirstByteTimeout, conn.Timeouts.Request)

			opts := apiclient.BuildOptions(conn.Host, conn.Port, conn.Protocol, "root@pam", "pam", "pmx=secret",
				"", "", "", conn.Insecure, conn.Fingerprint)

			opts, err := conn.ApplyToOptions(opts)
			require.NoError(t, err)

			apiclient.ReopenJumps()

			client, err := pve.NewClient(opts)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			ctx := pve.WithRetryDelay(context.Background(), 10*time.Millisecond)

			_, err = client.GetCtx(ctx, "/version", nil)
			require.Error(t, err)
			require.ErrorIs(t, err, apiclient.ErrJump)
			require.NotContains(t, err.Error(), "Client.Timeout exceeded")
			require.Len(t, script.Invocations(t), 1, "a dead bastion is tried once per request")
		})
	}
}

// TestJumpAndProxy_DialerReceivesProxyAddress proves a jump and a proxy
// compose: ssh forwards to the proxy, and only the proxy learns the target.
func TestJumpAndProxy_DialerReceivesProxyAddress(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(origin.Close)

	originPort := mustParseURL(t, origin.URL).Port()
	socks := testhelper.SOCKS5StandIn(t)
	script := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})

	const target = "pve-target.invalid"

	conn := mustResolve(t, withContext(func(c *config.Context) {
		c.Host, c.Protocol, c.SSH.Jump = target, "http", "bastion"
		c.Port, _ = strconv.Atoi(originPort)
	}), "--api-proxy", "socks5h://"+socks.Addr)
	conn.Jump.Program = script.Program

	apiclient.ReopenJumps()

	tr, err := conn.ApplyToHTTPTransport(&http.Transport{DisableKeepAlives: true})
	require.NoError(t, err)
	t.Cleanup(tr.CloseIdleConnections)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+net.JoinHostPort(target, originPort)+"/", nil)
	require.NoError(t, err)

	resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "ok", string(body))

	invocations := script.Invocations(t)
	require.Len(t, invocations, 1)

	argv := invocations[0]
	w := slices.Index(argv, "-W")
	require.GreaterOrEqual(t, w, 0, argv)
	require.Equal(t, socks.Addr, argv[w+1], "the jump dials the proxy, not the target")

	for _, arg := range argv {
		require.NotContains(t, arg, target, "the bastion never learns the target")
	}

	connects := socks.Connections()
	require.Len(t, connects, 1)
	require.Equal(t, net.JoinHostPort(target, originPort), connects[0].Target)
}

// TestSOCKSProxy_StalledTargetFailsAtTheConnectBound proves a SOCKS5 proxy
// that never finishes its own connect to the API host fails at the connect
// bound, on a direct route, and at the connect bound plus the handshake
// bound plus one second through a bastion, whose own setup runs inside the
// dial there.
func TestSOCKSProxy_StalledTargetFailsAtTheConnectBound(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		socks := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{StallConnect: true})

		conn := mustResolve(t, labContext(), "--api-proxy", "socks5h://"+socks.Addr,
			"--api-connect-timeout", "300ms")

		opts, err := conn.ApplyToOptions(pve.Options{})
		require.NoError(t, err)

		start := time.Now()
		_, err = opts.DialContext(context.Background(), "tcp", "pve-target.invalid:8006")

		require.ErrorContains(t, err, "socks5h proxy "+socks.Addr+" did not connect to the target within 300ms")
		require.Less(t, time.Since(start), 3*time.Second)
	})

	t.Run("through a bastion", func(t *testing.T) {
		socks := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{StallConnect: true})
		script := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})

		conn := mustResolve(t, labContext(), "--api-jump", "bastion", "--api-proxy", "socks5h://"+socks.Addr,
			"--api-connect-timeout", "500ms", "--api-tls-handshake-timeout", "500ms")
		conn.Jump.Program = script.Program

		apiclient.ReopenJumps()

		tr, err := conn.ApplyToHTTPTransport(&http.Transport{DisableKeepAlives: true})
		require.NoError(t, err)

		_, err = tr.DialContext(context.Background(), "tcp", "pve-target.invalid:8006")

		require.ErrorContains(t, err, "socks5h proxy "+socks.Addr+" did not connect to the target within 2s")
		require.Len(t, socks.Connections(), 1, "the bastion reached the proxy, and the proxy took the CONNECT")
	})
}

// TestSOCKSProxy_BastionWaitIsNotCharged proves the SOCKS bound starts once
// the bastion's dial returns. Concurrent dials through one bastion wait for
// the first one's ssh to answer before they start their own, and that wait
// must not count against their SOCKS bound, or every dial after the first
// would fail and blame the proxy for a slow bastion.
func TestSOCKSProxy_BastionWaitIsNotCharged(t *testing.T) {
	socks := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{ConnectReply: 0x04})
	script := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{
		Mode: testhelper.SSHForward, StartDelay: 700 * time.Millisecond,
	})

	// The first-byte timer is one second, the floor, and the SOCKS bound is
	// 100ms + 100ms + 1s. Each ssh takes 700ms to answer, so a waiter that
	// started its clock with the first dial would run out at 1.2s.
	conn := mustResolve(t, labContext(), "--api-jump", "bastion", "--api-proxy", "socks5h://"+socks.Addr,
		"--api-connect-timeout", "100ms", "--api-tls-handshake-timeout", "100ms")
	conn.Jump.Program = script.Program

	apiclient.ReopenJumps()

	tr, err := conn.ApplyToHTTPTransport(&http.Transport{DisableKeepAlives: true})
	require.NoError(t, err)

	errs := make([]error, 3)

	var wg sync.WaitGroup

	for i := range errs {
		wg.Go(func() {
			_, errs[i] = tr.DialContext(context.Background(), "tcp", "pve-target.invalid:8006")
		})
	}

	wg.Wait()

	for i, err := range errs {
		require.ErrorContains(t, err, "could not connect to the target: host unreachable", "dial %d", i)
	}

	require.Len(t, socks.Connections(), len(errs))
}

// envSOCKSVar carries the SOCKS5 proxy URL into the child process that
// TestSOCKSProxy_FromEnvironment starts. TestMain pins HTTPS_PROXY to it
// and HTTP_PROXY to it when it is set, because Go reads the proxy
// environment once per process.
const envSOCKSVar = "PMX_TEST_ENV_SOCKS_PROXY"

// TestSOCKSProxy_FromEnvironment proves proxy.from-env hands a SOCKS5 proxy
// the environment names to pmx's own dial, bounded like any other, and that
// it arms the bastion's first-byte timer on an http route as a SOCKS5
// proxy.url does. It runs its checks in a child process with HTTPS_PROXY and
// HTTP_PROXY set to a SOCKS5 URL.
func TestSOCKSProxy_FromEnvironment(t *testing.T) {
	if os.Getenv(envSOCKSVar) != "" {
		conn := mustResolve(t, labContext(), "--api-proxy-from-env", "--api-connect-timeout", "300ms")
		require.Equal(t, "proxy "+os.Getenv(envSOCKSVar)+" (from environment)", conn.Via())

		opts, err := conn.ApplyToOptions(pve.Options{})
		require.NoError(t, err)
		require.Nil(t, opts.Proxy, "net/http must not run its own SOCKS negotiation")

		_, err = opts.DialContext(context.Background(), "tcp", "pve-target.invalid:8006")
		require.ErrorContains(t, err, "did not connect to the target within 300ms")

		jumped := mustResolve(t, withContext(func(c *config.Context) { c.Protocol = "http" }),
			"--api-proxy-from-env", "--api-jump", "bastion")
		require.Positive(t, jumped.Jump.FirstByteTimeout, "SOCKS negotiation comes first, so the timer is armed")

		return
	}

	socks := testhelper.SOCKS5StandInWith(t, testhelper.SOCKS5Options{StallConnect: true})

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSOCKSProxy_FromEnvironment$", "-test.count=1")
	cmd.Env = append(os.Environ(), envSOCKSVar+"=socks5h://"+socks.Addr)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "PASS")
	require.Len(t, socks.Connections(), 1)
}
