package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// debtList names the files that may still edit process-global state today.
// Every current offender outside internal/verifydeliver is listed here.
// The list shrinks as offenders are fixed; a file not on this list that
// offends fails the lint, and a listed file with no offender left also fails.
var debtList = map[string]bool{
	"internal/fixture/fixture_test.go":          true,
	"internal/gittest/gittest_test.go":          true,
	"internal/gitx/gitx_test.go":                true,
	"internal/gitx/repo_commit_test.go":         true,
	"internal/home/home_test.go":                true,
	"internal/pool/lease_test.go":               true,
	"internal/repohost/repohost_test.go":        true,
	"internal/session/extrareadfile_test.go":    true,
	"internal/session/headless_test.go":         true,
	"internal/session/herdr_test.go":            true,
	"internal/session/kill_windows_test.go":     true,
	"internal/session/longpath_windows_test.go": true,
	"internal/session/session_test.go":          true,
	"internal/store/checkpoint_test.go":         true,
}

// TestNoGlobalStateEditInInternalTests enforces that no test in internal/
// edits process-global state: the environment (t.Setenv, os.Setenv,
// os.Unsetenv), the working directory (t.Chdir, os.Chdir), or a
// package-level variable declared in non-test code. TestMain functions are
// exempt. It parses every _test.go file under internal/, finds each
// offender, and fails naming file:line and the fix. A debt list names today's
// offenders outside internal/verifydeliver; an offender in an unlisted file
// fails the lint, as does a listed file with no offender left.
func TestNoGlobalStateEditInInternalTests(t *testing.T) {
	root := repoRoot(t)

	// Collect all _test.go files and their packages' non-test declarations.
	testFiles := []string{}
	pkgNonTestDecls := map[string]map[string]bool{} // pkg -> name -> isGlobal
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if !strings.HasPrefix(relSlash, "internal/") {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		dir := filepath.Dir(path)
		isTestFile := strings.HasSuffix(path, "_test.go")

		if !isTestFile {
			// Collect package-level variable declarations.
			if pkgNonTestDecls[dir] == nil {
				pkgNonTestDecls[dir] = make(map[string]bool)
			}
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							pkgNonTestDecls[dir][name.Name] = true
						}
					}
				}
			}
		} else {
			testFiles = append(testFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("lint: walk %s: %v", root, err)
	}

	var violations []string
	filesWithOffenders := make(map[string]bool)

	for _, testFile := range testFiles {
		rel, _ := filepath.Rel(root, testFile)
		relSlash := filepath.ToSlash(rel)
		v := checkTestFileForGlobalStateEdit(t, root, testFile, pkgNonTestDecls)
		if len(v) > 0 {
			filesWithOffenders[relSlash] = true
			violations = append(violations, v...)
		}
	}
	sort.Strings(violations)

	// Check for offenders in files not on the debt list.
	for file := range filesWithOffenders {
		if !debtList[file] {
			t.Errorf("%s: offends but is not on the debt list; either fix it or add it to debtList", file)
			// Report the specific violations in this file for context.
			for _, v := range violations {
				if strings.Contains(v, file) {
					t.Errorf("  - %s", v)
				}
			}
		}
	}

	// Check that every file on the debt list still offends.
	for file := range debtList {
		if !filesWithOffenders[file] {
			t.Errorf("%s: is on the debt list but no longer offends; remove it from debtList", file)
		}
	}
}

