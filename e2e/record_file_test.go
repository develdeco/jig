package e2e

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecordTagNamesTheTestsSourceFile: each recording's tag names the file
// the running test is in, repo-relative with forward slashes, whether the call
// is made in the test itself, in a subtest, or through a helper it calls.
func TestRecordTagNamesTheTestsSourceFile(t *testing.T) {
	t.Parallel()
	const want = "e2e/record_file_test.go"
	record := func(t *testing.T, rs *recordings) {
		t.Helper()
		env := jigEnv{home: t.TempDir()}
		rs.run(&fakeT{name: t.Name()}, recordCall{env: env, cwd: env.home, args: []string{"status"}}, io.Discard, io.Discard, func(out, errs io.Writer) int { return 0 })
	}
	dir := t.TempDir()
	rs := newRecordings(dir)
	record(t, rs)
	t.Run("a subtest", func(t *testing.T) { record(t, rs) })

	var tags int
	for _, name := range recordedFiles(t, dir) {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		tags++
		if got := readTagFile(t, filepath.Join(dir, name)).File; got != want {
			t.Errorf("tag %s file = %q, want %q", name, got, want)
		}
		// jig reads the tag by its wire name, which the harness struct cannot pin.
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil || fields["file"] != want {
			t.Errorf("tag %s = %s, want a \"file\" key of %q", name, raw, want)
		}
	}
	if tags != 2 {
		t.Errorf("%d tags, want 2", tags)
	}
}

// TestRecordBoundIsWellAboveTheSuite: the harness records every jig call, so
// its safety bound must stay well above what the suite can record. The floor
// is read from the suite's source: the call sites of runJig and runJigIn and
// of every helper that reaches them (by name, to a fixed point), times 3 for
// the loops and subtests that run a site more than once. A bound the suite
// reaches stops recording in finish order, which is the bug this ticket fixes.
func TestRecordBoundIsWellAboveTheSuite(t *testing.T) {
	t.Parallel()
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	// Only plain calls: a method of the same name elsewhere would inflate the
	// count.
	calleeName := func(c *ast.CallExpr) string {
		if id, ok := c.Fun.(*ast.Ident); ok {
			return id.Name
		}
		return ""
	}
	recorded := map[string]bool{"runJig": true, "runJigIn": true}
	for grew := true; grew; {
		grew = false
		for _, pkg := range pkgs {
			for _, f := range pkg.Files {
				for _, d := range f.Decls {
					fn, ok := d.(*ast.FuncDecl)
					if !ok || fn.Body == nil || recorded[fn.Name.Name] {
						continue
					}
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						if c, ok := n.(*ast.CallExpr); ok && recorded[calleeName(c)] && !recorded[fn.Name.Name] {
							recorded[fn.Name.Name] = true
							grew = true
						}
						return true
					})
				}
			}
		}
	}
	var sites int
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok && recorded[calleeName(c)] {
					sites++
				}
				return true
			})
		}
	}
	if sites < 100 {
		t.Fatalf("found %d recorded call sites; the scan no longer sees the suite", sites)
	}
	if maxRecordings < 3*sites {
		t.Errorf("maxRecordings = %d, want at least 3 x the %d recorded call sites of the suite", maxRecordings, sites)
	}
}
