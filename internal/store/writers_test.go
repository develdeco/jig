package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// newWriter is a clone of the store at remote, for the writer named name: a
// person identity and git config jig.writer in the clone, as a person who
// runs jig has them.
func newWriter(t *testing.T, remote, name string) (*Store, string) {
	t.Helper()
	work := filepath.Join(t.TempDir(), name)
	runGit(t, "", "clone", remote, work)
	runGit(t, work, "config", "user.name", name+" person")
	runGit(t, work, "config", "user.email", name+"@example.invalid")
	runGit(t, work, "config", "jig.writer", name)
	st, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	return st, work
}

// newWritersStore is a store with an origin and one writer, alice, whose
// clone was made a writers store by MigrateToWriters.
func newWritersStore(t *testing.T) (alice *Store, aliceDir, remote string) {
	t.Helper()
	alice, aliceDir, remote = newTestRemoteStore(t)
	runGit(t, aliceDir, "config", "jig.writer", "alice")
	if _, err := alice.MigrateToWriters(); err != nil {
		t.Fatalf("MigrateToWriters: %v", err)
	}
	return alice, aliceDir, remote
}

// mintTicket mints one ticket the way jig ticket new does, through Claim.
func mintTicket(t *testing.T, st *Store, title string) string {
	t.Helper()
	id, err := st.Claim(
		func() (string, []string, error) {
			id, err := st.Mint("JIG", Ticket{Title: title})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err != nil {
		t.Fatalf("mint %q: %v", title, err)
	}
	return id
}

// appendJournal appends one line to a ticket journal by hand, as a journal
// append would, so two writers append to one file.
func appendJournal(t *testing.T, st *Store, ticket, line string) {
	t.Helper()
	path := filepath.Join(st.TicketDir(ticket), "journal.ndjson")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

func claimIDs(t *testing.T, st *Store) []string {
	t.Helper()
	lines, err := st.Claims()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range lines {
		if l.Kind == ClaimID {
			ids = append(ids, l.Ticket)
		}
	}
	return ids
}

func axiCode(err error) string {
	var ae *axi.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// TestSanitizeWriter: a writer name is lowercase letters, digits and single
// hyphens, forty characters at most.
func TestSanitizeWriter(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"alice", "alice"},
		{"Alice", "alice"},
		{"DESKTOP-ABC123", "desktop-abc123"},
		{"host.example.com", "host-example-com"},
		{"  --a  b--  ", "a-b"},
		{"under_score", "under-score"},
		{"", ""},
		{"...", ""},
		{strings.Repeat("a", 60), strings.Repeat("a", 40)},
		{strings.Repeat("a", 39) + "-b", strings.Repeat("a", 39)},
	} {
		if got := sanitizeWriter(c.in); got != c.want {
			t.Errorf("sanitizeWriter(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWriterNameComesFromConfigThenTheHost: git config jig.writer names the
// writer, cleaned; without it the host name does; a name with nothing to make
// a name of is refused.
func TestWriterNameComesFromConfigThenTheHost(t *testing.T) {
	t.Parallel()
	st, work, _ := newTestRemoteStore(t)

	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Writer()
	if err != nil || got != sanitizeWriter(host) {
		t.Fatalf("Writer() without jig.writer = %q, %v; want the host name %q", got, err, sanitizeWriter(host))
	}

	runGit(t, work, "config", "jig.writer", "Team Alice")
	st2, _ := Open(work)
	if got, err := st2.Writer(); err != nil || got != "team-alice" {
		t.Fatalf("Writer() with jig.writer = %q, %v; want team-alice", got, err)
	}

	runGit(t, work, "config", "jig.writer", "___")
	st3, _ := Open(work)
	if _, err := st3.Writer(); axiCode(err) != "VALIDATION_ERROR" {
		t.Fatalf("Writer() with a name of no letters = %v, want VALIDATION_ERROR", err)
	}
}

// TestModeFollowsTheClaimsBranch: a store is in writers mode exactly when it
// has a claims branch, in the clone or on the origin as fetched; no other
// store is, and asking costs no git process (it reads refs).
func TestModeFollowsTheClaimsBranch(t *testing.T) {
	t.Parallel()
	st, work, remote := newTestRemoteStore(t)
	if on, err := st.Mode(); err != nil || on {
		t.Fatalf("Mode() of a single-main store = %v, %v; want false", on, err)
	}

	// Only the origin one, as in a clone made after the move.
	runGit(t, work, "config", "jig.writer", "alice")
	if _, err := st.MigrateToWriters(); err != nil {
		t.Fatal(err)
	}
	if on, err := st.Mode(); err != nil || !on {
		t.Fatalf("Mode() after the move = %v, %v; want true", on, err)
	}
	fresh, _ := newWriter(t, remote, "bob")
	if on, err := fresh.Mode(); err != nil || !on {
		t.Fatalf("Mode() of a clone made after the move = %v, %v; want true (origin/claims)", on, err)
	}

	// Packed refs count too.
	runGit(t, work, "pack-refs", "--all")
	if on, err := st.Mode(); err != nil || !on {
		t.Fatalf("Mode() with packed refs = %v, %v; want true", on, err)
	}

	// A directory that is not a repository is single-main.
	plain := &Store{Root: t.TempDir()}
	if on, err := plain.Mode(); err != nil || on {
		t.Fatalf("Mode() of a directory with no git = %v, %v; want false", on, err)
	}
}

// TestMigrateToWritersMakesTheClaimsBranchAndTheWriterBranch: the claims
// branch starts with an id line per ticket and alias, plus the driver lines of
// an old root claims.ndjson; the clone ends on writers/<name>, which holds the
// union attribute and is on the origin; main is not pushed to.
func TestMigrateToWritersMakesTheClaimsBranchAndTheWriterBranch(t *testing.T) {
	t.Parallel()
	st, work, remote := newTestRemoteStore(t)
	runGit(t, work, "config", "jig.writer", "Alice")

	// Two tickets, one with an alias, and a driver line from single-main days.
	mintTicket(t, st, "one")
	mintTicket(t, st, "two")
	if err := os.WriteFile(filepath.Join(st.TicketDir("JIG-2"), "ticket.yaml"),
		[]byte("schema_version: 1\ntitle: two\naliases:\n  - OLD-7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "alias")
	if err := st.ClaimDriver("JIG-1", "s-1", false); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "push", "origin", "main")
	mainBefore := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "rev-parse", "refs/heads/main"))

	res, err := st.MigrateToWriters()
	if err != nil {
		t.Fatalf("MigrateToWriters: %v", err)
	}
	if res.Writer != "alice" || res.Branch != "writers/alice" || res.Joined || res.IDs != 3 || res.Carried != 1 {
		t.Errorf("result = %+v, want writer alice on writers/alice, claims made with 3 ids and 1 carried line", res)
	}

	if got := strings.TrimSpace(runGit(t, work, "symbolic-ref", "--short", "HEAD")); got != "writers/alice" {
		t.Errorf("the clone is on %q, want writers/alice", got)
	}
	attrs, _ := os.ReadFile(filepath.Join(work, ".gitattributes"))
	if string(attrs) != gitx.WritersAttributes {
		t.Errorf(".gitattributes = %q, want the writers attributes", attrs)
	}
	for _, ref := range []string{"refs/heads/claims", "refs/heads/writers/alice"} {
		if out, err := gitx.RunRaw("", "--git-dir", remote, "rev-parse", "--verify", ref); err != nil {
			t.Errorf("the origin lacks %s: %v\n%s", ref, err, out)
		}
	}
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "rev-parse", "refs/heads/main")); got != mainBefore {
		t.Errorf("the origin main moved from %s to %s; a migration never pushes main", mainBefore, got)
	}

	ids := claimIDs(t, st)
	if strings.Join(ids, ",") != "JIG-1,JIG-2,OLD-7" {
		t.Errorf("id lines = %v, want JIG-1, JIG-2 and the alias OLD-7", ids)
	}
	if d, ok, err := st.Driver("JIG-1"); err != nil || !ok || d.Session != "s-1" {
		t.Errorf("Driver(JIG-1) = %+v, %v, %v; want the carried driver line of session s-1", d, ok, err)
	}
	// The claims branch is orphan history of its own: one file.
	if got := strings.TrimSpace(runGit(t, work, "ls-tree", "--name-only", "refs/heads/claims")); got != "claims.ndjson" {
		t.Errorf("the claims branch holds %q, want claims.ndjson alone", got)
	}

	// Again: nothing to do, and nothing breaks.
	again, err := st.MigrateToWriters()
	if err != nil || !again.Joined {
		t.Fatalf("second MigrateToWriters = %+v, %v; want it to find the store converted", again, err)
	}
	if got := len(claimIDs(t, st)); got != 3 {
		t.Errorf("after the second run the claims branch has %d id lines, want still 3", got)
	}
}

// TestMigrateToWritersNeedsAnOrigin: writers coordinate through the origin, so
// a store without one is refused and left alone.
func TestMigrateToWritersNeedsAnOrigin(t *testing.T) {
	t.Parallel()
	st, work := newTestStandaloneStore(t)
	runGit(t, work, "config", "jig.writer", "alice")
	_, err := st.MigrateToWriters()
	if axiCode(err) != "VALIDATION_ERROR" {
		t.Fatalf("MigrateToWriters without an origin = %v, want VALIDATION_ERROR", err)
	}
	if on, _ := st.Mode(); on {
		t.Error("the refused migration left the store in writers mode")
	}
	if got := strings.TrimSpace(runGit(t, work, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Errorf("the refused migration left the clone on %q", got)
	}
}

// TestSingleMainStoreStaysOnMain: a store with no claims branch does what it
// always did - works on main, pushes main, makes no ref or file of writers
// mode.
func TestSingleMainStoreStaysOnMain(t *testing.T) {
	t.Parallel()
	st, work, remote := newTestRemoteStore(t)
	mintTicket(t, st, "one")
	if err := st.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("jig: nothing"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(runGit(t, work, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Errorf("a single-main store is on %q, want main", got)
	}
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "for-each-ref", "--format=%(refname)")); got != "refs/heads/main" {
		t.Errorf("the origin refs = %q, want main alone", got)
	}
	if got := strings.TrimSpace(runGit(t, work, "for-each-ref", "refs/jig", "refs/heads/claims")); got != "" {
		t.Errorf("the clone has writers-mode refs: %q", got)
	}
	attrs, _ := os.ReadFile(filepath.Join(work, ".gitattributes"))
	if string(attrs) != gitx.StoreAttributes {
		t.Errorf(".gitattributes = %q, want the store attributes unchanged", attrs)
	}
}

// TestWritersMergeEachOthersJournalsByUnion: two writers append to the same
// journal on their own branches; each Sync brings the other in without a
// conflict and ends with both writers lines in the journal.
func TestWritersMergeEachOthersJournalsByUnion(t *testing.T) {
	t.Parallel()
	alice, aliceDir, remote := newWritersStore(t)
	mintTicket(t, alice, "one")
	appendJournal(t, alice, "JIG-1", `{"ts":"2026-01-01T00:00:00Z","ticket":"JIG-1","event":"base"}`)
	if err := alice.Push("jig: base"); err != nil {
		t.Fatal(err)
	}

	bob, bobDir := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatalf("bob first Sync: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, bobDir, "symbolic-ref", "--short", "HEAD")); got != "writers/bob" {
		t.Fatalf("bob is on %q after his first Sync, want writers/bob", got)
	}
	if _, err := os.Stat(filepath.Join(bob.TicketDir("JIG-1"), "journal.ndjson")); err != nil {
		t.Fatalf("bob does not have the journal alice wrote: %v", err)
	}

	appendJournal(t, alice, "JIG-1", `{"ts":"2026-01-01T00:00:01Z","ticket":"JIG-1","event":"from-alice"}`)
	appendJournal(t, bob, "JIG-1", `{"ts":"2026-01-01T00:00:02Z","ticket":"JIG-1","event":"from-bob"}`)
	if err := alice.Push("jig: alice line"); err != nil {
		t.Fatal(err)
	}
	if err := bob.Push("jig: bob line"); err != nil {
		t.Fatal(err)
	}
	for name, st := range map[string]*Store{"alice": alice, "bob": bob} {
		if err := st.Sync(); err != nil {
			t.Fatalf("%s Sync: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(st.TicketDir("JIG-1"), "journal.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"base", "from-alice", "from-bob"} {
			if strings.Count(string(data), `"event":"`+want+`"`) != 1 {
				t.Errorf("%s journal has %q %d times, want once:\n%s", name, want, strings.Count(string(data), `"event":"`+want+`"`), data)
			}
		}
		if strings.Contains(string(data), "<<<<<<<") {
			t.Errorf("%s journal has conflict markers:\n%s", name, data)
		}
	}
	// Neither pushed main.
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "for-each-ref", "--format=%(refname)", "refs/heads/")); got !=
		"refs/heads/claims\nrefs/heads/main\nrefs/heads/writers/alice\nrefs/heads/writers/bob" {
		t.Errorf("origin branches = %q", got)
	}
	_ = aliceDir
}

// TestWritersJournalsConvergeWithoutDuplicates: after several rounds in which
// both writers append and each merges the other, with the merges crossing,
// every line is in both journals exactly once.
func TestWritersJournalsConvergeWithoutDuplicates(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	mintTicket(t, alice, "one")
	appendJournal(t, alice, "JIG-1", `{"ts":"2026-01-01T00:00:00Z","ticket":"JIG-1","event":"base"}`)
	if err := alice.Push("jig: base"); err != nil {
		t.Fatal(err)
	}
	bob, _ := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}
	var want []string
	for round := 0; round < 2; round++ {
		for _, who := range []struct {
			name string
			st   *Store
		}{{"alice", alice}, {"bob", bob}} {
			event := who.name + "-" + string(rune('a'+round))
			want = append(want, event)
			appendJournal(t, who.st, "JIG-1", `{"ts":"2026-01-01T00:00:0`+string(rune('1'+round))+`Z","ticket":"JIG-1","event":"`+event+`"}`)
			if err := who.st.Push("jig: " + event); err != nil {
				t.Fatalf("%s Push: %v", event, err)
			}
		}
		// Each merges the other in the same round: the merges cross.
		if err := alice.Sync(); err != nil {
			t.Fatalf("round %d alice Sync: %v", round, err)
		}
		if err := bob.Sync(); err != nil {
			t.Fatalf("round %d bob Sync: %v", round, err)
		}
		if err := alice.Push("jig: after merge"); err != nil {
			t.Fatal(err)
		}
		if err := bob.Push("jig: after merge"); err != nil {
			t.Fatal(err)
		}
	}
	for _, st := range []*Store{alice, bob, alice} {
		if err := st.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	for name, st := range map[string]*Store{"alice": alice, "bob": bob} {
		data, err := os.ReadFile(filepath.Join(st.TicketDir("JIG-1"), "journal.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range append([]string{"base"}, want...) {
			if n := strings.Count(string(data), `"event":"`+event+`"`); n != 1 {
				t.Errorf("%s journal has %q %d times, want once:\n%s", name, event, n, data)
			}
		}
	}
}

// TestDedupeLines: a line is cut back to the most times either side had it,
// the first places are kept, blank lines and line endings are left alone, and
// a merge that doubled nothing is left as it is.
func TestDedupeLines(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, merged, ours, theirs, want string
		did                              bool
	}{
		{"nothing doubled", "a\nb\nc\n", "a\nb\n", "a\nc\n", "a\nb\nc\n", false},
		{"a line in both sides comes out once", "a\nb\nc\nb\n", "a\nb\n", "a\nc\nb\n", "a\nb\nc\n", true},
		{"a line either side had twice stays twice", "a\na\nb\na\n", "a\na\n", "a\nb\n", "a\na\nb\n", true},
		{"pre-existing duplicates are not touched", "x\nx\ny\n", "x\nx\n", "x\nx\ny\n", "x\nx\ny\n", false},
		{"blank lines and CRLF", "a\r\n\r\nb\r\na\r\n", "a\r\n", "b\r\na\r\n", "a\r\n\r\nb\r\n", true},
		{"no final newline", "a\nb\na", "a\n", "b\na", "a\nb\n", true},
	} {
		got, did := dedupeLines(c.merged, c.ours, c.theirs)
		if did != c.did || (did && got != strings.ReplaceAll(c.want, "\n\n", "\n\n")) || (!did && got != c.merged) {
			t.Errorf("%s: dedupeLines = %q, %v; want %q, %v", c.name, got, did, c.want, c.did)
		}
	}
}

// TestWritersConflictInAnotherFileIsRefused: the union driver is for NDJSON
// files. Two writers who edited the same line of another file get a
// STORE_CONFLICT, the merge is aborted, and the clone is left clean on its
// own branch.
func TestWritersConflictInAnotherFileIsRefused(t *testing.T) {
	t.Parallel()
	alice, aliceDir, remote := newWritersStore(t)
	mintTicket(t, alice, "one")
	if err := alice.Push("jig: base"); err != nil {
		t.Fatal(err)
	}
	bob, bobDir := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}

	write := func(dir, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "tickets", "JIG-1", "brief.md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(aliceDir, "alice version\n")
	write(bobDir, "bob version\n")
	if err := alice.Push("jig: alice brief"); err != nil {
		t.Fatal(err)
	}
	if err := bob.Push("jig: bob brief"); err != nil {
		t.Fatal(err)
	}
	headBefore := strings.TrimSpace(runGit(t, aliceDir, "rev-parse", "HEAD"))
	err := alice.Sync()
	if axiCode(err) != "STORE_CONFLICT" {
		t.Fatalf("Sync with a conflicting brief = %v, want STORE_CONFLICT", err)
	}
	if !strings.Contains(err.Error(), "brief.md") {
		t.Errorf("the refusal does not name brief.md: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, aliceDir, "rev-parse", "HEAD")); got != headBefore {
		t.Errorf("alice HEAD moved from %s to %s over a refused merge", headBefore, got)
	}
	if out := strings.TrimSpace(runGit(t, aliceDir, "status", "--porcelain")); out != "" {
		t.Errorf("the refused merge left alice dirty: %q", out)
	}
}

// TestWriterRewrittenIsWarnedAndNotMerged: a writer branch whose new tip does
// not descend from the one merged last is skipped with a WRITER_REWRITTEN
// warning, never merged and never repaired; the other writers still merge.
func TestWriterRewrittenIsWarnedAndNotMerged(t *testing.T) {
	t.Parallel()
	alice, aliceDir, remote := newWritersStore(t)
	bob, bobDir := newWriter(t, remote, "bob")
	carol, _ := newWriter(t, remote, "carol")

	mintTicket(t, bob, "from bob")
	if err := bob.Push("jig: bob"); err != nil {
		t.Fatal(err)
	}
	if err := alice.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(alice.TicketDir("JIG-1")); err != nil {
		t.Fatalf("alice did not merge bob: %v", err)
	}
	aliceHead := strings.TrimSpace(runGit(t, aliceDir, "rev-parse", "HEAD"))

	// Bob rewrites his branch: reset to before his ticket, a different commit,
	// force-pushed.
	runGit(t, bobDir, "reset", "--hard", "HEAD~1")
	if err := os.WriteFile(filepath.Join(bobDir, "rewritten.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, bobDir, "add", "-A")
	runGit(t, bobDir, "commit", "-m", "rewritten")
	runGit(t, bobDir, "push", "--force", "origin", "writers/bob")
	// Carol is a writer too, and her work still merges.
	mintTicket(t, carol, "from carol")
	if err := carol.Push("jig: carol"); err != nil {
		t.Fatal(err)
	}

	var warnings []string
	alice.Warn = func(format string, args ...any) { warnings = append(warnings, format) }
	if err := alice.Sync(); err != nil {
		t.Fatalf("Sync over a rewritten branch: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "WRITER_REWRITTEN") {
		t.Fatalf("warnings = %q, want one WRITER_REWRITTEN", warnings)
	}
	if _, err := os.Stat(filepath.Join(aliceDir, "rewritten.txt")); err == nil {
		t.Error("alice merged the rewritten branch")
	}
	if out := runGit(t, aliceDir, "branch", "--contains", strings.TrimSpace(runGit(t, "", "--git-dir", remote, "rev-parse", "refs/heads/writers/bob"))); strings.Contains(out, "writers/alice") {
		t.Error("alice branch contains the rewritten tip of bob")
	}
	if _, err := os.Stat(alice.TicketDir("JIG-2")); err != nil {
		t.Errorf("carol ticket did not merge while bob was skipped: %v", err)
	}
	// Nothing repaired: bob old work is still in alice history, her HEAD only
	// moved by carol.
	if ok, _ := gitx.IsAncestor(aliceDir, aliceHead, "HEAD"); !ok {
		t.Error("alice history lost what she had merged from bob before the rewrite")
	}
}

// TestWriterNameTakenWhenTwoClonesDiverge: two clones under one writer name
// that both committed are refused before anything is merged or pushed; a
// clone that is only behind its own name takes the other forward.
func TestWriterNameTakenWhenTwoClonesDiverge(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	mintTicket(t, alice, "one")
	if err := alice.Push("jig: one"); err != nil {
		t.Fatal(err)
	}
	second, secondDir := newWriter(t, remote, "alice") // a second clone of the same writer
	if err := second.Sync(); err != nil {
		t.Fatalf("second clone of alice first Sync: %v", err)
	}
	if _, err := os.Stat(second.TicketDir("JIG-1")); err != nil {
		t.Fatalf("the second alice does not have the first ticket: %v", err)
	}

	// The first alice goes on; the second is only behind: it follows.
	mintTicket(t, alice, "two")
	if err := alice.Push("jig: two"); err != nil {
		t.Fatal(err)
	}
	if err := second.Sync(); err != nil {
		t.Fatalf("a clone that is only behind its own name: %v", err)
	}
	if _, err := os.Stat(second.TicketDir("JIG-2")); err != nil {
		t.Errorf("the second alice did not follow: %v", err)
	}

	// Now both commit: diverged.
	mintTicket(t, alice, "three from the first")
	if err := alice.Push("jig: three"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondDir, "second.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := second.Push("jig: second clone work")
	if axiCode(err) != "WRITER_NAME_TAKEN" {
		t.Fatalf("Push from a diverged clone of one writer name = %v, want WRITER_NAME_TAKEN", err)
	}
	if !strings.Contains(err.Error(), "alice") {
		t.Errorf("the refusal does not name the writer: %v", err)
	}
}

// TestMintReservesIdsOnTheClaimsBranch: two writers mint at once from stale
// views; the claims branch hands each a different id, the one that lost the
// race re-mints, and every id is on the log once.
func TestMintReservesIdsOnTheClaimsBranch(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	bob, _ := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}

	// Neither has seen the other: both would mint JIG-1 from their folders.
	a := mintTicket(t, alice, "alice one")
	b := mintTicket(t, bob, "bob one")
	if a == b {
		t.Fatalf("both writers minted %s", a)
	}
	got := map[string]bool{a: true, b: true}
	if !got["JIG-1"] || !got["JIG-2"] {
		t.Errorf("ids = %s and %s, want JIG-1 and JIG-2", a, b)
	}
	for name, st := range map[string]*Store{"alice": alice, "bob": bob} {
		if err := st.Sync(); err != nil {
			t.Fatalf("%s Sync: %v", name, err)
		}
		if ids := claimIDs(t, st); strings.Join(ids, ",") != "JIG-1,JIG-2" && strings.Join(ids, ",") != "JIG-2,JIG-1" {
			t.Errorf("%s sees claimed ids %v, want JIG-1 and JIG-2 once each", name, ids)
		}
		for _, id := range []string{"JIG-1", "JIG-2"} {
			if _, err := os.Stat(st.TicketDir(id)); err != nil {
				t.Errorf("%s does not have %s after syncing: %v", name, id, err)
			}
		}
	}
}

// TestMintSkipsAnIdAnotherWriterReserved: an id on the claims log that has no
// folder here yet (the other writer has not pushed its ticket, or this clone
// has not synced it) still counts, so nextID goes past it.
func TestMintSkipsAnIdAnotherWriterReserved(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	bob, _ := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := bob.AppendClaim(ClaimLine{Kind: ClaimID, Ticket: "JIG-1"}); err != nil {
		t.Fatal(err)
	}
	if err := bob.AppendClaim(ClaimLine{Kind: ClaimID, Ticket: "JIG-2"}); err != nil {
		t.Fatal(err)
	}
	if err := alice.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := mintTicket(t, alice, "alice"); got != "JIG-3" {
		t.Errorf("alice minted %s, want JIG-3: JIG-1 and JIG-2 are reserved on the claims branch", got)
	}
}

// TestAppendClaimRetriesAfterARejectedPush: a writer whose view of the claims
// branch is stale has its push rejected, fetches, decides again and lands its
// line after the one that beat it.
func TestAppendClaimRetriesAfterARejectedPush(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	bob, _ := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := alice.AppendClaim(ClaimLine{Kind: ClaimID, Ticket: "JIG-8"}); err != nil {
		t.Fatal(err)
	}
	// Bob has not fetched since: his append goes on a stale tip.
	if err := bob.AppendClaim(ClaimLine{Kind: ClaimID, Ticket: "JIG-9"}); err != nil {
		t.Fatalf("a stale append: %v", err)
	}
	remoteLog := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "show", "refs/heads/claims:claims.ndjson"))
	lines := parseClaims(remoteLog)
	if len(lines) != 2 || lines[0].Ticket != "JIG-8" || lines[1].Ticket != "JIG-9" || lines[0].Writer != "alice" || lines[1].Writer != "bob" {
		t.Errorf("the log on the origin = %+v, want alice JIG-8 then bob JIG-9", lines)
	}
	// One line per commit, a straight history.
	if n := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "rev-list", "--count", "refs/heads/claims")); n != "3" {
		t.Errorf("the claims branch has %s commits, want 3 (root and two lines)", n)
	}
}

// TestReserveIDIsRefusedForAnIdTheLogHolds: the loser of a race for one id
// learns it is taken, and nothing of its own is on the log.
func TestReserveIDIsRefusedForAnIdTheLogHolds(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	bob, _ := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}
	if taken, err := alice.reserveID("JIG-5"); err != nil || taken {
		t.Fatalf("alice reserveID = %v, %v; want it reserved", taken, err)
	}
	// Bob's view lacks JIG-5: the push is rejected, the fetch shows the line.
	taken, err := bob.reserveID("JIG-5")
	if err != nil || !taken {
		t.Fatalf("bob reserveID of the same id = %v, %v; want taken", taken, err)
	}
	if ids := claimIDs(t, bob); len(ids) != 1 || ids[0] != "JIG-5" {
		t.Errorf("bob sees ids %v after losing, want JIG-5 once (alice)", ids)
	}
	lines, _ := bob.Claims()
	if lines[len(lines)-1].Writer != "alice" {
		t.Errorf("the line for JIG-5 is %+v, want alice", lines[len(lines)-1])
	}
}

// TestAppendClaimNotPushedLeavesNothingBehind: an append that cannot reach the
// origin is refused, and the claims branch of the clone is as it was - not a
// claim until the origin has it.
func TestAppendClaimNotPushedLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	alice, aliceDir, remote := newWritersStore(t)
	before := strings.TrimSpace(runGit(t, aliceDir, "rev-parse", "refs/heads/claims"))
	if err := os.Rename(remote, remote+".gone"); err != nil {
		t.Fatal(err)
	}
	err := alice.AppendClaim(ClaimLine{Kind: ClaimID, Ticket: "JIG-4"})
	if axiCode(err) != claimsNotPushedCode {
		t.Fatalf("AppendClaim with the origin gone = %v, want %s", err, claimsNotPushedCode)
	}
	if got := strings.TrimSpace(runGit(t, aliceDir, "rev-parse", "refs/heads/claims")); got != before {
		t.Errorf("the claims branch moved from %s to %s over a push that failed", before, got)
	}
	if ids := claimIDs(t, alice); len(ids) != 0 {
		t.Errorf("claim ids after the failed append = %v, want none", ids)
	}
}

// TestDriver: one writer drives a ticket; another is refused with DRIVER_HELD
// unless it takes the ticket over; a release frees it; a writer already
// driving writes nothing.
func TestDriver(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	bob, _ := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}

	if err := alice.ClaimDriver("JIG-1", "s-a", false); err != nil {
		t.Fatalf("alice ClaimDriver: %v", err)
	}
	d, ok, err := alice.Driver("JIG-1")
	if err != nil || !ok || d.Writer != "alice" || d.Session != "s-a" || d.Author != "tester" || d.TS == "" {
		t.Fatalf("Driver = %+v, %v, %v; want alice, session s-a, authored by tester", d, ok, err)
	}

	tip := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "rev-parse", "refs/heads/claims"))
	if err := alice.ClaimDriver("JIG-1", "s-other", false); err != nil {
		t.Fatalf("alice again, other session: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "rev-parse", "refs/heads/claims")); got != tip {
		t.Error("a writer already driving the ticket wrote another line")
	}

	err = bob.ClaimDriver("JIG-1", "s-b", false)
	if axiCode(err) != DriverHeldCode || !strings.Contains(err.Error(), "alice") {
		t.Fatalf("bob ClaimDriver = %v, want DRIVER_HELD naming alice", err)
	}
	if d, ok, _ := bob.Driver("JIG-1"); !ok || d.Writer != "alice" {
		t.Errorf("after the refusal bob sees driver %+v, want alice", d)
	}

	if err := bob.ClaimDriver("JIG-1", "s-b", true); err != nil {
		t.Fatalf("bob ClaimDriver --take: %v", err)
	}
	if d, ok, _ := alice.Driver("JIG-1"); ok && d.Writer == "bob" {
		t.Error("alice sees bob before she fetched")
	}
	if err := alice.Sync(); err != nil {
		t.Fatal(err)
	}
	if d, ok, _ := alice.Driver("JIG-1"); !ok || d.Writer != "bob" || d.Session != "s-b" {
		t.Errorf("after the takeover alice sees %+v, want bob", d)
	}

	// Alice releasing what she no longer drives writes nothing; bob releasing
	// frees the ticket.
	tip = strings.TrimSpace(runGit(t, "", "--git-dir", remote, "rev-parse", "refs/heads/claims"))
	if err := alice.ReleaseDriver("JIG-1", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "rev-parse", "refs/heads/claims")); got != tip {
		t.Error("alice released a drive she did not hold, and wrote a line")
	}
	if err := bob.ReleaseDriver("JIG-1", "s-b"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := bob.Driver("JIG-1"); ok {
		t.Error("the ticket still has a driver after its release")
	}
	if err := alice.ClaimDriver("JIG-1", "s-a", false); err != nil {
		t.Errorf("alice ClaimDriver after the release: %v", err)
	}
}

