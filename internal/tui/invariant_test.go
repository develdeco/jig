package tui

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

// TestTheScreensReadNoStore pins how the package is built: its code reaches a
// store only through a verb it is handed (Runner) and the stream it is handed
// (Source). It imports no package that opens a store, a repo or a file, and
// calls nothing that reads the disk.
func TestTheScreensReadNoStore(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"github.com/develdeco/jig/internal/store",
		"github.com/develdeco/jig/internal/project",
		"github.com/develdeco/jig/internal/gitx",
		"github.com/develdeco/jig/internal/verifydeliver",
		"github.com/develdeco/jig/internal/frontier",
		"github.com/develdeco/jig/internal/ledger",
		"github.com/develdeco/jig/internal/home",
		"os/exec",
		"io/ioutil",
	}
	reads := map[string]bool{"ReadFile": true, "Open": true, "OpenFile": true, "ReadDir": true, "Stat": true, "Lstat": true, "Getwd": true}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if path == bad {
					t.Errorf("%s imports %s: the screens read a store only through a verb or the stream", name, path)
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && reads[sel.Sel.Name] {
				t.Errorf("%s calls os.%s: the screens read no file", name, sel.Sel.Name)
			}
			return true
		})
	}
	if checked < 8 {
		t.Fatalf("checked %d files, want the package's whole code", checked)
	}
	if _, err := os.Stat("model.go"); err != nil {
		t.Fatal(err)
	}
}
