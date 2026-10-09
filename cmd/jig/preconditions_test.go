package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestTicketPreconditions follows a new user's first steps: after
// `jig init --standalone` and `jig ticket new`, commands on a ticket with no
// slices, or on an unknown ticket, explain what to do next instead of
// failing deep inside a run.
func TestTicketPreconditions(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	e = e.inDir(repo)

	jig := func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := run(e, args, &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := jig("init", "--standalone"); code != 0 {
		t.Fatalf("jig init --standalone: exit %d\n%s", code, out)
	}
	if code, out := jig("ticket", "new", "--title", "Fix the thing"); code != 0 || !strings.Contains(out, "DEMO-1") {
		t.Fatalf("jig ticket new: exit %d\n%s", code, out)
	}

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "DEMO-1"}, "ticket DEMO-1 has no slices yet"},
		{[]string{"run", "DEMO-99"}, "ticket DEMO-99 has no slices yet"},
		{[]string{"requeue", "DEMO-1", "--from-brief-diff"}, "ticket DEMO-1 has no slices yet"},
		{[]string{"gate", "DEMO-1"}, "ticket DEMO-1 has no slices yet"},
		{[]string{"publish", "DEMO-1", "--yes"}, "ticket DEMO-1 has no slices yet"},
		{[]string{"solve", "DEMO-1", "--yes"}, "ticket DEMO-1 has no slices yet"},
	}
	for _, c := range cases {
		code, out := jig(c.args...)
		if code == 0 || !strings.Contains(out, c.want) || !strings.Contains(out, "intake skill") {
			t.Errorf("jig %s: exit %d, want a non-zero exit with %q and an intake hint:\n%s", strings.Join(c.args, " "), code, c.want, out)
		}
	}

	// A ticket the store has no folder for at all (never minted) is told to
	// mint one, not pointed at the intake skill: no tracker holds an id jig
	// does not also have a folder for.
	code, out := jig("status", "DEMO-99")
	if code == 0 || !strings.Contains(out, "ticket DEMO-99 not found") || !strings.Contains(out, "jig ticket new") {
		t.Errorf("jig status DEMO-99: exit %d, want a non-zero exit naming the missing ticket and jig ticket new:\n%s", code, out)
	}
	if strings.Contains(out, "intake skill") {
		t.Errorf("jig status DEMO-99: output mentions the intake skill for a ticket that was never minted:\n%s", out)
	}

	code, out = jig("status", "DEMO-1")
	if code != 0 || !strings.Contains(out, "intake skill") {
		t.Errorf("jig status DEMO-1: exit %d, want 0 with the intake hint:\n%s", code, out)
	}
}
