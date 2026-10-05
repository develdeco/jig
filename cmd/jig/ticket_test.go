package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// TestTicketNewRecordsTitleAndMintsNextID covers the basic mint: jig ticket
// new writes the title into <ticket>/ticket.yaml, and a second ticket gets
// the next ticket_format id, not a repeat of the first.
func TestTicketNewRecordsTitleAndMintsNextID(t *testing.T) {
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
	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, out)
	}
	// A minted ticket has no work yet, and there are two ways to give it some:
	// a brief with slices, or a branch built outside jig, which needs neither.
	for _, want := range []string{"jig validate T-1", "jig gate T-1 --branch <name>"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig ticket new's output does not offer %q:\n%s", want, out)
		}
	}

	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "project.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's project.yaml: %v, %v", cfgs, err)
	}
	st, err := store.Open(filepath.Dir(cfgs[0]))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if got.Title != "Fix the thing" {
		t.Fatalf("ticket.yaml title = %q, want %q", got.Title, "Fix the thing")
	}

	if code, out := jig("ticket", "new", "--title", "Second thing"); code != 0 || !strings.Contains(out, "T-2") {
		t.Fatalf("jig ticket new (second): exit %d, want id T-2:\n%s", code, out)
	}
}

// TestTicketNewMintsByTicketFormatRegardlessOfTracker covers the central
// change: jig ticket new mints through the store's own ticket_format
// counter, never asking a tracker for an id - true not just of the
// trackers: [] jig init itself writes, but of the legacy tracker: local a
// store may still carry (until L3's migration rewrites project.yaml).
func TestTicketNewMintsByTicketFormatRegardlessOfTracker(t *testing.T) {
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
	rewritten := strings.Replace(string(data), "trackers: []", "tracker: local", 1)
	if rewritten == string(data) {
		t.Fatalf("project.yaml has no trackers: [] to replace:\n%s", data)
	}
	if err := os.WriteFile(cfgs[0], []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code != 0 || !strings.Contains(out, "T-1") {
		t.Fatalf("jig ticket new with tracker: local: exit %d, want id T-1:\n%s", code, out)
	}

	st, err := store.Open(filepath.Dir(cfgs[0]))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if got.Title != "Fix the thing" {
		t.Fatalf("ticket.yaml title = %q, want %q", got.Title, "Fix the thing")
	}
	if _, err := os.Stat(filepath.Join(st.TicketDir("T-1"), "tracker")); !os.IsNotExist(err) {
		t.Fatalf("T-1/tracker exists (stat err %v), want nothing written there", err)
	}
}
