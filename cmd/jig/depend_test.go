package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// dependRig is a standalone project with a git-backed store holding the
// tickets DEMO-1 to DEMO-4, and the env that runs jig in its repo.
type dependRig struct {
	e        env
	storeDir string
}

func newDependRig(t *testing.T) dependRig {
	t.Helper()
	e := testEnv(t.TempDir())
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	r := dependRig{e: e.inDir(repo), storeDir: filepath.Join(filepath.Dir(repo), "demo-tickets")}
	if out, code := runMain(t, r.e, "", "init", "--standalone"); code != 0 {
		t.Fatalf("jig init: exit %d\n%s", code, out)
	}
	for _, title := range []string{"One", "Two", "Three", "Four"} {
		if out, code := runMain(t, r.e, "", "ticket", "new", "--title", title); code != 0 {
			t.Fatalf("jig ticket new: exit %d\n%s", code, out)
		}
	}
	return r
}

func (r dependRig) jig(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return runMain(t, r.e, "", args...)
}

func (r dependRig) ticketYAML(t *testing.T, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.storeDir, "tickets", id, "ticket.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (r dependRig) head(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(gitMust(t, r.storeDir, "rev-parse", "HEAD"))
}

// TestDepend: the verb records, replaces and clears a ticket's edges as one
// commit of ticket.yaml alone, and refuses what it cannot record before it
// writes anything.
func TestDepend(t *testing.T) {
	t.Parallel()
	r := newDependRig(t)

	if out, code := r.jig(t, "close", "DEMO-4", "--reason", "not-planned"); code != 0 {
		t.Fatalf("jig close: exit %d\n%s", code, out)
	}
	if out, code := r.jig(t, "depend", "DEMO-2", "--on", "DEMO-3"); code != 0 {
		t.Fatalf("jig depend DEMO-2 --on DEMO-3: exit %d\n%s", code, out)
	}
	if out, code := r.jig(t, "depend", "DEMO-1", "--on", "DEMO-2"); code != 0 {
		t.Fatalf("jig depend DEMO-1 --on DEMO-2: exit %d\n%s", code, out)
	}

	// Every refusal is a VALIDATION_ERROR (TICKET_CLOSED for a closed child)
	// and leaves ticket.yaml byte for byte and the store's history as it was.
	snapshot := func() string {
		var sb strings.Builder
		for _, id := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4"} {
			sb.WriteString(r.ticketYAML(t, id))
		}
		return sb.String() + r.head(t)
	}
	before := snapshot()
	for _, tc := range []struct {
		name string
		args []string
		code string
		want string
	}{
		{"no --on", []string{"depend", "DEMO-1"}, "VALIDATION_ERROR", "missing required --on"},
		{"bad kind", []string{"depend", "DEMO-1", "--on", "DEMO-2", "--kind", "soon"}, "VALIDATION_ERROR", "neither merged nor stacked"},
		{"clear with kind", []string{"depend", "DEMO-1", "--on", "DEMO-2", "--clear", "--kind", "merged"}, "VALIDATION_ERROR", "--clear takes no --kind"},
		{"self edge", []string{"depend", "DEMO-1", "--on", "DEMO-1"}, "VALIDATION_ERROR", "cannot wait for itself"},
		{"unknown parent", []string{"depend", "DEMO-1", "--on", "DEMO-99"}, "VALIDATION_ERROR", "DEMO-99 not found"},
		{"unknown child", []string{"depend", "DEMO-99", "--on", "DEMO-1"}, "VALIDATION_ERROR", "DEMO-99 not found"},
		{"closed child", []string{"depend", "DEMO-4", "--on", "DEMO-1"}, "TICKET_CLOSED", "DEMO-4 is closed"},
		{"direct cycle", []string{"depend", "DEMO-2", "--on", "DEMO-1"}, "VALIDATION_ERROR", "DEMO-2 -> DEMO-1 -> DEMO-2"},
		{"cycle through a chain", []string{"depend", "DEMO-3", "--on", "DEMO-1"}, "VALIDATION_ERROR", "DEMO-3 -> DEMO-1 -> DEMO-2 -> DEMO-3"},
	} {
		out, code := r.jig(t, tc.args...)
		if code == 0 || !strings.Contains(out, tc.code) || !strings.Contains(out, tc.want) {
			t.Errorf("%s: exit %d, want a %s refusal containing %q\n%s", tc.name, code, tc.code, tc.want, out)
		}
		if snapshot() != before {
			t.Fatalf("%s: a refusal changed the store", tc.name)
		}
	}

	// A closed parent is allowed.
	if out, code := r.jig(t, "depend", "DEMO-3", "--on", "DEMO-4"); code != 0 {
		t.Fatalf("a closed parent: exit %d\n%s", code, out)
	}
	if out, code := r.jig(t, "depend", "DEMO-3", "--on", "DEMO-4", "--clear"); code != 0 {
		t.Fatalf("clear it: exit %d\n%s", code, out)
	}

	// Recorded, unchanged, changed.
	out, code := r.jig(t, "depend", "DEMO-1", "--on", "DEMO-3", "--kind", "stacked")
	if code != 0 || !strings.Contains(out, "blocked_by: \"DEMO-2:merged,DEMO-3:stacked\"") || !strings.Contains(out, "result: recorded") || !strings.Contains(out, "jig next DEMO-1") {
		t.Fatalf("recorded: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "branch") {
		t.Errorf("a stacked edge says nothing about the branch:\n%s", out)
	}
	if subject := strings.TrimSpace(gitMust(t, r.storeDir, "log", "-1", "--format=%s")); subject != "DEMO-1: depend on DEMO-3 (stacked)" {
		t.Errorf("commit subject = %q", subject)
	}
	if files := strings.Fields(gitMust(t, r.storeDir, "show", "--name-only", "--format=", "HEAD")); len(files) != 1 || files[0] != "tickets/DEMO-1/ticket.yaml" {
		t.Errorf("the commit touched %v, want ticket.yaml alone", files)
	}
	head := r.head(t)
	out, code = r.jig(t, "depend", "DEMO-1", "--on", "DEMO-3", "--kind", "stacked")
	if code != 0 || !strings.Contains(out, "result: unchanged") || r.head(t) != head {
		t.Errorf("same edge: exit %d, head moved = %v\n%s", code, r.head(t) != head, out)
	}
	out, code = r.jig(t, "depend", "DEMO-1", "--on", "DEMO-3")
	if code != 0 || !strings.Contains(out, "blocked_by: \"DEMO-2:merged,DEMO-3:merged\"") || !strings.Contains(out, "result: changed") {
		t.Errorf("other kind: exit %d\n%s", code, out)
	}
	if strings.Count(r.ticketYAML(t, "DEMO-1"), "ticket: DEMO-3") != 1 {
		t.Errorf("one edge per parent:\n%s", r.ticketYAML(t, "DEMO-1"))
	}

	// Clear one, clear what is not there, clear all.
	out, code = r.jig(t, "depend", "DEMO-1", "--on", "DEMO-2", "--clear")
	if code != 0 || !strings.Contains(out, "blocked_by: \"DEMO-3:merged\"") || !strings.Contains(out, "result: cleared") {
		t.Errorf("clear one: exit %d\n%s", code, out)
	}
	head = r.head(t)
	out, code = r.jig(t, "depend", "DEMO-1", "--on", "DEMO-2", "--clear")
	if code != 0 || !strings.Contains(out, "result: unchanged") || r.head(t) != head {
		t.Errorf("clear what is not there: exit %d\n%s", code, out)
	}
	out, code = r.jig(t, "depend", "DEMO-1", "--clear")
	if code != 0 || !strings.Contains(out, "blocked_by: none") || !strings.Contains(out, "result: cleared") || strings.Contains(r.ticketYAML(t, "DEMO-1"), "blocked_by") {
		t.Errorf("clear all: exit %d\n%s\n%s", code, out, r.ticketYAML(t, "DEMO-1"))
	}
	if subject := strings.TrimSpace(gitMust(t, r.storeDir, "log", "-1", "--format=%s")); subject != "DEMO-1: clear dependencies" {
		t.Errorf("commit subject = %q", subject)
	}
	if out, code = r.jig(t, "depend", "DEMO-1", "--clear"); code != 0 || !strings.Contains(out, "result: unchanged") {
		t.Errorf("clear all of none: exit %d\n%s", code, out)
	}
}

