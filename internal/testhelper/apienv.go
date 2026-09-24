package testhelper

import (
	"fmt"
	"os"
)

// apiEnvNames are the eight PMX_API_* variables that mirror the root's
// --api-* connection flags. --api-proxy-from-env has no mirror.
var apiEnvNames = []string{
	"PMX_API_ENDPOINT",
	"PMX_API_JUMP",
	"PMX_API_PROXY",
	"PMX_API_CA_CERT",
	"PMX_API_FINGERPRINT",
	"PMX_API_CONNECT_TIMEOUT",
	"PMX_API_TLS_HANDSHAKE_TIMEOUT",
	"PMX_API_REQUEST_TIMEOUT",
}

// APIEnvNames returns the names of the eight PMX_API_* variables, in the
// order the root's --api-* flags are registered. The slice is a fresh copy
// on every call, so a caller may modify it.
func APIEnvNames() []string {
	return append([]string(nil), apiEnvNames...)
}

// UnsetAPIEnv unsets the eight PMX_API_* variables with os.Unsetenv and
// returns a function that restores each one that was set, to the value it
// held, and leaves unset the ones that were not. A package's TestMain calls
// it before m.Run, so an override exported in the operator's shell can never
// change which endpoint, proxy, bastion, or timeout a test resolves, and a
// test that needs a variable sets it with t.Setenv.
//
// It panics when the environment cannot be changed, because a TestMain has
// no test to fail and running the package's tests under a leaked override
// would make their results meaningless. The restore function panics for the
// same reason.
func UnsetAPIEnv() func() {
	saved := make(map[string]string, len(apiEnvNames))

	for _, name := range apiEnvNames {
		if value, ok := os.LookupEnv(name); ok {
			saved[name] = value
		}

		if err := os.Unsetenv(name); err != nil {
			panic(fmt.Sprintf("testhelper: unset %s: %v", name, err))
		}
	}

	return func() {
		for _, name := range apiEnvNames {
			value, ok := saved[name]
			if !ok {
				if err := os.Unsetenv(name); err != nil {
					panic(fmt.Sprintf("testhelper: unset %s: %v", name, err))
				}

				continue
			}

			if err := os.Setenv(name, value); err != nil {
				panic(fmt.Sprintf("testhelper: restore %s: %v", name, err))
			}
		}
	}
}

// PinProxyEnv fixes the proxy environment a package's tests run under. It
// sets HTTPS_PROXY, HTTP_PROXY, and NO_PROXY to the given values and unsets
// their lowercase forms and REQUEST_METHOD, which would otherwise change
// what Go's http.ProxyFromEnvironment answers. The lowercase forms are read
// when the uppercase ones are empty, and REQUEST_METHOD makes Go ignore
// HTTP_PROXY, as it does under CGI.
//
// http.ProxyFromEnvironment reads these variables once per process and
// caches the answer, so a test that changed them with t.Setenv would see
// the first test's values or leak its own into later tests. A TestMain
// therefore calls this before m.Run, and no test in that package may set
// the three names itself.
func PinProxyEnv(httpsProxy, httpProxy, noProxy string) error {
	pinned := []struct{ name, value string }{
		{"HTTPS_PROXY", httpsProxy},
		{"HTTP_PROXY", httpProxy},
		{"NO_PROXY", noProxy},
	}

	for _, p := range pinned {
		if err := os.Setenv(p.name, p.value); err != nil {
			return fmt.Errorf("set %s: %w", p.name, err)
		}
	}

	for _, name := range []string{"https_proxy", "http_proxy", "no_proxy", "REQUEST_METHOD"} {
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("unset %s: %w", name, err)
		}
	}

	return nil
}
