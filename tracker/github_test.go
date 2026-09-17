package tracker_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/store"
	"github.com/develdeco/jig/tracker"
)

// buildGhStub compiles testdata/fixture/ghstub into a binary named gh (or
// gh.exe on Windows) inside a fresh directory, returning that directory so
// it can be prepended to PATH.
func buildGhStub(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Dir(filepath.Dir(thisFile)) // tracker/ -> repo root
	src := filepath.Join(repoRoot, "testdata", "fixture", "ghstub")

	dir := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	out := filepath.Join(dir, name)

	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", out, src)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ghstub: %v\n%s", err, output)
	}
	return dir
}

func TestGithubArgv(t *testing.T) {
	stubDir := buildGhStub(t)
	home := t.TempDir()
	t.Setenv("JIG_HOME", home)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	logFile := filepath.Join(t.TempDir(), "gh.log")
	statePath := filepath.Join(t.TempDir(), "gh.state")
	t.Setenv("GH_STUB_LOG", logFile)
	t.Setenv("GH_STUB_STATE", statePath)

	cfg := project.Config{
		SchemaVersion: 1,
		Name:          "fixture",
		TicketFormat:  "JIG-{n}",
		Tracker:       "github",
		Repos:         []project.Repo{{Remote: "git@github.com:owner/repo.git"}},
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}

	st, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	id, err := a.Mint(tracker.Draft{Title: "T", Body: "B"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id != "#1" {
		t.Fatalf("id = %q, want #1", id)
	}

	if err := a.Comment(id, "hello"); err != nil {
		t.Fatalf("Comment: %v", err)
	}

	proj := tracker.Projection{
		Description: "consolidated",
		Subtasks: []tracker.Subtask{
			{ID: "#2", Title: "sub", State: "queued", BlockedBy: []string{"#1"}},
		},
	}
	if err := a.Project(id, proj); err != nil {
		t.Fatalf("Project: %v", err)
	}

	lines := readLoggedArgv(t, logFile)

	if !anyLineHasPrefix(lines, "issue", "create") {
		t.Fatalf("no logged 'issue create' call in %v", lines)
	}
	if !anyLineContainsAll(lines, "issue", "comment", "1") {
		t.Fatalf("no logged 'issue comment' call in %v", lines)
	}
	if !anyLineHasGraphqlMutation(lines) {
		t.Fatalf("no logged graphql mutation call in %v", lines)
	}
	if !anyLineContainsSubstring(lines, "dependencies/blocked_by") {
		t.Fatalf("no logged dependencies/blocked_by call in %v", lines)
	}
}

func TestGithubPRCreateArgv(t *testing.T) {
	stubDir := buildGhStub(t)
	home := t.TempDir()
	t.Setenv("JIG_HOME", home)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	logFile := filepath.Join(t.TempDir(), "gh.log")
	statePath := filepath.Join(t.TempDir(), "gh.state")
	t.Setenv("GH_STUB_LOG", logFile)
	t.Setenv("GH_STUB_STATE", statePath)

	cfg := project.Config{
		SchemaVersion: 1,
		Name:          "fixture",
		TicketFormat:  "JIG-{n}",
		Tracker:       "github",
		Repos:         []project.Repo{{Remote: "git@github.com:owner/repo.git"}},
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	creator, ok := a.(tracker.PRCreator)
	if !ok {
		t.Fatal("github adapter does not implement tracker.PRCreator")
	}

	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte("pr body"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	url, err := creator.CreatePR("jig/JIG-1", "main", "JIG-1: title", bodyFile)
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if url == "" {
		t.Fatal("CreatePR returned an empty url")
	}

	lines := readLoggedArgv(t, logFile)
	if !anyLineHasPrefix(lines, "pr", "create") {
		t.Fatalf("no logged 'pr create' call in %v", lines)
	}
	if !anyLineContainsAll(lines, "--base", "main", "--head", "jig/JIG-1", "--body-file", bodyFile) {
		t.Fatalf("logged 'pr create' call missing expected flags in %v", lines)
	}
}

func TestStubsNotImplemented(t *testing.T) {
	for _, name := range []string{"jira", "linear"} {
		t.Run(name, func(t *testing.T) {
			cfg := project.Config{SchemaVersion: 1, Name: "fixture", TicketFormat: "T-{n}", Tracker: name}
			st, cfg := newTestStore(t, cfg)
			a, err := tracker.New(cfg, st)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := a.Mint(tracker.Draft{Title: "x"}); err == nil {
				t.Fatal("Mint: want error")
			}
			if err := a.Project("x", tracker.Projection{}); err == nil {
				t.Fatal("Project: want error")
			}
			if err := a.Comment("x", "y"); err == nil {
				t.Fatal("Comment: want error")
			}
		})
	}
}

func readLoggedArgv(t *testing.T, logFile string) [][]string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read gh stub log: %v", err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var argv []string
		if err := json.Unmarshal([]byte(line), &argv); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		out = append(out, argv)
	}
	return out
}

func anyLineHasPrefix(lines [][]string, prefix ...string) bool {
	for _, argv := range lines {
		if len(argv) < len(prefix)+1 { // argv[0] is the gh binary itself
			continue
		}
		match := true
		for i, p := range prefix {
			if argv[i+1] != p {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func anyLineContainsAll(lines [][]string, want ...string) bool {
	for _, argv := range lines {
		found := map[string]bool{}
		for _, a := range argv {
			for _, w := range want {
				if a == w {
					found[w] = true
				}
			}
		}
		if len(found) == len(want) {
			return true
		}
	}
	return false
}

func anyLineContainsSubstring(lines [][]string, sub string) bool {
	for _, argv := range lines {
		for _, a := range argv {
			if strings.Contains(a, sub) {
				return true
			}
		}
	}
	return false
}

func anyLineHasGraphqlMutation(lines [][]string) bool {
	for _, argv := range lines {
		hasGraphql := false
		hasMutation := false
		for _, a := range argv {
			if a == "graphql" {
				hasGraphql = true
			}
			if strings.HasPrefix(a, "query=mutation") {
				hasMutation = true
			}
		}
		if hasGraphql && hasMutation {
			return true
		}
	}
	return false
}
