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

// The identity the store used to author its own commits with, written as
// the two halves of each literal so this file does not hold it either.
const (
	artificialEmail = "jig" + "@invalid"
	artificialName  = "user.name=" + "jig"
)

// TestNoArtificialStoreIdentity keeps every store commit authored by the
// person at the keyboard: it parses every .go file in the repo (tests and
// testdata included, .git and vendor skipped) and fails, naming file:line,
// on a string literal that holds the address jig once made up for itself or
// that sets git's user.name to "jig" on the command line. A commit the store
// makes takes its author from git (gitx.ResolveIdentity), and a test that
// needs a committer names a person of its own.
func TestNoArtificialStoreIdentity(t *testing.T) {
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if strings.Contains(v, artificialEmail) || v == artificialName {
				violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(fset.Position(lit.Pos()).Line))
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
		t.Errorf("%s: names jig's own made-up commit identity; commit as the identity git resolves (gitx.ResolveIdentity), or give a test a person of its own", v)
	}
}
