// Command gitbudget reports the git processes each test package starts.
//
// CI's Linux leg runs every test binary through it (go test -exec), and it
// points git's own trace, GIT_TRACE2_EVENT, at a directory of the package's
// own. git then writes one file there for every git process the package's
// tests start, in the test binary and in anything the binary runs (the jig
// binary e2e builds, a git that starts another git). A step after go test
// shows those counts beside the baseline main's last run measured, so a
// change that adds git work is seen in its pull request's job summary. A
// count never fails anything.
//
// Usage:
//
//	gitbudget <test binary> [args...]
//	gitbudget report <baseline or -> <count dir> <package>...
//	gitbudget write <baseline file> <count dir> <package>...
//
// The first form runs the test binary, counting into $GITBUDGET_DIR when it
// is set; go test runs it in the package's directory, which names the
// package. report shows the named packages' counts beside the baseline (a
// yaml packages map; "-" or a missing file means none, and the report says
// so) with each change and percent, and exits 0 unless a file it must read
// is unreadable or a package is not in the module. It prints a table and
// appends it as Markdown to $GITHUB_STEP_SUMMARY. write records the counts
// as the baseline. Packages are import paths, as go list prints them.
//
// Each trace file also says who started its git process, when the process
// ran under a git config that lists trace2.envVars = JIG_GIT_CALLER,
// JIG_GIT_DIR (internal/gittest writes it for test binaries; internal/gitx
// sets both variables while git traces): the Go function that called gitx,
// and the directory it ran in, from which the test is derived. report reads
// only the head of each file and, after the table, prints and summarizes the
// top callers across the packages and each package's top callers and tests,
// and writes every count to callers.tsv in the count directory.
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
		fmt.Fprintln(stderr, "usage: gitbudget <test binary> [args...] | gitbudget report <baseline or -> <count dir> <package>... | gitbudget write <baseline file> <count dir> <package>...")
		return 2
	}
	switch args[0] {
	case "report", "write":
		return reportOrWrite(args[0], args[1:], getenv, stdout, stderr)
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

// reportOrWrite is the report and write commands; getenv names where the job
// summary goes ($GITHUB_STEP_SUMMARY).
func reportOrWrite(cmd string, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	// write <baseline file> <count dir> <package>...
	// report <baseline or -> <count dir> <package>...
	var file, countDir, base string
	var packages []string
	if len(args) < 3 {
		fmt.Fprintln(stderr, "usage: gitbudget report <baseline or -> <count dir> <package>... | gitbudget write <baseline file> <count dir> <package>...")
		return 2
	}
	file, countDir, packages = args[0], args[1], args[2:]
	if cmd == "report" {
		base = file
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
	var b *baseline
	if base != "-" {
		if b, err = readBaseline(base); err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
			return 1
		}
	}
	var plain, md string
	if b == nil {
		plain, md = formatCounts(counts, false), formatCounts(counts, true)
	} else {
		rows := compare(b.Packages, counts)
		plain, md = formatRows(rows), markdownRows(rows)
	}
	// Who started the processes is reported like the counts: nothing here
	// changes the exit code, and a failure to read or write it is a warning.
	as := attributions{}
	for rel := range counts {
		a, err := attribute(filepath.Join(countDir, countDirName(rel)))
		if err != nil {
			fmt.Fprintf(stderr, "gitbudget: attribution of %s: %v\n", rel, err)
			continue
		}
		if a.total > 0 {
			as[rel] = a
		}
	}
	plain += formatAttribution(as, false)
	md += formatAttribution(as, true)
	if len(as) > 0 {
		if err := writeCallers(countDir, as); err != nil {
			fmt.Fprintf(stderr, "gitbudget: %v\n", err)
		}
	}
	fmt.Fprint(stdout, plain)
	if path := getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendFile(path, md); err != nil {
			fmt.Fprintf(stderr, "gitbudget: summary: %v\n", err)
		}
	}
	return 0
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
