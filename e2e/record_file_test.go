package e2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
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
	}
	if tags != 2 {
		t.Errorf("%d tags, want 2", tags)
	}
}

// TestRecordBoundIsWellAboveTheSuite: the harness records every scenario, so
// its safety bound must stay well above the number of tests the suite has
// (each makes a few recordings at most).
func TestRecordBoundIsWellAboveTheSuite(t *testing.T) {
	t.Parallel()
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var tests int
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
					tests++
				}
			}
		}
	}
	if maxRecordings < 3*tests {
		t.Errorf("maxRecordings = %d, want well above the %d tests of the suite", maxRecordings, tests)
	}
}
