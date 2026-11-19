package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
)

// newTestOriginClone creates a bare remote with a committed, pushed v2
// project.yaml (keys: T, no trackers), then a plain clone of it at dir,
// ready for `jig ticket new --store dir` to claim against.
func newTestOriginClone(t *testing.T, dir string) (remote string) {
	t.Helper()
	parent := filepath.Dir(dir)
	remote = filepath.Join(parent, filepath.Base(dir)+"-remote.git")

	if _, err := gitx.Run("", "init", "--bare", "-b", "main", remote); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(parent, filepath.Base(dir)+"-seed")
	if _, err := gitx.Run("", "clone", remote, seed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "project.yaml"), []byte("schema_version: 2\nname: demo\nkeys:\n  DEMO: everything in demo\ntrackers: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".gitattributes"), []byte(gitx.StoreAttributes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".gitignore"), []byte("*.lock\n.*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(seed, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(seed, "-c", "user.name=tester", "-c", "user.email=tester@example.invalid", "commit", "-m", "jig: init store"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(seed, "push", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	if _, err := gitx.Run("", "clone", remote, dir); err != nil {
		t.Fatal(err)
	}
	return remote
}

// TestTicketNewClaimsOnOrigin covers jig ticket new against a store whose
// origin is a bare repo: the id it mints lands on the origin in its own
// commit, named "<id>: new ticket", and the command's output names only
// that id.
func TestTicketNewClaimsOnOrigin(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginClone(t, clone)

	var buf bytes.Buffer
	code := run(e, []string{"ticket", "new", "--title", "Fix the thing", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "DEMO-1") {
		t.Fatalf("output missing DEMO-1:\n%s", out)
	}

	subject, err := gitx.Run("", "--git-dir", remote, "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatal(err)
	}
	if subject != "DEMO-1: new ticket" {
		t.Fatalf("remote HEAD subject = %q, want %q", subject, "DEMO-1: new ticket")
	}

	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.ReadTicket("DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Fix the thing" {
		t.Fatalf("ticket.yaml title = %q, want %q", got.Title, "Fix the thing")
	}
}

// TestTicketNewTiesTheMintingSessionToTheTicket: the session that mints a
// ticket works on it from then on, before any journal line names it, so jig
// ticket new records a session-touch line naming the ticket for it in the
// machine ledger, and names the ticket's folder, where the brief goes. A
// ticket minted outside a session writes no line.
func TestTicketNewTiesTheMintingSessionToTheTicket(t *testing.T) {
	t.Parallel()
	for _, session := range []string{"s-plan", ""} {
		home := t.TempDir()
		vars := map[string]string{"JIG_HOME": home, "HOME": home, "USERPROFILE": home}
		if session != "" {
			vars[locate.SessionEnv] = session
			if err := ledger.StartSession(home, time.Time{}, ledger.Registration{ID: session, Harness: "claude", Model: "claude-sonnet-5"}); err != nil {
				t.Fatal(err)
			}
		}
		clone := filepath.Join(t.TempDir(), "clone")
		newTestOriginClone(t, clone)

		var buf bytes.Buffer
		if code := run(envFrom(vars), []string{"ticket", "new", "--title", "Fix the thing", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
			t.Fatalf("jig ticket new: exit %d\n%s", code, buf.String())
		}
		st, err := store.Open(clone)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), st.TicketDir("DEMO-1")) {
			t.Errorf("the output does not name the ticket's folder %s:\n%s", st.TicketDir("DEMO-1"), buf.String())
		}
		lines, err := ledger.Read(home)
		if err != nil {
			t.Fatal(err)
		}
		var touches []journal.Line
		for _, l := range lines {
			if l.Event == journal.EventSessionTouch {
				touches = append(touches, l)
			}
		}
		switch {
		case session == "" && len(touches) != 0:
			t.Errorf("a ticket minted outside a session wrote %+v", touches)
		case session != "" && (len(touches) != 1 || touches[0].Session != session || touches[0].Ticket != "DEMO-1" ||
			touches[0].Harness != "claude" || touches[0].Model != "claude-sonnet-5"):
			t.Errorf("session-touch lines = %+v, want one naming DEMO-1 for %s, with the harness and model it started with", touches, session)
		}
	}
}

// TestTicketNewRecordsTitleAndMintsNextID covers the basic mint: jig ticket
// new writes the title into <ticket>/ticket.yaml, and a second ticket gets
// the next ticket_format id, not a repeat of the first.
func TestTicketNewRecordsTitleAndMintsNextID(t *testing.T) {
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
	code, out := jig("ticket", "new", "--title", "Fix the thing")
	if code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, out)
	}
	// A minted ticket has no work yet, and there are two ways to give it some:
	// a brief with slices, or a branch built outside jig, which needs neither.
	for _, want := range []string{"jig validate DEMO-1", "jig gate DEMO-1 --branch <name>"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig ticket new's output does not offer %q:\n%s", want, out)
		}
	}

	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "store.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's store.yaml: %v, %v", cfgs, err)
	}
	st, err := store.Open(filepath.Dir(cfgs[0]))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	got, err := st.ReadTicket("DEMO-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if got.Title != "Fix the thing" {
		t.Fatalf("ticket.yaml title = %q, want %q", got.Title, "Fix the thing")
	}
	// The slug of its branch is fixed with the title it comes from.
	if got.Slug != "fix-the-thing" {
		t.Fatalf("ticket.yaml slug = %q, want %q", got.Slug, "fix-the-thing")
	}

	if code, out := jig("ticket", "new", "--title", "Second thing"); code != 0 || !strings.Contains(out, "DEMO-2") {
		t.Fatalf("jig ticket new (second): exit %d, want id DEMO-2:\n%s", code, out)
	}
}

