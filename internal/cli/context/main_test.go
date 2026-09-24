package context

import (
	"fmt"
	"os"
	"testing"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// The proxy environment every test in this package runs under, the same one
// the internal/cli tests pin. Go's http.ProxyFromEnvironment reads
// HTTPS_PROXY, HTTP_PROXY, and NO_PROXY once per process and caches the
// answer, so a test that changed them with t.Setenv would see the first
// test's values or leak its own into later tests, depending on which test
// happened to run first. TestMain therefore fixes all three before any test
// runs, and no test in this package may set them. The HTTPS proxy carries a
// password, so a test that prints the environment proxy also proves the
// password is redacted.
const (
	testEnvHTTPSProxy = "http://envuser:envs3cret@env-proxy.test:3128"
	testEnvHTTPProxy  = "http://env-http-proxy.test:3128"
	testEnvNoProxy    = "no-proxy.test"
)

// TestMain pins the proxy environment and clears the eight PMX_API_*
// variables before any test runs, so neither the operator's proxy settings
// nor an override exported in their shell can change which route a test
// resolves. A test that needs a PMX_API_* variable sets it with t.Setenv.
func TestMain(m *testing.M) {
	if err := testhelper.PinProxyEnv(testEnvHTTPSProxy, testEnvHTTPProxy, testEnvNoProxy); err != nil {
		fmt.Fprintf(os.Stderr, "pin the test environment: %v\n", err)
		os.Exit(1)
	}

	restore := testhelper.UnsetAPIEnv()
	code := m.Run()
	restore()

	os.Exit(code)
}
