// Package graphify integrates jig with an optional external "graphify"
// knowledge-graph tool (PyPI graphifyy): when a project opts in and the
// graphify binary is on PATH, Plane shells out to it to keep a lease's code
// graph current and to find the code linked to a slice's goal; otherwise
// jig runs without it (ADR 0026).
package graphify

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/project"
)

// defaultDepth is graphify's own default BFS depth for "affected"; jig
// passes it explicitly rather than relying on the CLI default.
const defaultDepth = "2"

// OutDir is the directory graphify writes in the repo it runs in. It must
// never be committed: frontier excludes it in each lease it graphs.
const OutDir = "graphify-out"

// queryBudget caps a query's output, in graphify's tokens.
const queryBudget = "1500"

// updateLimit and queryLimit bound one graphify run. On jig's own repo
// (about 400 files) an update takes 25 to 36 s and a query about 4 s on the
// Windows dev machine.
const (
	updateLimit = 5 * time.Minute
	queryLimit  = time.Minute
)

// Node is one code-graph node a query returned: its label, and where it is.
type Node struct {
	Label string
	File  string // repo-relative, forward slashes
	Line  int    // 0 when graphify gives none
}

// Plane is jig's code-graph surface.
type Plane interface {
	// Enabled reports whether this plane can actually answer queries.
	Enabled() bool
	// Affected returns the labels of nodes affected by seed, per the
	// "graphify affected" shell-out contract.
	Affected(seed string, repoDir string) ([]string, error)
	// Update brings repoDir's graph up to date with its files: code only,
	// no LLM.
	Update(repoDir string) error
	// Query returns the nodes repoDir's graph links to question, in
	// graphify's order, the most directly matched first.
	Query(repoDir, question string) ([]Node, error)
}

// Noop is the fallback plane used whenever graphify is not opted into or
// not available: it answers every query with nothing.
type Noop struct{}

// Enabled always reports false for Noop.
func (Noop) Enabled() bool { return false }

// Affected always returns no nodes and no error for Noop.
func (Noop) Affected(seed string, repoDir string) ([]string, error) { return nil, nil }

// Update does nothing for Noop.
func (Noop) Update(repoDir string) error { return nil }

// Query always returns no nodes and no error for Noop.
func (Noop) Query(repoDir, question string) ([]Node, error) { return nil, nil }

// Detect returns the cli plane when the project has opted in (cfg.Context
// carries a "graphify" key) and the graphify binary is found on PATH;
// otherwise it returns Noop.
func Detect(cfg project.Config) Plane {
	return DetectWith(cfg, exec.LookPath)
}

// DetectWith is Detect with the binary lookup supplied, so a test never
// edits PATH.
func DetectWith(cfg project.Config, lookPath func(string) (string, error)) Plane {
	if cfg.Context == nil {
		return Noop{}
	}
	if _, ok := cfg.Context["graphify"]; !ok {
		return Noop{}
	}
	bin, err := lookPath("graphify")
	if err != nil {
		return Noop{}
	}
	return cliPlane{bin: bin}
}

// cliPlane shells out to the graphify binary.
type cliPlane struct{ bin string }

// Enabled always reports true for cliPlane (Detect only returns it when
// graphify is actually available).
func (cliPlane) Enabled() bool { return true }

// run runs the graphify binary with args in repoDir within limit, killing
// its whole process tree past it (the binary may be a launcher for a
// Python process), and returns its stdout.
func (p cliPlane) run(repoDir string, limit time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.bin, args...)
	cmd.Dir = repoDir
	envrun.NewProcessGroup(cmd)
	cmd.Cancel = func() error { return envrun.KillTree(cmd) }
	cmd.WaitDelay = 10 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("graphify %s: did not finish within %s", args[0], limit)
	}
	if err != nil {
		// graphify prints a failure's cause on stdout and only a pointer to
		// it on stderr, so the reason carries both.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if out := strings.TrimSpace(stdout.String()); out != "" {
			if len(out) > 1500 {
				out = out[len(out)-1500:]
			}
			msg += "\n" + out
		}
		return "", fmt.Errorf("graphify %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

// Affected runs `graphify affected <seed> --graph <path> --depth N` in
// repoDir and parses its "- <label> ..." lines into a slice of labels.
// Both "No unique node match" and "No affected nodes found" are normal,
// zero-result outcomes (graphify exits 0 for them), not errors.
func (p cliPlane) Affected(seed string, repoDir string) ([]string, error) {
	graphPath := filepath.Join(repoDir, OutDir, "graph.json")
	out, err := p.run(repoDir, queryLimit, "affected", seed, "--graph", graphPath, "--depth", defaultDepth)
	if err != nil {
		return nil, err
	}
	return parseAffected(out), nil
}

// Update runs `graphify update .` in repoDir: an incremental, code-only
// rebuild of OutDir/graph.json that re-extracts changed files.
func (p cliPlane) Update(repoDir string) error {
	_, err := p.run(repoDir, updateLimit, "update", ".")
	return err
}

// Query runs `graphify query <question>` in repoDir against its graph,
// capped at queryBudget, and parses the nodes it prints.
func (p cliPlane) Query(repoDir, question string) ([]Node, error) {
	graphPath := filepath.Join(repoDir, OutDir, "graph.json")
	out, err := p.run(repoDir, queryLimit, "query", question, "--budget", queryBudget, "--graph", graphPath)
	if err != nil {
		return nil, err
	}
	return parseQuery(out), nil
}

// parseAffected extracts the label from each "- <label> [...] file:loc"
// line of graphify's plain-text "affected" output. Lines that are not hit
// lines (headers, "No unique node match ...", "No affected nodes found.")
// are ignored.
func parseAffected(output string) []string {
	var labels []string
	sc := bufio.NewScanner(strings.NewReader(output))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		// The label runs up to the next " [" (via_relation bracket); if
		// that marker is absent, fall back to the whole remainder.
		if i := strings.Index(rest, " ["); i >= 0 {
			labels = append(labels, rest[:i])
		} else {
			labels = append(labels, strings.TrimSpace(rest))
		}
	}
	return labels
}

// parseQuery extracts the nodes from graphify query's
// "NODE <label> [src=<path> loc=L<n> community=<c>]" lines, in order. A
// node with no source file (a package or library node) is skipped, and so
// is every other line (the header, the truncation notice, edges).
func parseQuery(output string) []Node {
	var nodes []Node
	sc := bufio.NewScanner(strings.NewReader(output))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "NODE ") {
			continue
		}
		rest := strings.TrimPrefix(line, "NODE ")
		i := strings.LastIndex(rest, " [src=")
		if i < 0 || !strings.HasSuffix(rest, "]") {
			continue
		}
		// The source path runs from "src=" to " loc=", so a path with a
		// space survives; graphify may print it with backslashes on any OS.
		attrs := rest[i+len(" [src=") : len(rest)-1]
		j := strings.Index(attrs, " loc=")
		if j < 0 {
			continue
		}
		n := Node{Label: rest[:i], File: strings.ReplaceAll(attrs[:j], `\`, "/")}
		loc := attrs[j+len(" loc="):]
		if k := strings.IndexByte(loc, ' '); k >= 0 {
			loc = loc[:k]
		}
		n.Line, _ = strconv.Atoi(strings.TrimPrefix(loc, "L"))
		if n.File == "" {
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes
}