// TestDependResolvesAnAlias: a ticket named by an alias is the ticket it now is.
func TestDependResolvesAnAlias(t *testing.T) {
	t.Parallel()
	r := newDependRig(t)
	path := filepath.Join(r.storeDir, "tickets", "DEMO-2", "ticket.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("aliases:\n  - OLD-2\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, r.storeDir, "add", "-A")
	gitMust(t, r.storeDir, "commit", "-m", "alias")

	out, code := r.jig(t, "depend", "DEMO-1", "--on", "OLD-2")
	if code != 0 || !strings.Contains(out, "OLD-2 is now DEMO-2") || !strings.Contains(out, "blocked_by: \"DEMO-2:merged\"") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(r.ticketYAML(t, "DEMO-1"), "ticket: DEMO-2") {
		t.Errorf("the edge names the alias:\n%s", r.ticketYAML(t, "DEMO-1"))
	}
}

// TestDependRefusesARemovedTicket: a ticket removed by a commit leaves its
// folder emptied, and that folder is no ticket: jig depend refuses it as jig
// rank does, and writes no record into it.
func TestDependRefusesARemovedTicket(t *testing.T) {
	t.Parallel()
	r := newDependRig(t)
	gitMust(t, r.storeDir, "rm", "-r", "-q", "tickets/DEMO-4")
	gitMust(t, r.storeDir, "commit", "-q", "-m", "remove DEMO-4")
	if err := os.MkdirAll(filepath.Join(r.storeDir, "tickets", "DEMO-4"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := r.head(t)

	out, code := r.jig(t, "depend", "DEMO-4", "--on", "DEMO-3")
	if code == 0 || !strings.Contains(out, "VALIDATION_ERROR") || !strings.Contains(out, "DEMO-4 not found") {
		t.Errorf("depend on a removed ticket: exit %d, want a not-found refusal\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(r.storeDir, "tickets", "DEMO-4", "ticket.yaml")); !os.IsNotExist(err) {
		t.Errorf("a ticket.yaml was written into the removed ticket's folder (stat err = %v)", err)
	}
	if r.head(t) != head {
		t.Errorf("the refusal committed")
	}
}
