package context

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// The three pages below follow the shape of the unauthenticated root page each
// Proxmox web interface serves: a node-named title ending in the product name,
// then that product's own asset paths. Each also mentions the other products in
// ordinary page text and links, to show that only the title is read.

const pveRootPage = `<!DOCTYPE html>
<html>
  <head>
    <meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge">
    <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
    <title>pve-0 - Proxmox Virtual Environment</title>
    <link rel="icon" sizes="128x128" href="/pve2/images/logo-128.png" />
    <link rel="stylesheet" type="text/css" href="/pve2/ext6/theme-crisp/resources/theme-crisp-all.css" />
    <link rel="stylesheet" type="text/css" href="/pve2/fa/css/font-awesome.css" />
    <script type="text/javascript" src="/pve2/ext6/ext-all.js"></script>
    <script type="text/javascript" src="/pve2/js/pvemanagerlib.js?ver=9.2.5"></script>
  </head>
  <body>
    <!-- Integrates with Proxmox Backup Server and Proxmox Datacenter Manager -->
    <script type="text/javascript">Proxmox = { Setup: { auth_cookie_name: 'PVEAuthCookie' } };</script>
  </body>
</html>
`

const pbsRootPage = `<!DOCTYPE html>
<html>
  <head>
    <meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge">
    <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
    <title>pbs-0 - Proxmox Backup Server</title>
    <link rel="icon" sizes="128x128" href="/images/logo-128.png" />
    <link rel="stylesheet" type="text/css" href="/extjs/theme-crisp/resources/theme-crisp-all.css" />
    <script type="text/javascript" src="/extjs/ext-all.js"></script>
    <script type="text/javascript" src="/js/proxmox-backup-gui.js"></script>
  </head>
  <body>
    <a href="https://pve.proxmox.com/wiki/Proxmox_VE">Proxmox Virtual Environment</a>
  </body>
</html>
`

const pdmRootPage = `<!DOCTYPE html>
<html>
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
    <title>pdm-0 - Proxmox Datacenter Manager</title>
    <link rel="icon" sizes="128x128" href="/images/logo-128.png" />
    <link rel="stylesheet" href="/css/pdm-ui.css" />
  </head>
  <body>
    <p>Manage Proxmox Virtual Environment and Proxmox Backup Server remotes.</p>
  </body>
</html>
`

// TestProductFromRootPage covers the title reader directly: every product's
// page, the ways a page can fail to identify one, and the cases that must never
// read as a different product.
func TestProductFromRootPage(t *testing.T) {
	cases := []struct {
		name string
		page string
		want string
	}{
		{"PVE root page", pveRootPage, config.ProductPVE},
		{"PBS root page", pbsRootPage, config.ProductPBS},
		{"PDM root page", pdmRootPage, config.ProductPDM},
		{"login page titled by the product alone", "<html><title>Proxmox Backup Server</title></html>", config.ProductPBS},
		{"upper-case tag and attributes", `<TITLE lang="en">node1 - Proxmox Virtual Environment</TITLE>`, config.ProductPVE},
		{"title split across lines", "<title>\n  pdm-0 -\n  Proxmox Datacenter Manager\n</title>", config.ProductPDM},
		{"entity in the node name", "<title>a&amp;b - Proxmox Backup Server</title>", config.ProductPBS},
		{"no title", "<html><body>Proxmox Backup Server</body></html>", ""},
		{"empty page", "", ""},
		{"unrelated application", "<title>Grafana</title>", ""},
		{"product name only in the body", "<title>Welcome</title><body>Proxmox Virtual Environment</body>", ""},
		{"product name only in a comment", "<title>Welcome</title><!-- Proxmox Backup Server -->", ""},
		{"title does not end in the product", "<title>Proxmox Backup Server docs</title>", ""},
		{"product name inside a longer word", "<title>node - NotProxmox Backup Server</title>", ""},
		{"no separator before the name", "<title>pbs0Proxmox Backup Server</title>", ""},
		{"title tag name is only a prefix", "<titlebar>pbs-0 - Proxmox Backup Server</titlebar>", ""},
		{"unclosed title", "<title>pbs-0 - Proxmox Backup Server", ""},
		{"two titles naming different products",
			"<title>a - Proxmox Backup Server</title><title>b - Proxmox Virtual Environment</title>", ""},
		{"two titles naming the same product",
			"<title>a - Proxmox Backup Server</title><title>b - Proxmox Backup Server</title>", config.ProductPBS},
		{"an untitled first element and a product second",
			"<title></title><title>a - Proxmox Datacenter Manager</title>", config.ProductPDM},
		{"non-ASCII text before the title keeps offsets straight",
			"<!-- İİİİ --><title>n1 - Proxmox Virtual Environment</title>", config.ProductPVE},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, productFromRootPage(tc.page))
		})
	}
}