// TestDriverRefusalLooksAtTheOriginFirst: a writer whose view of the claims
// log is old is not refused by it - the driver it shows may have let go since.
func TestDriverRefusalLooksAtTheOriginFirst(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	bob, _ := newWriter(t, remote, "bob")
	if err := alice.ClaimDriver("JIG-1", "s-a", false); err != nil {
		t.Fatal(err)
	}
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := alice.ReleaseDriver("JIG-1", "s-a"); err != nil {
		t.Fatal(err)
	}
	// Bob's view still shows alice driving it.
	if d, ok, _ := bob.Driver("JIG-1"); !ok || d.Writer != "alice" {
		t.Fatalf("bob sees %+v, %v before the claim; the test needs the stale view", d, ok)
	}
	if err := bob.ClaimDriver("JIG-1", "s-b", false); err != nil {
		t.Fatalf("bob ClaimDriver after alice let go: %v", err)
	}
	if d, ok, _ := bob.Driver("JIG-1"); !ok || d.Writer != "bob" {
		t.Errorf("Driver = %+v, %v; want bob", d, ok)
	}
}

// TestDriverRaceHasOneWinner: two writers claim the same undriven ticket from
// views that both show it free; the claims branch lets one in and refuses the
// other.
func TestDriverRaceHasOneWinner(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	bob, _ := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := alice.ClaimDriver("JIG-1", "s-a", false); err != nil {
		t.Fatal(err)
	}
	// Bob never fetched: his view says free, his push is rejected, the retry
	// sees alice.
	err := bob.ClaimDriver("JIG-1", "s-b", false)
	if axiCode(err) != DriverHeldCode {
		t.Fatalf("the second claimant = %v, want DRIVER_HELD", err)
	}
}

