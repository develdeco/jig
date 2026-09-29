// Command fixture builds the jig fixture - a fixture repo, its truth-repo
// store, and a scripted attempt/patch scenario - into a directory, so a VHS
// tape (or any other out-of-process caller) can drive the real `jig` binary
// against it without hand-rolling a git repo of its own.
//
// It never touches the caller's real home directory: JIG_HOME is read from
// the environment when already set, and otherwise defaults to <out>/home, so
// a tape that never sets JIG_HOME itself still builds a fixture rooted
// entirely under -out. Either way the resolved value is handed to
// fixture.Build and exported in shell form on stdout, so a tape's own env
// never leaks a host path: it evals this command's output instead of naming
// one itself.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/fixture"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run implements the command: parse flags, resolve JIG_HOME, build the
// fixture, and print the shell export lines a tape evals. It takes stdout
// and stderr explicitly (never os.Stdout/os.Stderr directly) so the test
// below exercises this exact code path with buffers instead of the real
// process streams.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fixture", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "directory to build the fixture into (required)")
	scenario := fs.String("scenario", "reviewer", "scenario-branches overlay to apply")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" {
		fmt.Fprintln(stderr, "fixture: -out is required")
		return 2
	}

	jigHome := os.Getenv("JIG_HOME")
	if jigHome == "" {
		absOut, err := filepath.Abs(*out)
		if err != nil {
			fmt.Fprintf(stderr, "fixture: %v\n", err)
			return 1
		}
		jigHome = filepath.Join(absOut, "home")
	}
	fx, err := fixture.Build(*out, fixture.Opts{ScenarioBranch: *scenario, Home: jigHome})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	printExports(stdout, jigHome, fx)
	return 0
}

// printExports writes the shell `export` lines a tape evals: JIG_HOME (the
// value run resolved, whether inherited or defaulted), the store dir, the
// scenario dir, the fixture repo's own working clone, and the ticket id.
// -out is caller-supplied and may contain spaces or shell metacharacters
// (an eval'd unquoted export truncates at the first one), so every value
// is single-quoted with shellQuote before it is printed, and a plain
// `eval "$(...)"` is still enough to load them back.
func printExports(stdout io.Writer, jigHome string, fx *fixture.Fixture) {
	fmt.Fprintf(stdout, "export JIG_HOME=%s\n", shellQuote(jigHome))
	fmt.Fprintf(stdout, "export JIG_STORE_DIR=%s\n", shellQuote(fx.StoreDir))
	fmt.Fprintf(stdout, "export JIG_SCENARIO_DIR=%s\n", shellQuote(fx.ScenarioDir))
	fmt.Fprintf(stdout, "export JIG_REPO_DIR=%s\n", shellQuote(fx.RepoDir))
	fmt.Fprintf(stdout, "export JIG_TICKET=%s\n", shellQuote(fx.Ticket))
}

// shellQuote renders s as a single-quoted POSIX shell word: every single
// quote in s closes the current quote, contributes an escaped quote, and
// reopens a new one, so `eval` reads back exactly the bytes of s regardless
// of spaces or shell metacharacters.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
