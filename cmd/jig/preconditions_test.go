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
	t.Setenv("JIG_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	t.Chdir(repo)

	jig := func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := Main(args, &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := jig("init", "--standalone"); code != 0 {
		t.Fatalf("jig init --standalone: exit %d\n%s", code, out)
	}
	if code, out := jig("ticket", "new", "--title", "Fix the thing"); code != 0 || !strings.Contains(out, "T-1") {
		t.Fatalf("jig ticket new: exit %d\n%s", code, out)
	}

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"status", "T-99"}, "ticket T-99 not found"},
		{[]string{"run", "T-1"}, "ticket T-1 has no slices yet"},
		{[]string{"run", "T-99"}, "ticket T-99 has no slices yet"},
		{[]string{"requeue", "T-1", "--from-brief-diff"}, "ticket T-1 has no slices yet"},
		{[]string{"gate", "T-1"}, "ticket T-1 has no slices yet"},
		{[]string{"publish", "T-1", "--yes"}, "ticket T-1 has no slices yet"},
		{[]string{"solve", "T-1", "--yes"}, "ticket T-1 has no slices yet"},
	}
	for _, c := range cases {
		code, out := jig(c.args...)
		if code == 0 || !strings.Contains(out, c.want) || !strings.Contains(out, "intake skill") {
			t.Errorf("jig %s: exit %d, want a non-zero exit with %q and an intake hint:\n%s", strings.Join(c.args, " "), code, c.want, out)
		}
	}

	code, out := jig("status", "T-1")
	if code != 0 || !strings.Contains(out, "intake skill") {
		t.Errorf("jig status T-1: exit %d, want 0 with the intake hint:\n%s", code, out)
	}
}
