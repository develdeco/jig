package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// rewriters are the gitx functions that rewrite commits in a repository: for
// a repository a test built, whose files carry a directory it was not built
// in. They are not a way to edit history, and no product code may call them.
var rewriters = map[string]bool{"RewriteHeadCommit": true, "RewriteHistory": true, "RewriteHistoryFunc": true}

// TestOnlyTestsAndTheFixtureCallTheCommitRewriters parses every .go file
// outside internal/gitx and fails, naming file and call, when a file that is
// not a _test.go file and is not in internal/fixture refers to one of the
// rewriters, so a test-only tool is not taken for a general one.
func TestOnlyTestsAndTheFixtureCallTheCommitRewriters(t *testing.T) {
	root := repoRoot(t)
	var violations []string
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if strings.HasPrefix(relSlash, gitxDir+"/") || strings.HasPrefix(relSlash, "internal/fixture/") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !rewriters[sel.Sel.Name] {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "gitx" {
				violations = append(violations, relSlash+": calls gitx."+sel.Sel.Name)
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
		t.Errorf("%s; the commit rewriters are for tests and internal/fixture only", v)
	}
}