// checkTestFileForGlobalStateEdit checks one _test.go file and returns
// "path:line" for each global state edit it finds.
func checkTestFileForGlobalStateEdit(t *testing.T, root, testFile string, pkgNonTestDecls map[string]map[string]bool) []string {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, testFile, nil, 0)
	if err != nil {
		t.Fatalf("lint: parse %s: %v", testFile, err)
	}

	dir := filepath.Dir(testFile)
	pkgDecls := pkgNonTestDecls[dir]

	// Check for os and testing imports, accounting for aliases.
	var osAlias, testingAlias string
	var osImported, testingImported bool
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		switch path {
		case "os":
			osImported = true
			if imp.Name == nil {
				osAlias = "os"
			} else if imp.Name.Name != "_" {
				osAlias = imp.Name.Name
			}
		case "testing":
			testingImported = true
			if imp.Name == nil {
				testingAlias = "testing"
			} else if imp.Name.Name != "_" {
				testingAlias = imp.Name.Name
			}
		}
	}

	var violations []string
	report := func(pos token.Pos, msg string) {
		p := fset.Position(pos)
		rel, err := filepath.Rel(root, p.Filename)
		if err != nil {
			rel = p.Filename
		}
		violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(p.Line)+" "+msg)
	}

	// checkScoped walks n for violations, given testingVars - the names the
	// enclosing function scope(s) bound to a *testing.T/*testing.B/testing.TB
	// parameter. t.Setenv and t.Chdir are only ever a call through such an
	// identifier (there is no package-level testing.Setenv or testing.Chdir
	// to call qualified by testingAlias the way os.Setenv is by osAlias), so
	// telling a real call apart from an unrelated identifier that merely
	// shares its name needs the handle a function actually received, not
	// its conventional name. A nested func literal - a t.Run subtest, a
	// table-driven test's closure - gets its own scope, built from a copy of
	// the enclosing one so it still sees a testing handle it captures rather
	// than redeclares, with its own params added or, for a same-named
	// non-handle param, shadowing the outer one.
	var checkScoped func(n ast.Node, testingVars map[string]bool)
	checkScoped = func(n ast.Node, testingVars map[string]bool) {
		ast.Inspect(n, func(node ast.Node) bool {
			if lit, ok := node.(*ast.FuncLit); ok {
				checkScoped(lit.Body, scopedTestingVars(testingVars, lit.Type.Params, testingAlias, testingImported))
				return false
			}

			if call, ok := node.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					id, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					switch sel.Sel.Name {
					case "Setenv":
						if testingVars[id.Name] {
							report(call.Pos(), "calls t.Setenv")
						} else if osImported && id.Name == osAlias {
							report(call.Pos(), "calls os.Setenv")
						}
					case "Unsetenv":
						if osImported && id.Name == osAlias {
							report(call.Pos(), "calls os.Unsetenv")
						}
					case "Chdir":
						if testingVars[id.Name] {
							report(call.Pos(), "calls t.Chdir")
						} else if osImported && id.Name == osAlias {
							report(call.Pos(), "calls os.Chdir")
						}
					}
				}
				return true
			}

			// Check for assignments to package-level variables. Tok must be
			// token.ASSIGN: a := short declaration is also an *ast.AssignStmt,
			// but it declares a new local, not an assignment to anything
			// package-level, even when the local's name happens to collide.
			if assign, ok := node.(*ast.AssignStmt); ok && assign.Tok == token.ASSIGN {
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" && pkgDecls[id.Name] {
						report(assign.Pos(), "assigns to package-level "+id.Name)
					}
				}
			}

			return true
		})
	}

	// Walk every top-level declaration other than TestMain, which may set
	// the process up once before any test runs. A func decl can't nest, so
	// its own testing-handle params seed checkScoped's scope once, here,
	// rather than inside checkScoped itself; any other decl (a var or const,
	// possibly holding a func literal of its own) starts from an empty scope.
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			if fd.Name.Name == "TestMain" {
				continue
			}
			checkScoped(fd.Body, scopedTestingVars(nil, fd.Type.Params, testingAlias, testingImported))
			continue
		}
		checkScoped(decl, nil)
	}

	return violations
}

// scopedTestingVars returns a copy of outer with params's own
// testing-handle names (*testing.T, *testing.B or testing.TB, qualified by
// testingAlias) added, and any other param of the same name removed, so a
// param that shadows an outer testing handle with something else stops
// looking like one inside this scope.
func scopedTestingVars(outer map[string]bool, params *ast.FieldList, testingAlias string, testingImported bool) map[string]bool {
	vars := make(map[string]bool, len(outer))
	for k, v := range outer {
		vars[k] = v
	}
	if params == nil {
		return vars
	}
	for _, field := range params.List {
		isHandle := testingImported && isTestingHandleType(field.Type, testingAlias)
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			if isHandle {
				vars[name.Name] = true
			} else {
				delete(vars, name.Name)
			}
		}
	}
	return vars
}

// isTestingHandleType reports whether typ is *testing.T, *testing.B or
// testing.TB, qualified by testingAlias.
func isTestingHandleType(typ ast.Expr, testingAlias string) bool {
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	sel, ok := typ.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != testingAlias {
		return false
	}
	switch sel.Sel.Name {
	case "T", "B", "TB":
		return true
	}
	return false
}
