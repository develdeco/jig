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

// storeConfigFiles are the non-test files that may name a store's config file:
// a v2 store keeps project.yaml, a v3 store store.yaml, and a store is opened
// and read through one door. internal/project is that door (LoadStoreConfig,
// LoadConfig); the others write or move the file: the store's Open names the
// two it accepts, the migration steps rewrite one into the other, the fixture
// writes a v2 store, and the review eval builds a store of its own.
var storeConfigFiles = []string{
	"internal/project/",
	"internal/migrate/",
	"internal/store/store.go",
	"internal/fixture/fixture.go",
	"internal/revieweval/runner.go",
}

// TestStoreConfigIsReadThroughTheLoader keeps a v3 store working everywhere: a
// verb that read "project.yaml" itself would see no store in a v3 one, which has
// a store.yaml, so no non-test code outside storeConfigFiles may hold either file
// name as a string literal. A reader asks project.LoadStoreConfig (the store's
// own settings) or project.LoadConfig (those completed with the project's
// repos from its jig.yaml).
func TestStoreConfigIsReadThroughTheLoader(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "testdata", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, allowed := range storeConfigFiles {
			if rel == allowed || (strings.HasSuffix(allowed, "/") && strings.HasPrefix(rel, allowed)) {
				return nil
			}
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && (v == "project.yaml" || v == "store.yaml") {
				violations = append(violations, rel+":"+strconv.Itoa(fset.Position(lit.Pos()).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("lint: walk %s: %v", root, err)
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s: names a store's config file; read it with project.LoadStoreConfig or project.LoadConfig, which know both layouts", v)
	}
}
