package lint

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// goGitModule is the import path prefix of go-git, the library gitx runs
// git with in process.
const goGitModule = "github.com/go-git/"

// TestNoGoGitOutsideGitx keeps gitx the single owner of git execution in
// process too: it parses the imports of every .go file outside
// internal/gitx/ (testdata included, .git and vendor skipped) and fails,
// naming file and import, on any import of go-git. Whether git runs as a
// program or as a library is gitx's decision alone (see gitx.Repo).
func TestNoGoGitOutsideGitx(t *testing.T) {
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
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if strings.HasPrefix(relSlash, gitxDir+"/") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err == nil && strings.HasPrefix(p, goGitModule) {
				violations = append(violations, relSlash+": imports "+p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("lint: walk %s: %v", root, err)
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s; only internal/gitx runs git, as a program or in process", v)
	}
}
