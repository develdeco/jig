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

// gitxDir is the one directory allowed to spawn git directly: it is git
// execution's single owner (see the gitx package doc comment).
const gitxDir = "internal/gitx"

// gitProgram reports whether s is a program name that spawns git: the bare
// command or, as Windows' PATH lookup can resolve it, the .exe form.
func gitProgram(s string) bool {
	return s == "git" || s == "git.exe"
}

// TestNoGitSpawnOutsideGitx keeps gitx the single owner of git execution. It
// parses every .go file outside internal/gitx/ (testdata included, .git and vendor
// skipped) and fails, naming file:line, on an os/exec Command, CommandContext
// or LookPath call, or an exec.Cmd literal, whose program is git or git.exe:
// written literally, through a const or var, from a LookPath result, or with
// os/exec imported under another name.
func TestNoGitSpawnOutsideGitx(t *testing.T) {
	root := repoRoot(t)

	// Group files by directory: a package's const or var may bind a git
	// literal in one file and be used as a program argument in another.
	dirFiles := map[string][]string{}
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
		if relSlash == gitxDir || strings.HasPrefix(relSlash, gitxDir+"/") {
			return nil
		}
		dir := filepath.Dir(path)
		dirFiles[dir] = append(dirFiles[dir], path)
		return nil
	})
	if err != nil {
		t.Fatalf("lint: walk %s: %v", root, err)
	}

	var violations []string
	for _, files := range dirFiles {
		violations = append(violations, checkPackageForGitSpawn(t, root, files)...)
	}
	sort.Strings(violations)

	for _, v := range violations {
		t.Errorf("%s: spawns git directly; route it through gitx.Run/gitx.RunEnv instead", v)
	}
}

// checkPackageForGitSpawn parses every file in files (all files of one
// directory) and returns "path:line" for each git-spawning construct found.
func checkPackageForGitSpawn(t *testing.T, root string, files []string) []string {
	t.Helper()

	fset := token.NewFileSet()
	parsed := make([]*ast.File, 0, len(files))
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("lint: parse %s: %v", path, err)
		}
		parsed = append(parsed, f)
	}

	// Package-scope names (const or var, at file top level) bound to a
	// single string literal that names git.
	gitNames := map[string]bool{}
	for _, f := range parsed {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			collectGitLiteralNames(gd, gitNames, gitNames)
		}
	}

	var violations []string
	for _, f := range parsed {
		violations = append(violations, checkFileForGitSpawn(fset, root, f, gitNames)...)
	}
	return violations
}

// collectGitLiteralNames records, into names, every name in gd (a const or
// var GenDecl) whose single value is a string literal equal to "git" or
// "git.exe". known is consulted so a name initialized from another known
// git name (rare, but cheap to support) is also recorded.
func collectGitLiteralNames(gd *ast.GenDecl, names, known map[string]bool) {
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
			continue
		}
		if litIsGit(vs.Values[0], known) {
			names[vs.Names[0].Name] = true
		}
	}
}

// litIsGit reports whether expr is a string literal naming git, or an
// identifier already known (via names) to hold one.
func litIsGit(expr ast.Expr, names map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return false
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return false
		}
		return gitProgram(v)
	case *ast.Ident:
		return names[e.Name]
	}
	return false
}

// checkFileForGitSpawn walks one parsed file and returns "path:line" for
// every git-spawning construct it finds, resolving program arguments
// against pkgGitNames (package-scope) and names assigned locally within
// the file (function-scope consts/vars, and variables assigned from
// exec.LookPath("git")).
func checkFileForGitSpawn(fset *token.FileSet, root string, f *ast.File, pkgGitNames map[string]bool) []string {
	execAlias, dot, imported := execImportName(f)
	if !imported {
		return nil
	}

	// isExecSel reports whether sel is a reference to os/exec.<name> under
	// this file's import (accounting for an alias or a dot import).
	isExecSel := func(fun ast.Expr, name string) bool {
		if dot {
			id, ok := fun.(*ast.Ident)
			return ok && id.Name == name
		}
		sel, ok := fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != name {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == execAlias
	}

	// localGitNames accumulates function-scope const/var names and
	// LookPath("git") results bound to a git program name, in source
	// order. It is not scope- or shadowing-aware; that is a fine tradeoff
	// for a lint that only ever adds names, never removes them.
	localGitNames := map[string]bool{}
	resolves := func(expr ast.Expr) bool {
		if litIsGit(expr, pkgGitNames) {
			return true
		}
		return litIsGit(expr, localGitNames)
	}

	var violations []string
	report := func(pos token.Pos) {
		p := fset.Position(pos)
		rel, err := filepath.Rel(root, p.Filename)
		if err != nil {
			rel = p.Filename
		}
		violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(p.Line))
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GenDecl:
			if node.Tok == token.CONST || node.Tok == token.VAR {
				collectGitLiteralNames(node, localGitNames, localGitNames)
			}

		case *ast.AssignStmt:
			for i, rhs := range node.Rhs {
				if i >= len(node.Lhs) {
					break
				}
				lhs, ok := node.Lhs[i].(*ast.Ident)
				if !ok || lhs.Name == "_" {
					continue
				}
				if litIsGit(rhs, pkgGitNames) || litIsGit(rhs, localGitNames) {
					localGitNames[lhs.Name] = true
					continue
				}
				if call, ok := rhs.(*ast.CallExpr); ok && isExecSel(call.Fun, "LookPath") && len(call.Args) == 1 && resolves(call.Args[0]) {
					localGitNames[lhs.Name] = true
				}
			}

		case *ast.CallExpr:
			switch {
			case isExecSel(node.Fun, "Command") && len(node.Args) >= 1:
				if resolves(node.Args[0]) {
					report(node.Pos())
				}
			case isExecSel(node.Fun, "CommandContext") && len(node.Args) >= 2:
				if resolves(node.Args[1]) {
					report(node.Pos())
				}
			}

		case *ast.CompositeLit:
			var isCmdType bool
			switch typ := node.Type.(type) {
			case *ast.SelectorExpr:
				if id, ok := typ.X.(*ast.Ident); ok && id.Name == execAlias && typ.Sel.Name == "Cmd" {
					isCmdType = true
				}
			case *ast.Ident:
				if dot && typ.Name == "Cmd" {
					isCmdType = true
				}
			}
			if !isCmdType {
				break
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "Path" {
					continue
				}
				if resolves(kv.Value) {
					report(node.Pos())
				}
			}
		}
		return true
	})
	return violations
}

// execImportName reports the local name this file uses for the os/exec
// package (its import alias, or "exec" if unaliased), whether that import
// is a dot import, and whether the file imports os/exec at all. A blank
// import ("_") cannot be referenced, so it is treated as not imported.
func execImportName(f *ast.File) (name string, dot bool, imported bool) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "os/exec" {
			continue
		}
		switch {
		case imp.Name == nil:
			return "exec", false, true
		case imp.Name.Name == "_":
			return "", false, false
		case imp.Name.Name == ".":
			return "", true, true
		default:
			return imp.Name.Name, false, true
		}
	}
	return "", false, false
}
