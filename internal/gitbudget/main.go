// Command gitbudget holds each test package to a budget of git processes.
//
// CI's Linux leg runs every test binary through it (go test -exec), and it
// points git's own trace, GIT_TRACE2_EVENT, at a directory of the package's
// own. git then writes one file there for every git process the package's
// tests start, in the test binary and in anything the binary runs (the jig
// binary e2e builds, a git that starts another git). A step after go test
// compares those counts with .github/git-budget.yaml, so a change that adds
// git work to the suite says so in its own diff, and a cut records its
// lower count.
//
// Usage:
//
//	gitbudget <test binary> [args...]
//	gitbudget check <budget file> <count dir> <package>...
//	gitbudget write <budget file> <count dir> <package>...
//
// The first form runs the test binary, counting into $GITBUDGET_DIR when it
// is set; go test runs it in the package's directory, which names the
// package. check compares the named packages' counts with their budgets and
// fails on a count outside the margin either way; write sets their budgets
// to their counts. Packages are import paths, as go list prints them.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main with its arguments and output passed in.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: gitbudget <test binary> [args...] | gitbudget check|write <budget file> <count dir> <package>...")
		return 2
	}
	switch args[0] {
	case "check", "write":
		if len(args) < 3 {
			fmt.Fprintf(stderr, "usage: gitbudget %s <budget file> <count dir> <package>...\n", args[0])
			return 2
		}
		return checkOrWrite(args[0], args[1], args[2], args[3:], stdout, stderr)
	}
	dir := os.Getenv("GITBUDGET_DIR")
	env := os.Environ()
	if dir != "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
			return 1
		}
		_, rel, err := moduleOf(cwd)
		if err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
			return 1
		}
		traces := filepath.Join(dir, countDirName(rel))
		if err := os.MkdirAll(traces, 0o755); err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
			return 1
		}
		env = append(env, "GIT_TRACE2_EVENT="+traces)
	}
	return execTest(args[0], args, env, stderr)
}

// checkOrWrite is the check and write commands.
func checkOrWrite(cmd, budgetFile, countDir string, packages []string, stdout, stderr io.Writer) int {
	b, err := readBudget(budgetFile)
	if err != nil {
		fmt.Fprintf(stderr, "gitbudget: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "gitbudget: %v\n", err)
		return 1
	}
	module, _, err := moduleOf(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "gitbudget: %v\n", err)
		return 1
	}
	counts := map[string]int{}
	for _, pkg := range packages {
		rel, ok := relPackage(module, pkg)
		if !ok {
			fmt.Fprintf(stderr, "gitbudget: %s is not a package of module %s\n", pkg, module)
			return 1
		}
		n, err := countTraces(filepath.Join(countDir, countDirName(rel)))
		if err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
			return 1
		}
		counts[rel] = n
	}
	if cmd == "write" {
		b.set(counts)
		if err := os.WriteFile(budgetFile, b.encode(), 0o644); err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "gitbudget: wrote %d packages' counts to %s\n", len(counts), budgetFile)
		return 0
	}
	rows, ok := b.check(counts)
	fmt.Fprint(stdout, formatRows(rows, b.Margin))
	if ok {
		return 0
	}
	b.set(counts)
	fmt.Fprintf(stdout, "\nA count is outside its budget's margin. If the change means it, put this in %s:\n\n%s", budgetFile, b.encode())
	return 1
}

// moduleOf returns the module path of the go.mod in dir or the nearest
// directory above it, and dir's path relative to that directory, with
// forward slashes ("." for the module root).
func moduleOf(dir string) (module, rel string, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	for root := abs; ; {
		data, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if f := strings.Fields(line); len(f) == 2 && f[0] == "module" {
					r, err := filepath.Rel(root, abs)
					if err != nil {
						return "", "", err
					}
					return f[1], filepath.ToSlash(r), nil
				}
			}
			return "", "", fmt.Errorf("%s names no module", filepath.Join(root, "go.mod"))
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", "", fmt.Errorf("no go.mod in %s or above it", abs)
		}
		root = parent
	}
}

// relPackage returns pkg's path relative to module, and whether pkg is in
// module at all.
func relPackage(module, pkg string) (string, bool) {
	if pkg == module {
		return ".", true
	}
	rel, ok := strings.CutPrefix(pkg, module+"/")
	return rel, ok && rel != ""
}

// countDirName is the directory a package's traces go to: its path with
// each slash spelled %2F, a name every OS accepts.
func countDirName(rel string) string {
	return strings.ReplaceAll(rel, "/", "%2F")
}

// countTraces returns the number of trace files in dir, one per git
// process; a directory that was never made holds none.
func countTraces(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}