// TestTicketNewWithBodyRecordsItInTheRecord covers --body: jig ticket new
// writes it into the minted ticket.yaml alongside the title, and a ticket
// minted with no --body gets no body key at all.
func TestTicketNewWithBodyRecordsItInTheRecord(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginClone(t, clone)

	var buf bytes.Buffer
	code := run(e, []string{"ticket", "new", "--title", "Fix the thing", "--body", "Why this matters.", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig ticket new --body: exit %d\n%s", code, buf.String())
	}

	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.ReadTicket("DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "Why this matters." {
		t.Fatalf("ticket.yaml body = %q, want %q", got.Body, "Why this matters.")
	}

	buf.Reset()
	if code := run(e, []string{"ticket", "new", "--title", "No body", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig ticket new without --body: exit %d\n%s", code, buf.String())
	}
	got, err = st.ReadTicket("DEMO-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "" {
		t.Fatalf("ticket.yaml body with no --body = %q, want empty", got.Body)
	}
}

// initStandaloneWithKeys runs jig init --standalone, then replaces the
// store's derived keys: entry with a keys: block declaring keys, returning
// a jig runner and the store's root.
func initStandaloneWithKeys(t *testing.T, keys string) (jig func(args ...string) (int, string), storeRoot string) {
	t.Helper()
	e := testEnv(t.TempDir())
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	e = e.inDir(repo)

	jig = func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := run(e, args, &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := jig("init", "--standalone"); code != 0 {
		t.Fatalf("jig init --standalone: exit %d\n%s", code, out)
	}
	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "store.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's store.yaml: %v, %v", cfgs, err)
	}
	data, err := os.ReadFile(cfgs[0])
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(data), "keys:\n    DEMO: everything in demo\n", keys, 1)
	if rewritten == string(data) {
		t.Fatalf("store.yaml has no derived keys: entry to replace:\n%s", data)
	}
	if err := os.WriteFile(cfgs[0], []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}
	return jig, filepath.Dir(cfgs[0])
}

// TestTicketNewRefusesAnUndeclaredKey covers the refusal: an undeclared
// --key is rejected before anything is minted, naming the declared keys and
// their meanings.
func TestTicketNewRefusesAnUndeclaredKey(t *testing.T) {
	jig, storeRoot := initStandaloneWithKeys(t, "keys:\n  STORE: the store's layout, ids and git sync\n  GRAPH: tickets, charts and the order between them\n")

	code, out := jig("ticket", "new", "--title", "Fix the thing", "--key", "NOPE")
	if code == 0 {
		t.Fatalf("jig ticket new --key NOPE: exit 0, want a refusal:\n%s", out)
	}
	for _, want := range []string{`"NOPE"`, "STORE", "GRAPH"} {
		if !strings.Contains(out, want) {
			t.Fatalf("jig ticket new --key NOPE: output lacks %q:\n%s", want, out)
		}
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := st.TicketIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("TicketIDs = %v, want none minted", ids)
	}
}
