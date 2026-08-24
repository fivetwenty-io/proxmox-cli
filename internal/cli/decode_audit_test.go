package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// rigidScalarKinds are the Go types that cannot decode what PVE sends. PVE is
// Perl: a value documented as an integer arrives as the number 5 or the string
// "5" depending on how that scalar was last used, and a boolean arrives as 1,
// 0, "1", "0", or (in pass-through payloads only) true. A field of one of
// these types fails the whole decode the first time the other form shows up.
//
// Strings are absent deliberately: a string field decodes only a JSON string,
// but PVE's string-valued fields are consistently strings.
var rigidScalarKinds = map[string]bool{
	"bool": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
}

// decodeAuditExempt lists packages whose hand-rolled decoders read a product
// that is not Perl. PBS and PDM are Rust and serialise through serde, so a
// documented integer is always a JSON number.
var decodeAuditExempt = map[string]bool{
	"pbs": true,
	"pdm": true,
}

// decodeAuditAllowed lists the type.field pairs deliberately left rigid,
// each with the reason. Adding to it is a decision, not a formality: the
// whole point of this guard is that a new rigid field cannot appear silently.
var decodeAuditAllowed = map[string]string{}

// TestPVEDecoders_UseTolerantScalars guards the class of defect that made
// `pmx lab ceph osd` and `pmx pve cluster firewall ipset list` fail outright:
// a hand-rolled decoder declared a Go bool or int for a field PVE renders
// through a Perl scalar, so the command aborted on the real payload.
//
// The SDK already solves this with client.PVEBool, client.PVEInt, and
// client.PVEFloat. This asserts that every struct pmx decodes a PVE response
// into uses them.
func TestPVEDecoders_UseTolerantScalars(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "cli")

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if decodeAuditExempt[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}

		structs := structDecls(file)
		for name := range decodeTargets(file) {
			st, ok := structs[name]
			if !ok {
				continue
			}
			for _, f := range rigidFields(name, st) {
				offenders = append(offenders, path+": "+f)
			}
		}
		return nil
	})
	require.NoError(t, err)

	sort.Strings(offenders)
	require.Empty(t, offenders, "these fields decode a PVE response into a rigid Go scalar; "+
		"use pve.PVEInt, pve.PVEBool, or pve.PVEFloat, or record an exemption in decodeAuditAllowed")
}

// structDecls indexes every struct type declared in file by name.
func structDecls(file *ast.File) map[string]*ast.StructType {
	out := map[string]*ast.StructType{}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		if st, ok := ts.Type.(*ast.StructType); ok {
			out[ts.Name.Name] = st
		}
		return true
	})
	return out
}

// decodeTargets names every type in file that a json.Unmarshal call decodes
// into. It resolves the "&v" argument back to the type v was declared with in
// the enclosing function, which covers the two shapes this repository uses:
// "var v T" and "v := T{...}".
func decodeTargets(file *ast.File) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		declared := declaredTypes(fn.Body)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isJSONUnmarshal(call) || len(call.Args) < 2 {
				return true
			}
			unary, ok := call.Args[1].(*ast.UnaryExpr)
			if !ok || unary.Op != token.AND {
				return true
			}
			if id, ok := unary.X.(*ast.Ident); ok {
				if name := declared[id.Name]; name != "" {
					out[name] = true
				}
			}
			return true
		})
		return true
	})
	return out
}

// declaredTypes maps each local variable name in body to the named type it
// was declared with, for the "var v T", "var v []T", and "v := T{...}" forms.
func declaredTypes(body *ast.BlockStmt) map[string]string {
	out := map[string]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.DeclStmt:
			gd, ok := s.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				return true
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || vs.Type == nil {
					continue
				}
				if name := typeName(vs.Type); name != "" {
					for _, id := range vs.Names {
						out[id.Name] = name
					}
				}
			}
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE || len(s.Lhs) != len(s.Rhs) {
				return true
			}
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				if lit, ok := s.Rhs[i].(*ast.CompositeLit); ok && lit.Type != nil {
					if name := typeName(lit.Type); name != "" {
						out[id.Name] = name
					}
				}
			}
		}
		return true
	})
	return out
}

// typeName returns the local type name of expr, unwrapping slices and
// pointers. A qualified type (pkg.T) is not local, so it returns "".
func typeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.ArrayType:
		return typeName(t.Elt)
	default:
		return ""
	}
}

// isJSONUnmarshal reports whether call is json.Unmarshal(...).
func isJSONUnmarshal(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Unmarshal" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "json"
}

// rigidFields returns the tagged fields of st whose Go type cannot decode
// every form PVE renders the value in.
func rigidFields(typeName string, st *ast.StructType) []string {
	var out []string
	for _, f := range st.Fields.List {
		if f.Tag == nil {
			continue
		}
		tag, err := strconv.Unquote(f.Tag.Value)
		if err != nil || !strings.Contains(tag, `json:"`) {
			continue
		}
		kind := scalarKind(f.Type)
		if kind == "" || !rigidScalarKinds[kind] {
			continue
		}
		for _, name := range f.Names {
			key := typeName + "." + name.Name
			if _, ok := decodeAuditAllowed[key]; ok {
				continue
			}
			out = append(out, key+" is "+kind)
		}
	}
	return out
}

// scalarKind returns the underlying builtin name of a scalar field type,
// unwrapping pointers and slices, or "" for anything else.
func scalarKind(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return scalarKind(t.X)
	case *ast.ArrayType:
		return scalarKind(t.Elt)
	default:
		return ""
	}
}
