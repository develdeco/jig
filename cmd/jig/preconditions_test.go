package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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

// TestTicketNewRefusesReservedID covers `jig ticket new` when the tracker
// mints an id ending in a suffix the pool reserves for a ticket's gate or
// publish lease (here a local ticket_format of T-{n}-gate): the id would
// share a lease directory with ticket T-<n>'s gate, so jig must refuse it
// instead of reporting it as the new ticket.
func TestTicketNewRefusesReservedID(t *testing.T) {
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
	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "project.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's project.yaml: %v, %v", cfgs, err)
	}
	data, err := os.ReadFile(cfgs[0])
	if err != nil {
		t.Fatal(err)
	}
	reserved := strings.Replace(string(data), "T-{n}", "T-{n}-gate", 1)
	if reserved == string(data) {
		t.Fatalf("project.yaml has no T-{n} ticket_format to rewrite:\n%s", data)
	}
	if err := os.WriteFile(cfgs[0], []byte(reserved), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code == 0 || !strings.Contains(out, "T-1-gate") || !strings.Contains(out, "reserves") {
		t.Fatalf("jig ticket new with ticket_format T-{n}-gate: exit %d, want a non-zero exit naming T-1-gate as reserved:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfgs[0]), "T-1-gate")); !os.IsNotExist(err) {
		t.Fatalf("the refused ticket T-1-gate left a store folder behind (stat err %v)", err)
	}
}

// TestTicketNewRefusesReservedIDFromCommandTracker covers a tracker whose
// ids are known only once it has created the ticket (here a command tracker
// that mints EXT-7-gate): jig cannot refuse the id before the mint, so it
// refuses it after and says the ticket now exists in the tracker.
func TestTicketNewRefusesReservedIDFromCommandTracker(t *testing.T) {
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
	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "project.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's project.yaml: %v, %v", cfgs, err)
	}

	// The stub reads its request from stdin, then answers with the id.
	stubDir := t.TempDir()
	script := filepath.Join(stubDir, "tracker.sh")
	content := "#!/bin/sh\ncat > /dev/null\necho '{\"id\": \"EXT-7-gate\"}'\n"
	if runtime.GOOS == "windows" {
		script = filepath.Join(stubDir, "tracker.cmd")
		content = "@echo off\r\nfindstr \"^\" > nul\r\necho {\"id\": \"EXT-7-gate\"}\r\n"
	}
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write tracker stub: %v", err)
	}
	data, err := os.ReadFile(cfgs[0])
	if err != nil {
		t.Fatal(err)
	}
	withCommand := strings.Replace(string(data), "tracker: local", "tracker:\n    command: '"+script+"'", 1)
	if withCommand == string(data) {
		t.Fatalf("project.yaml has no local tracker to replace:\n%s", data)
	}
	if err := os.WriteFile(cfgs[0], []byte(withCommand), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code == 0 || !strings.Contains(out, "EXT-7-gate") || !strings.Contains(out, "reserves") || !strings.Contains(out, "close it there") {
		t.Fatalf("jig ticket new with a tracker minting EXT-7-gate: exit %d, want a non-zero exit naming EXT-7-gate as reserved and the ticket to close:\n%s", code, out)
	}
}
