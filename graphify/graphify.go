// Package graphify integrates jig with an optional external "graphify"
// knowledge-graph tool: when a project opts in and the graphify binary is
// on PATH, Plane shells out to it to find code affected by a seed symbol
// or file; otherwise jig runs without it.
package graphify

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/project"
)

// defaultDepth is graphify's own default BFS depth for "affected"; jig
// passes it explicitly rather than relying on the CLI default.
const defaultDepth = "2"

// Plane is jig's graph-affected-set query surface.
type Plane interface {
	// Enabled reports whether this plane can actually answer queries.
	Enabled() bool
	// Affected returns the labels of nodes affected by seed, per the
	// "graphify affected" shell-out contract.
	Affected(seed string, repoDir string) ([]string, error)
}

// Noop is the fallback plane used whenever graphify is not opted into or
// not available: it answers every query with no affected nodes.
type Noop struct{}

// Enabled always reports false for Noop.
func (Noop) Enabled() bool { return false }

// Affected always returns no nodes and no error for Noop.
func (Noop) Affected(seed string, repoDir string) ([]string, error) { return nil, nil }

// Detect returns the cli plane when the project has opted in (cfg.Context
// carries a "graphify" key) and the graphify binary is found on PATH;
// otherwise it returns Noop.
func Detect(cfg project.Config) Plane {
	if cfg.Context == nil {
		return Noop{}
	}
	if _, ok := cfg.Context["graphify"]; !ok {
		return Noop{}
	}
	if _, err := exec.LookPath("graphify"); err != nil {
		return Noop{}
	}
	return cliPlane{}
}

// cliPlane shells out to the graphify binary.
type cliPlane struct{}

// Enabled always reports true for cliPlane (Detect only returns it when
// graphify is actually available).
func (cliPlane) Enabled() bool { return true }

// Affected runs `graphify affected <seed> --graph <path> --depth N` in
// repoDir and parses its "- <label> ..." lines into a slice of labels.
// Both "No unique node match" and "No affected nodes found" are normal,
// zero-result outcomes (graphify exits 0 for them), not errors.
func (cliPlane) Affected(seed string, repoDir string) ([]string, error) {
	graphPath := filepath.Join(repoDir, "graphify-out", "graph.json")
	cmd := exec.Command("graphify", "affected", seed, "--graph", graphPath, "--depth", defaultDepth)
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("graphify affected %s: %s", seed, msg)
	}
	return parseAffected(stdout.String()), nil
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
