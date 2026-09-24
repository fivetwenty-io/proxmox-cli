package cli_test

import (
	"fmt"
	"os"
	"testing"
)

// The proxy environment every test in this package runs under. Go's
// http.ProxyFromEnvironment reads HTTPS_PROXY, HTTP_PROXY, and NO_PROXY once
// per process and caches the answer, so a test that changed them with
// t.Setenv would see the first test's values or leak its own into later
// tests, depending on which test happened to run first. TestMain therefore
// fixes all three before any test runs, and no test in this package may set
// them. The HTTPS proxy carries a password, so a test that prints the
// environment proxy also proves the password is redacted.
const (
	testEnvHTTPSProxy = "http://envuser:envs3cret@env-proxy.test:3128"
	testEnvHTTPProxy  = "http://env-http-proxy.test:3128"
	testEnvNoProxy    = "no-proxy.test"
)

// connectionEnvVars are the eight PMX_API_* variables. TestMain clears them so
// an operator's shell cannot leak an override into a test, and tests that
// need one set it with t.Setenv.
var connectionEnvVars = []string{
	"PMX_API_ENDPOINT",
	"PMX_API_JUMP",
	"PMX_API_PROXY",
	"PMX_API_CA_CERT",
	"PMX_API_FINGERPRINT",
	"PMX_API_CONNECT_TIMEOUT",
	"PMX_API_TLS_HANDSHAKE_TIMEOUT",
	"PMX_API_REQUEST_TIMEOUT",
}

func TestMain(m *testing.M) {
	if err := pinTestEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "pin the test environment: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// pinTestEnvironment fixes the proxy environment and clears every variable
// that could change which proxy or connection a test resolves.
func pinTestEnvironment() error {
	pinned := map[string]string{
		"HTTPS_PROXY": testEnvHTTPSProxy,
		"HTTP_PROXY":  testEnvHTTPProxy,
		"NO_PROXY":    testEnvNoProxy,
	}

	for name, value := range pinned {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set %s: %w", name, err)
		}
	}

	// The lowercase forms lose to the uppercase ones anyway, and
	// REQUEST_METHOD makes Go ignore HTTP_PROXY, as it does under CGI.
	unset := append([]string{"https_proxy", "http_proxy", "no_proxy", "REQUEST_METHOD"}, connectionEnvVars...)
	for _, name := range unset {
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("unset %s: %w", name, err)
		}
	}

	return nil
}