// TestDriverInASingleMainStore: with no claims branch the driver lives in
// claims.ndjson at the store root on main, committed and pushed like any
// claim; a second clone is refused, and takes over.
func TestDriverInASingleMainStore(t *testing.T) {
	t.Parallel()
	alice, aliceDir, remote := newTestRemoteStore(t)
	runGit(t, aliceDir, "config", "jig.writer", "alice")
	bob, bobDir := newWriter(t, remote, "bob")

	if err := alice.ClaimDriver("JIG-1", "s-a", false); err != nil {
		t.Fatalf("alice ClaimDriver: %v", err)
	}
	if on, _ := alice.Mode(); on {
		t.Fatal("claiming a driver made the store a writers store")
	}
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "show", "refs/heads/main:claims.ndjson")); !strings.Contains(got, `"writer":"alice"`) || !strings.Contains(got, `"kind":"driver"`) {
		t.Errorf("claims.ndjson on the origin main = %q, want alice driver line", got)
	}
	if got := strings.TrimSpace(runGit(t, "", "--git-dir", remote, "log", "-1", "--pretty=%s", "refs/heads/main")); !strings.HasPrefix(got, "jig: driver JIG-1") {
		t.Errorf("origin main subject = %q", got)
	}

	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := bob.ClaimDriver("JIG-1", "s-b", false); axiCode(err) != DriverHeldCode {
		t.Fatalf("bob ClaimDriver = %v, want DRIVER_HELD", err)
	}
	if err := bob.ClaimDriver("JIG-1", "s-b", true); err != nil {
		t.Fatalf("bob --take: %v", err)
	}
	if d, ok, _ := bob.Driver("JIG-1"); !ok || d.Writer != "bob" {
		t.Errorf("Driver = %+v, want bob", d)
	}
	if err := alice.Sync(); err != nil {
		t.Fatal(err)
	}
	if d, ok, _ := alice.Driver("JIG-1"); !ok || d.Writer != "bob" {
		t.Errorf("alice sees %+v after syncing, want bob", d)
	}
	if out := strings.TrimSpace(runGit(t, bobDir, "status", "--porcelain")); out != "" {
		t.Errorf("bob store is dirty after the claim: %q", out)
	}
}

// TestSingleMainClaimsRaceLosesToTheFirstPush: two single-main writers claim
// one ticket from views that both show it free. The second one's push is
// rejected, Claim undoes and pulls, and the decision made over refuses it.
func TestSingleMainClaimsRaceLosesToTheFirstPush(t *testing.T) {
	t.Parallel()
	alice, aliceDir, remote := newTestRemoteStore(t)
	runGit(t, aliceDir, "config", "jig.writer", "alice")
	bob, _ := newWriter(t, remote, "bob")
	if err := alice.ClaimDriver("JIG-1", "s-a", false); err != nil {
		t.Fatal(err)
	}
	// Bob has not pulled: the file he reads is empty.
	err := bob.ClaimDriver("JIG-1", "s-b", false)
	if axiCode(err) != DriverHeldCode {
		t.Fatalf("bob ClaimDriver from a stale view = %v, want DRIVER_HELD after the retry", err)
	}
	if out := strings.TrimSpace(runGit(t, bob.Root, "status", "--porcelain")); out != "" {
		t.Errorf("the lost race left bob dirty: %q", out)
	}
}
