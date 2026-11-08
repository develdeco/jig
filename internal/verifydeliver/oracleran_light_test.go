package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func lightRepo(t *testing.T) (dir string, commit func(files map[string]string) string) {
	t.Helper()
	dir = t.TempDir()
	if _, err := gitx.Run(dir, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	commit = func(files map[string]string) string {
		t.Helper()
		for rel, body := range files {
			full := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := gitx.Run(dir, "add", "-A"); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.Run(dir, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "step"); err != nil {
			t.Fatal(err)
		}
		sha, err := gitx.RevParse(dir, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	return dir, commit
}

// TestOracleRanNamesALightReuse: a pass on a commit that differs from the
// reviewed head only in light paths still names its command and says it was
// reused with the light oracle run; a pass on the head's own tree wins over
// it, and a delta with a non-light file counts for nothing.
func TestOracleRanNamesALightReuse(t *testing.T) {
	t.Parallel()
	dir, commit := lightRepo(t)
	manifest := "oracles:\n  test: go test ./...\nlight:\n  paths: [\"*.md\"]\n  oracle: go test ./lint\n"
	pass := commit(map[string]string{".claude/jig.yaml": manifest, "a.go": "package a\n"})
	built := []store.Slice{{ID: "s", Oracle: "test", Workspace: "root"}}
	line := func(commit string) journal.Line {
		return journal.Line{Event: journal.EventOracle, Outcome: "pass", Slice: "s", Commit: commit, Command: "go test ./..."}
	}

	head := commit(map[string]string{"README.md": "x\n"})
	commands, reuse := oracleRan([]journal.Line{line(pass)}, built, dir, head)
	if got := commands["test"]; len(got) != 1 || got[0] != "go test ./..." {
		t.Errorf("commands = %v, want the suite command", commands)
	}
	if want := "reused the whole-suite pass of " + shortSHA(pass) + ", and the light oracle ran: `go test ./lint`"; reuse["test"] != want {
		t.Errorf("reuse = %q, want %q", reuse["test"], want)
	}

	// A verify light line at the head names its origin, whichever commit it is on.
	lightLine := line(head)
	lightLine.Ran, lightLine.ReusedFrom = "go test ./lint", pass
	_, reuse = oracleRan([]journal.Line{line(pass), lightLine}, built, dir, head)
	if !strings.Contains(reuse["test"], shortSHA(pass)) || !strings.Contains(reuse["test"], "go test ./lint") {
		t.Errorf("reuse = %q, want the origin and the lint", reuse["test"])
	}

	// A pass on the head's own tree is no reuse, and keeps standing after.
	_, reuse = oracleRan([]journal.Line{line(pass), line(head), line(pass)}, built, dir, head)
	if _, ok := reuse["test"]; ok {
		t.Errorf("reuse = %q, want none for a full run on the head", reuse["test"])
	}

	// A non-light file between the pass and the head: the pass counts for nothing.
	code := commit(map[string]string{"a.go": "package a\n\nvar X = 1\n"})
	commands, reuse = oracleRan([]journal.Line{line(pass)}, built, dir, code)
	if len(commands) != 0 || len(reuse) != 0 {
		t.Errorf("commands = %v, reuse = %v, want nothing for a non-light delta", commands, reuse)
	}
}

// TestVerificationSectionSaysAPassWasReused: the oracle's last line carries the
// reuse words; an oracle without them reads as before.
func TestVerificationSectionSaysAPassWasReused(t *testing.T) {
	t.Parallel()
	rep := reportYAML{Reuse: map[string]string{"test": "reused the whole-suite pass of abc1234, and the light oracle ran: `go test ./lint`"}}
	got := renderVerificationSection(rep, []string{"test", "vet"}, map[string][]string{"test": {"go test ./..."}, "vet": {"go vet ./..."}}, nil)
	for _, want := range []string{
		"- test: `go test ./...` (reused the whole-suite pass of abc1234, and the light oracle ran: `go test ./lint`)\n",
		"- vet: `go vet ./...`\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the section lacks %q:\n%s", want, got)
		}
	}
}
