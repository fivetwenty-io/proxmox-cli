package cli_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
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
var connectionEnvVars = testhelper.APIEnvNames()

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

// rootDrivingCalls are the functions whose use in a test means the test can
// reach OverridesFromCommand, which reads the PMX_API_* variables. Any
// qualifier counts, so a package's own tests calling them unqualified count
// too.
var rootDrivingCalls = map[string]bool{
	"NewRootCmd":            true,
	"OverridesFromCommand":  true,
	"BuildContextClient":    true,
	"BuildContextPBSClient": true,
	"BuildContextPDMClient": true,
	"BuildContextAnyClient": true,
}

// testPackageScan records, for one directory, whether its tests reach the
// root and whether its TestMain clears the PMX_API_* variables first.
type testPackageScan struct {
	drivesRoot bool
	// trigger names the first file and call that made drivesRoot true, for
	// the failure message.
	trigger  string
	isolated bool
}

// TestEveryRootDrivingTestPackageClearsAPIEnv walks every test file under
// internal/ and cmd/ and fails when a package's tests execute commands
// through the real root, resolve connection overrides, or start a built pmx
// binary, and that package has no TestMain that calls
// testhelper.UnsetAPIEnv before m.Run. Without that TestMain, a
// PMX_API_ENDPOINT or PMX_API_JUMP exported in the operator's shell would
// send the package's requests somewhere other than the fake server the test
// started. Each package needs its own TestMain because go test runs every
// package in a process of its own.
func TestEveryRootDrivingTestPackageClearsAPIEnv(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	scans := map[string]*testPackageScan{}

	for _, top := range []string{"internal", "cmd"} {
		start := filepath.Join(moduleRoot, top)

		err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				name := d.Name()
				if path != start && (name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
					return filepath.SkipDir
				}

				return nil
			}

			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}

			rel, err := filepath.Rel(moduleRoot, filepath.Dir(path))
			if err != nil {
				return err
			}

			scan, ok := scans[rel]
			if !ok {
				scan = &testPackageScan{}
				scans[rel] = scan
			}

			return scanTestFile(path, scan)
		})
		require.NoError(t, err, "walk %s", start)
	}

	var driving, missing []string

	for dir, scan := range scans {
		if !scan.drivesRoot {
			continue
		}

		driving = append(driving, dir)
		if !scan.isolated {
			missing = append(missing, dir+" (reaches the root through "+scan.trigger+")")
		}
	}

	slices.Sort(missing)

	// The walk must see the packages known to drive the root, or a broken
	// walk would pass with nothing checked.
	for _, known := range []string{"internal/cli", "internal/cli/storage", "internal/cli/node", "cmd/pmx"} {
		require.Contains(t, driving, filepath.FromSlash(known), "the scan must find %s", known)
	}

	require.Empty(t, missing,
		"these test packages reach the root but have no TestMain calling testhelper.UnsetAPIEnv "+
			"before m.Run; add a main_test.go like internal/cli/api/main_test.go")
}

// scanTestFile parses one test file and updates scan with whether it reaches
// the root and whether it declares a TestMain that isolates the PMX_API_*
// variables.
func scanTestFile(path string, scan *testPackageScan) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || scan.drivesRoot {
			return !scan.drivesRoot
		}

		if name := callName(call); name != "" {
			scan.drivesRoot = true
			scan.trigger = filepath.Base(path) + ": " + name
		}

		return true
	})

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "TestMain" && fn.Body != nil && clearsAPIEnvBeforeRun(fn.Body) {
			scan.isolated = true
		}
	}

	return nil
}

// callName returns a description of call when it is one that reaches the
// root, and "" otherwise. It recognises the functions in rootDrivingCalls,
// cli.Main and cli.Execute, and exec.Command("go", "build", ...), which is
// how a test builds a pmx binary to start.
func callName(call *ast.CallExpr) string {
	var qualifier, name string

	switch fun := call.Fun.(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		name = fun.Sel.Name
		if x, ok := fun.X.(*ast.Ident); ok {
			qualifier = x.Name
		}
	default:
		return ""
	}

	switch {
	case rootDrivingCalls[name]:
		return name
	case qualifier == "cli" && (name == "Main" || name == "Execute"):
		return "cli." + name
	case qualifier == "exec" && name == "Command" && len(call.Args) >= 2 &&
		isStringLit(call.Args[0], "go") && isStringLit(call.Args[1], "build"):
		return `exec.Command("go", "build", ...)`
	default:
		return ""
	}
}

// isStringLit reports whether expr is the string literal want.
func isStringLit(expr ast.Expr, want string) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}

	value, err := strconv.Unquote(lit.Value)

	return err == nil && value == want
}

// clearsAPIEnvBeforeRun reports whether body calls testhelper.UnsetAPIEnv
// before its first call to a Run method, which in a TestMain is m.Run.
func clearsAPIEnvBeforeRun(body *ast.BlockStmt) bool {
	var unsetAt, runAt token.Pos

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		switch {
		case x.Name == "testhelper" && sel.Sel.Name == "UnsetAPIEnv" && unsetAt == token.NoPos:
			unsetAt = call.Pos()
		case sel.Sel.Name == "Run" && runAt == token.NoPos:
			runAt = call.Pos()
		}

		return true
	})

	return unsetAt != token.NoPos && runAt != token.NoPos && unsetAt < runAt
}

// findModuleRoot returns the directory holding go.mod, searching upward from
// the package directory go test runs in.
func findModuleRoot(t *testing.T) string {
	t.Helper()

	dir, err := filepath.Abs(".")
	require.NoError(t, err)

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "no go.mod above the test directory")
		dir = parent
	}
}
