package slicectx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/graphify"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// Caps on the related list: the files a code graph links to a goal, most
// directly matched first, and the symbols named per file.
const (
	maxRelatedFiles   = 25
	maxRelatedSymbols = 8
	// maxSeedFiles is how many files of the earlier slices seed the query.
	maxSeedFiles = 20
	// maxReason is how many characters of a graph failure the ledger keeps.
	maxReason = 300
)

// related asks the code graph of the project which code is linked to the goal
// of sl and to the files the earlier slices changed, and groups it by file.
// With no enabled plane or no clone it returns an empty list and records
// nothing. Otherwise it keeps the output of the graph out of the commits of
// the clone, brings the graph up to date and queries it, and records a graph
// line, pass or fail with the reason: a failure leaves the list empty and
// never fails the context.
func related(in Input, sl store.Slice, earlier []EarlierSlice) []RelatedFile {
	out := []RelatedFile{}
	if in.Graph == nil || !in.Graph.Enabled() || in.RepoDir == "" {
		return out
	}
	started := time.Now()
	nodes, err := queryGraph(in.Graph, in.RepoDir, graphQuestion(sl.Goal, earlier))
	line := journal.Line{
		Ticket: in.Ticket, Slice: sl.ID, Session: in.Session, Cwd: in.RepoDir,
		Seconds: int((time.Since(started) + time.Second - 1) / time.Second),
	}
	if err != nil {
		line.Outcome = "fail: " + oneLine(err.Error())
	} else {
		out = groupRelated(nodes)
		line.Outcome = "pass"
		for _, r := range out {
			line.Files = append(line.Files, r.File)
		}
	}
	if in.RecordGraph != nil {
		_ = in.RecordGraph(line)
	}
	return out
}

// queryGraph excludes the output of the graph in dir, updates the graph, and
// queries it with question.
func queryGraph(plane graphify.Plane, dir, question string) ([]graphify.Node, error) {
	if err := excludeInClone(dir, "/"+graphify.OutDir+"/"); err != nil {
		return nil, err
	}
	if err := plane.Update(dir); err != nil {
		return nil, err
	}
	return plane.Query(dir, question)
}

// graphQuestion is the code-graph query for a slice: its goal, then the files
// earlier slices changed, as seeds.
func graphQuestion(goal string, earlier []EarlierSlice) string {
	var files []string
	for _, e := range earlier {
		files = append(files, e.Files...)
	}
	if len(files) > maxSeedFiles {
		files = files[:maxSeedFiles]
	}
	if len(files) == 0 {
		return goal
	}
	return goal + " Related files: " + strings.Join(files, " ")
}

// groupRelated groups nodes by file in first-seen order, each with its
// symbols as "<label> L<line>", within the caps.
func groupRelated(nodes []graphify.Node) []RelatedFile {
	out := []RelatedFile{}
	at := map[string]int{}
	for _, n := range nodes {
		i, ok := at[n.File]
		if !ok {
			if len(out) == maxRelatedFiles {
				continue
			}
			i = len(out)
			at[n.File] = i
			out = append(out, RelatedFile{File: n.File, Symbols: []string{}})
		}
		if len(out[i].Symbols) == maxRelatedSymbols {
			continue
		}
		sym := n.Label
		if n.Line > 0 {
			sym = fmt.Sprintf("%s L%d", n.Label, n.Line)
		}
		out[i].Symbols = append(out[i].Symbols, sym)
	}
	return out
}

// excludeInClone adds pattern to the info/exclude of the clone, so the files
// a tool writes there are never committed and never make the tree look dirty.
// The file is local to the clone and shared with no one.
func excludeInClone(dir, pattern string) error {
	path, err := gitx.Run(dir, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return fmt.Errorf("slicectx: find info/exclude of the clone: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		data = append(data, '\n')
	}
	data = append(data, []byte(pattern+"\n")...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// oneLine flattens s to one line of at most maxReason characters.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxReason {
		return string(r[:maxReason])
	}
	return s
}