// TestProbeContext_RootPageFallback is the live-path half. It serves each
// product's page over TLS, with and without the Server header, and checks the
// product the probe reports. PBS 4.x sends no Server header, so its row with
// no header is the reported defect.
func TestProbeContext_RootPageFallback(t *testing.T) {
	const html = "text/html; charset=utf-8"

	cases := []struct {
		name        string
		server      string
		contentType string
		status      int
		page        string
		want        string
	}{
		{"PVE with its header", "pve-api-daemon/3.0", html, http.StatusOK, pveRootPage, config.ProductPVE},
		{"PVE without a header", "", html, http.StatusOK, pveRootPage, config.ProductPVE},
		{"PBS with its header", "proxmox-backup-proxy/4.0", html, http.StatusOK, pbsRootPage, config.ProductPBS},
		{"PBS without a header", "", html, http.StatusOK, pbsRootPage, config.ProductPBS},
		{"PDM with its header", "proxmox-datacenter-api/0.1", html, http.StatusOK, pdmRootPage, config.ProductPDM},
		{"PDM without a header", "", html, http.StatusOK, pdmRootPage, config.ProductPDM},
		{"the header outranks the page", "pve-api-daemon/3.0", html, http.StatusOK, pbsRootPage, config.ProductPVE},
		{"no header and an unrelated page", "", html, http.StatusOK, "<title>nginx</title>", ""},
		{"no header and no page", "", html, http.StatusOK, "", ""},
		{"no header and a JSON body", "", "application/json", http.StatusOK, pbsRootPage, ""},
		{"no header and no content type", "", "", http.StatusOK, pbsRootPage, ""},
		{"no header and a plain-text body", "", "text/plain", http.StatusOK, pbsRootPage, ""},
		{"no header and an error page", "", html, http.StatusServiceUnavailable, pbsRootPage, ""},
		{"no header and a not-found page", "", html, http.StatusNotFound, pveRootPage, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// A nil entry in the header map stops net/http adding a
				// Content-Type of its own, so "no content type" is a real case.
				w.Header()["Server"] = nil
				w.Header()["Content-Type"] = nil
				if tc.server != "" {
					w.Header().Set("Server", tc.server)
				}
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.page))
			}))
			defer ts.Close()

			got := mustProbe(t, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))

			require.True(t, got.Reachable, probeErrText(got))
			require.Equal(t, tc.want, got.ProductGuess)
		})
	}
}

// TestProbeContext_RootPageFallback_ReadIsBounded covers a page whose title
// lies past the read limit, and a server that never stops sending: the probe
// reads at most probeBodyLimit bytes, so the first reports no product and the
// second does not run the probe past its bound.
func TestProbeContext_RootPageFallback_ReadIsBounded(t *testing.T) {
	t.Run("a title past the limit is not found", func(t *testing.T) {
		padding := strings.Repeat("<!-- padding -->\n", probeBodyLimit/16+1)
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(padding + pbsRootPage))
		}))
		defer ts.Close()

		got := mustProbe(t, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))

		require.True(t, got.Reachable, probeErrText(got))
		require.Empty(t, got.ProductGuess)
	})

	t.Run("a title just inside the limit is found", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(strings.Repeat(" ", probeBodyLimit/2) + pbsRootPage))
		}))
		defer ts.Close()

		got := mustProbe(t, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))

		require.Equal(t, config.ProductPBS, got.ProductGuess)
	})

	t.Run("a body cut short is no signal and no failure", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Length", "100000")
			_, _ = w.Write([]byte("<html><head>"))
		}))
		defer ts.Close()

		got := mustProbe(t, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))

		require.True(t, got.Reachable, probeErrText(got))
		require.Empty(t, got.ProductGuess)
	})
}

// TestValidateConnect_HeaderlessRootPage covers the verb end to end for the
// reported case, a PBS endpoint that sends no Server header. Its context
// declares the product, so the column reads match, and a PVE context pointed
// at the same page reads mismatch and not unverified. An endpoint whose page
// names no product keeps reading unverified.
func TestValidateConnect_HeaderlessRootPage(t *testing.T) {
	serve := func(t *testing.T, page string) *httptest.Server {
		t.Helper()

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header()["Server"] = nil
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
		}))
		t.Cleanup(ts.Close)

		return ts
	}

	t.Run("a PBS context matches", func(t *testing.T) {
		ts := serve(t, pbsRootPage)
		cfg := validateConnectCfg(t, ts)
		cfg.Contexts["live"].Product = config.ProductPBS

		path, cfg := makeConfig(t, cfg)
		out, err := run(t, makeDeps(t, path, cfg), "", "validate", "live", "--connect")

		require.NoError(t, err)
		require.Contains(t, out, "match (pbs)")
		require.NotContains(t, out, "unverified")
	})

	t.Run("a PVE context pointed at it is a mismatch", func(t *testing.T) {
		ts := serve(t, pbsRootPage)
		cfg := validateConnectCfg(t, ts)
		cfg.Contexts["live"].Product = config.ProductPVE

		path, cfg := makeConfig(t, cfg)
		out, err := run(t, makeDeps(t, path, cfg), "", "validate", "live", "--connect")

		require.NoError(t, err, "a product mismatch is a warning, never a failure")
		require.Contains(t, out, "mismatch (endpoint looks like pbs)")
	})

	t.Run("a page that names no product stays unverified", func(t *testing.T) {
		ts := serve(t, "<html><head><title>login</title></head></html>")
		cfg := validateConnectCfg(t, ts)
		cfg.Contexts["live"].Product = config.ProductPBS

		path, cfg := makeConfig(t, cfg)
		out, err := run(t, makeDeps(t, path, cfg), "", "validate", "live", "--connect")

		require.NoError(t, err)
		require.Contains(t, out, "unverified")
		require.NotContains(t, out, "match (")
	})
}
