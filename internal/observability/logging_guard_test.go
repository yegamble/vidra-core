package observability

// These are the mechanical observability stop guards from
// .ralph/specs/observability.md ("Enforcement", fix_plan P17.2). They parse
// every non-test source file in the module and fail the build when code
// regresses the two hard logging rules:
//
//   - TestNoForbiddenLogging: diagnostics must go through log/slog, never
//     fmt.Print*, log.Print*/Fatal*/Panic*, or the println/print builtins
//     (outside package main and tests).
//   - TestNoSensitiveLogKeys: a denylisted sensitive field name (see
//     IsSensitiveKey) must never be used as a structured-log key.
//
// They run under `make ci` as part of `go test -race ./...`; there is no
// Makefile change to make — being ordinary Go tests is the enforcement.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// moduleRoot walks up from the test's working directory to the directory holding
// go.mod, so the guards scan the whole vidra-core module regardless of where
// `go test` is invoked from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test directory")
		}
		dir = parent
	}
}

// forEachSourceFile parses every non-test .go file under the module root and
// invokes fn with its parsed AST. Vendored and dot-directories (.git, .ralph …)
// are skipped.
func forEachSourceFile(t *testing.T, fn func(path string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	root := moduleRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		fn(path, fset, f)
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
}

// TestNoForbiddenLogging fails when non-test, non-main code uses a banned
// diagnostic primitive instead of log/slog.
func TestNoForbiddenLogging(t *testing.T) {
	forEachSourceFile(t, func(_ string, fset *token.FileSet, file *ast.File) {
		if file.Name.Name == "main" {
			return // entrypoints may use log.Fatal for startup failures
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if name := forbiddenCallName(call.Fun); name != "" {
					t.Errorf("%s: forbidden diagnostic call %q — use log/slog instead (see .ralph/specs/observability.md)",
						fset.Position(call.Pos()), name)
				}
			}
			return true
		})
	})
}

// forbiddenCallName returns a display name if fun is a banned diagnostic call
// (fmt.Print*, log.Print*/Fatal*/Panic*, or the println/print builtins), else "".
// fmt.Fprint*/Sprint* and fmt.Errorf are intentionally allowed.
func forbiddenCallName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		if f.Name == "println" || f.Name == "print" {
			return f.Name
		}
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		if !ok {
			return ""
		}
		sel := f.Sel.Name
		switch pkg.Name {
		case "fmt":
			if strings.HasPrefix(sel, "Print") { // Print, Printf, Println
				return "fmt." + sel
			}
		case "log":
			if strings.HasPrefix(sel, "Print") || strings.HasPrefix(sel, "Fatal") || strings.HasPrefix(sel, "Panic") {
				return "log." + sel
			}
		}
	}
	return ""
}

// logKeyStart maps an slog key/value logging function or method name to the
// argument index where its variadic key/value pairs begin. Keys then appear at
// start, start+2, … Mirrors the slog signatures:
//
//	Info(msg, args...)                 -> 1
//	InfoContext(ctx, msg, args...)     -> 2
//	Log(ctx, level, msg, args...)      -> 3
var logKeyStart = map[string]int{
	"Debug": 1, "Info": 1, "Warn": 1, "Error": 1,
	"DebugContext": 2, "InfoContext": 2, "WarnContext": 2, "ErrorContext": 2,
	"Log": 3,
}

// slogAttrCtor is the set of slog attribute constructors whose first argument is
// the attribute key (slog.String("key", v), slog.Any("key", v), …).
var slogAttrCtor = map[string]bool{
	"String": true, "Int": true, "Int64": true, "Uint64": true, "Float64": true,
	"Bool": true, "Any": true, "Duration": true, "Time": true, "Group": true,
}

// TestNoSensitiveLogKeys fails when a denylisted sensitive field name is used as
// a structured-log key — whether inline in an slog call, in a []any{} args slice
// (the slog args-builder idiom), or as an slog attribute-constructor key.
func TestNoSensitiveLogKeys(t *testing.T) {
	report := func(fset *token.FileSet, pos token.Pos, key string) {
		t.Errorf("%s: sensitive field name %q used as a structured-log key — never log secrets/PII (see .ralph/specs/observability.md)",
			fset.Position(pos), key)
	}
	forEachSourceFile(t, func(_ string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				checkLogCallKeys(node, fset, report)
			case *ast.CompositeLit:
				checkArgSliceKeys(node, fset, report)
			}
			return true
		})
	})
}

// litString returns the unquoted value of a string-literal expression.
func litString(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func checkLogCallKeys(call *ast.CallExpr, fset *token.FileSet, report func(*token.FileSet, token.Pos, string)) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	name := sel.Sel.Name
	// slog attribute constructors: the key is the first argument.
	if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "slog" && slogAttrCtor[name] {
		if len(call.Args) > 0 {
			if k, ok := litString(call.Args[0]); ok && IsSensitiveKey(k) {
				report(fset, call.Args[0].Pos(), k)
			}
		}
		return
	}
	// key/value logging calls (a logger method or a top-level slog.Info/… call).
	start, ok := logKeyStart[name]
	if !ok {
		return
	}
	for i := start; i < len(call.Args); i += 2 {
		if k, ok := litString(call.Args[i]); ok && IsSensitiveKey(k) {
			report(fset, call.Args[i].Pos(), k)
		}
	}
}

// checkArgSliceKeys scans []any{…} / []interface{}{…} composite literals — the
// idiom used to build slog args (see internal/httpapi/server.go, audit.go) —
// treating even-index string literals as keys.
func checkArgSliceKeys(cl *ast.CompositeLit, fset *token.FileSet, report func(*token.FileSet, token.Pos, string)) {
	at, ok := cl.Type.(*ast.ArrayType)
	if !ok || at.Len != nil || !isAnyType(at.Elt) { // must be a slice of any/interface{}
		return
	}
	for i := 0; i < len(cl.Elts); i += 2 {
		if k, ok := litString(cl.Elts[i]); ok && IsSensitiveKey(k) {
			report(fset, cl.Elts[i].Pos(), k)
		}
	}
}

// isAnyType reports whether e denotes `any` or the empty interface `interface{}`.
func isAnyType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "any"
	case *ast.InterfaceType:
		return t.Methods == nil || len(t.Methods.List) == 0
	}
	return false
}
