// Command gitbudget holds each test package to a budget of git processes.
//
// CI's Linux leg runs every test binary through it (go test -exec), and it
// points git's own trace, GIT_TRACE2_EVENT, at a directory of the package's
// own. git then writes one file there for every git process the package's
// tests start, in the test binary and in anything the binary runs (the jig
// binary e2e builds, a git that starts another git). A step after go test
// compares those counts with the baseline main's last run measured, so a
// change that adds git work beyond the threshold fails its pull request
// until a person accepts it, and nothing is committed to a budget.
//
// Usage:
//
//	gitbudget <test binary> [args...]
//	gitbudget check [-accepted] <config> <baseline or -> <count dir> <package>...
//	gitbudget write <baseline file> <count dir> <package>...
//
// The first form runs the test binary, counting into $GITBUDGET_DIR when it
// is set; go test runs it in the package's directory, which names the
// package. check compares the named packages' counts with the baseline (a
// yaml packages map; "-" or a missing file means none, and the check passes
// saying so) and fails on a count above its baseline plus max(margin
// percent of it, slack processes) from the config; a decrease never
// fails, and -accepted turns the overs into passes. It prints a table and
// appends it as Markdown to $GITHUB_STEP_SUMMARY. write records the counts
// as the baseline. Packages are import paths, as go list prints them.
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
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is main with its arguments, environment and output passed in.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: gitbudget <test binary> [args...] | gitbudget check [-accepted] <config> <baseline or -> <count dir> <package>... | gitbudget write <baseline file> <count dir> <package>...")
		return 2
	}
	switch args[0] {
	case "check", "write":
		return checkOrWrite(args[0], args[1:], getenv, stdout, stderr)
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
		env = setEnv(env, "GIT_TRACE2_EVENT", traces)
	}
	return execTest(args[0], args, env, stderr)
}

// setEnv returns env with name set to value, every earlier entry of name
// dropped: exec passes the list as it is, and git reads the first of two.
func setEnv(env []string, name, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, name) {
			out = append(out, kv)
		}
	}
	return append(out, name+"="+value)
}

// checkOrWrite is the check and write commands; getenv names where the job
// summary goes ($GITHUB_STEP_SUMMARY).
func checkOrWrite(cmd string, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	accepted := false
	if cmd == "check" && len(args) > 0 && args[0] == "-accepted" {
		accepted, args = true, args[1:]
	}
	// write <baseline file> <count dir> <package>...
	// check <config> <baseline or -> <count dir> <package>...
	var file, countDir, base string
	var packages []string
	need := 3
	if cmd == "check" {
		need = 4
	}
	if len(args) < need {
		fmt.Fprintln(stderr, "usage: gitbudget check [-accepted] <config> <baseline or -> <count dir> <package>... | gitbudget write <baseline file> <count dir> <package>...")
		return 2
	}
	file, countDir, packages = args[0], args[1], args[2:]
	if cmd == "check" {
		base, countDir, packages = args[1], args[2], args[3:]
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
		if err := os.WriteFile(file, encodeBaseline(counts), 0o644); err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "gitbudget: wrote %d packages' counts to %s\n", len(counts), file)
		return 0
	}
	c, err := readConfig(file)
	if err != nil {
		fmt.Fprintf(stderr, "gitbudget: %v\n", err)
		return 1
	}
	var b *baseline
	if base != "-" {
		if b, err = readBaseline(base); err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
			return 1
		}
	}
	var plain, md string
	ok := true
	if b == nil {
		plain, md = formatCounts(counts, false), formatCounts(counts, true)
	} else {
		var rows []row
		rows, ok = c.check(b.Packages, counts, accepted)
		plain, md = formatRows(rows, c), markdownRows(rows, c)
	}
	fmt.Fprint(stdout, plain)
	if path := getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendFile(path, md); err != nil {
			fmt.Fprintf(stderr, "gitbudget: summary: %v\n", err)
		}
	}
	if ok {
		return 0
	}
	fmt.Fprintln(stdout, "\nA count is over its baseline plus the allowance. If the change means it, add the git-budget-accepted label to the pull request and re-run this job.")
	return 1
}

// appendFile appends text to the file at path, making it if need be.
func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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
