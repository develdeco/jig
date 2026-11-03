package gitx

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestPathLog reads a path-limited log: the commits that changed a path and no
// others, newest first, bounded by the limit, with a subject that holds spaces
// and punctuation whole, the committer time in the offset it was made in, no
// commit as nil, a path taken literally, and the three refused arguments.
func TestPathLog(t *testing.T) {
	t.Parallel()
	repo := initRepo(t, filepath.Join(t.TempDir(), "origin.git"))
	commit := func(path, subject, date string) string {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(subject), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(repo, "add", path); err != nil {
			t.Fatal(err)
		}
		env := []string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}
		if _, err := RunEnv(repo, env, "commit", "-m", subject, "-m", "a body line\nand another"); err != nil {
			t.Fatal(err)
		}
		sha, err := RevParse(repo, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}

	first := commit("charts/a/map.md", "chart a: first, with spaces", "2026-03-02T11:30:00+01:00")
	other := commit("charts/b/map.md", "chart b: only b", "2026-03-02T11:31:00Z")
	neighbor := commit("charts/a-two/map.md", "chart a-two: a neighbor", "2026-03-02T11:32:00Z")
	second := commit("charts/a/tickets.yaml", "chart a: second ('quotes' and a | pipe)", "2026-03-02T11:33:00Z")

	got, err := PathLog(repo, "HEAD", 10, "charts/a")
	if err != nil {
		t.Fatalf("PathLog: %v", err)
	}
	if len(got) != 2 || got[0].SHA != second || got[1].SHA != first {
		t.Fatalf("PathLog(charts/a) = %+v, want %s then %s, newest first and not %s or %s", got, second, first, other, neighbor)
	}
	if got[0].Subject != "chart a: second ('quotes' and a | pipe)" || got[1].Subject != "chart a: first, with spaces" {
		t.Errorf("subjects = %q and %q: want each whole line, with no body", got[0].Subject, got[1].Subject)
	}
	if want := time.Date(2026, 3, 2, 10, 30, 0, 0, time.UTC); !got[1].Time.Equal(want) {
		t.Errorf("time = %s, want the committer time %s", got[1].Time, want)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(got[0].SHA) {
		t.Errorf("sha = %q, want 40 hex", got[0].SHA)
	}

	if got, err := PathLog(repo, "HEAD", 1, "charts/a"); err != nil || len(got) != 1 || got[0].SHA != second {
		t.Errorf("PathLog with limit 1 = %+v, %v, want only %s", got, err, second)
	}
	if got, err := PathLog(repo, "HEAD", 10, "charts/b", "charts/a-two"); err != nil || len(got) != 2 || got[0].SHA != neighbor || got[1].SHA != other {
		t.Errorf("PathLog of two paths = %+v, %v, want %s then %s", got, err, neighbor, other)
	}
	if got, err := PathLog(repo, first, 10, "charts/a"); err != nil || len(got) != 1 || got[0].SHA != first {
		t.Errorf("PathLog from a commit = %+v, %v, want only %s", got, err, first)
	}

	// No commit touched the path, or the path is a glob git would expand but
	// PathLog takes literally.
	for _, path := range []string{"charts/none", "charts/*"} {
		if got, err := PathLog(repo, "HEAD", 10, path); err != nil || got != nil {
			t.Errorf("PathLog(%q) = %+v, %v, want nil and no error", path, got, err)
		}
	}

	// Refused before git runs.
	for name, call := range map[string]func() ([]LogEntry, error){
		"a rev with a dash":  func() ([]LogEntry, error) { return PathLog(repo, "--all", 10, "charts/a") },
		"a limit of zero":    func() ([]LogEntry, error) { return PathLog(repo, "HEAD", 0, "charts/a") },
		"a negative limit":   func() ([]LogEntry, error) { return PathLog(repo, "HEAD", -3, "charts/a") },
		"no paths":           func() ([]LogEntry, error) { return PathLog(repo, "HEAD", 10) },
		"a directory absent": func() ([]LogEntry, error) { return PathLog(filepath.Join(repo, "no-such-dir"), "HEAD", 10, "x") },
	} {
		if got, err := call(); err == nil || got != nil {
			t.Errorf("%s: PathLog = %+v, %v, want an error", name, got, err)
		} else if name != "a directory absent" && !strings.HasPrefix(err.Error(), "gitx: path log:") {
			t.Errorf("%s: error %q, want gitx's own refusal", name, err)
		}
	}
}

// TestPathLogOfAnUnbornHeadIsAnError: a repository with no commit has no
// history to read, and PathLog says so rather than reporting no commit.
func TestPathLogOfAnUnbornHeadIsAnError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if got, err := PathLog(dir, "HEAD", 5, "charts/a"); err == nil || got != nil {
		t.Errorf("PathLog of an unborn HEAD = %+v, %v, want an error", got, err)
	}
}
