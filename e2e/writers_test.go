package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// writersWorld is the fixture's store with two people at it: alice, whose clone
// is the fixture's own, and bob, whose clone is a plain git clone of the same
// origin under a jig home of his own. Both clones name their writer in git
// config. With writers set, alice has moved the store to writers mode
// (jig store migrate --writers) before bob cloned it, so bob's clone is made
// of a store that already is one.
type writersWorld struct {
	fx       *fixture.Fixture
	alice    jigEnv
	bob      jigEnv
	aliceDir string // cwd alice runs jig in: her product repo clone
	bobStore string // bob's store clone, which is also his cwd
}

func newWritersWorld(t *testing.T, writers bool) writersWorld {
	t.Helper()
	fx, aliceEnv := newFixture(t, fixture.Opts{})
	aliceEnv = aliceEnv.without("JIG_SLICE", "JIG_SESSION")
	gitLog(t, fx.StoreDir, "config", "jig.writer", "alice")
	if writers {
		r := runJig(t, aliceEnv, fx.RepoDir, "store", "migrate", "--writers")
		if r.Code != 0 || !strings.Contains(r.Stdout, "claims: created") {
			t.Fatalf("store migrate --writers: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
	}
	bobStore := filepath.Join(t.TempDir(), "bob-store")
	gitLog(t, "", "clone", fx.StoreRemote, bobStore)
	gitLog(t, bobStore, "config", "jig.writer", "bob")
	bobEnv := aliceEnv
	bobEnv.home, bobEnv.userHome = t.TempDir(), t.TempDir()
	return writersWorld{fx: fx, alice: aliceEnv, bob: bobEnv, aliceDir: fx.RepoDir, bobStore: bobStore}
}

// jigAlice and jigBob run jig as one of the two.
func (w writersWorld) jigAlice(t *testing.T, args ...string) jigResult {
	t.Helper()
	return runJig(t, w.alice, w.aliceDir, args...)
}

func (w writersWorld) jigBob(t *testing.T, args ...string) jigResult {
	t.Helper()
	return runJig(t, w.bob, w.bobStore, append(args, "--store", w.bobStore)...)
}

var ticketIDLine = regexp.MustCompile(`(?m)^  id: (\S+)$`)

// TestWritersMintRace: two clones of a writers store each mint five tickets
// at the same time, ten jig processes at once. Every id is different, none is
// lost, and after each clone syncs both list all of them.
func TestWritersMintRace(t *testing.T) {
	t.Parallel()
	w := newWritersWorld(t, true)

	type minted struct {
		who string
		res jigResult
	}
	results := make(chan minted, 10)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		for _, who := range []string{"alice", "bob"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				title := who + " ticket"
				if who == "alice" {
					results <- minted{who, w.jigAlice(t, "ticket", "new", "--title", title)}
				} else {
					results <- minted{who, w.jigBob(t, "ticket", "new", "--title", title)}
				}
			}()
		}
	}
	wg.Wait()
	close(results)

	ids := map[string]string{}
	for m := range results {
		if m.res.Code != 0 {
			t.Errorf("%s ticket new: exit %d\n%s%s", m.who, m.res.Code, m.res.Stdout, m.res.Stderr)
			continue
		}
		match := ticketIDLine.FindStringSubmatch(m.res.Stdout)
		if match == nil {
			t.Errorf("%s ticket new printed no id:\n%s", m.who, m.res.Stdout)
			continue
		}
		if other, dup := ids[match[1]]; dup {
			t.Errorf("%s was minted by %s and by %s", match[1], other, m.who)
		}
		ids[match[1]] = m.who
	}
	if len(ids) != 10 {
		t.Fatalf("minted %d distinct ids %v, want 10", len(ids), keysOfStrings(ids))
	}
	// The fixture has JIG-1; the ten are the next ten, none skipped.
	var want []string
	for n := 2; n <= 11; n++ {
		want = append(want, "JIG-"+strconv.Itoa(n))
	}
	got := keysOfStrings(ids)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want JIG-2 to JIG-11", got)
	}

	// Each clone syncs once; then both list the ten and the fixture ticket.
	for name, dir := range map[string]string{"alice": w.fx.StoreDir, "bob": w.bobStore} {
		st, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Sync(); err != nil {
			t.Fatalf("%s Sync: %v", name, err)
		}
	}
	for name, dir := range map[string]string{"alice": w.fx.StoreDir, "bob": w.bobStore} {
		st, _ := store.Open(dir)
		listed, err := st.TicketIDs()
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(listed)
		wantAll := append([]string{"JIG-1"}, want...)
		sort.Strings(wantAll)
		if strings.Join(listed, ",") != strings.Join(wantAll, ",") {
			t.Errorf("%s lists %v after the sync, want JIG-1 to JIG-11", name, listed)
		}
	}
	// And the binary agrees: status of a ticket the other minted.
	for id, who := range ids {
		var r jigResult
		if who == "alice" {
			r = w.jigBob(t, "status", id)
		} else {
			r = w.jigAlice(t, "status", id)
		}
		if r.Code != 0 {
			t.Errorf("status %s (minted by %s) in the other clone: exit %d\n%s", id, who, r.Code, r.Stdout)
		}
	}

	// The claims branch holds each id once, and main was never pushed.
	claims := gitLog(t, w.fx.StoreRemote, "show", "refs/heads/claims:claims.ndjson")
	for _, id := range append([]string{"JIG-1"}, want...) {
		if n := strings.Count(claims, `"ticket":"`+id+`"`); n != 1 {
			t.Errorf("the claims branch has %d lines for %s, want 1", n, id)
		}
	}
	if out := gitLog(t, w.fx.StoreRemote, "for-each-ref", "--format=%(refname)", "refs/heads/"); out !=
		"refs/heads/claims\nrefs/heads/main\nrefs/heads/writers/alice\nrefs/heads/writers/bob" {
		t.Errorf("branches on the origin = %q", out)
	}
}

func keysOfStrings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestStatusNamesDriver: the writer that claims a ticket drives it, and
// `jig status` names that writer; another writer's claim is refused with
// DRIVER_HELD and the command that takes over; --take takes it and status
// names the new driver. It holds in a writers store and, backed by
// claims.ndjson on main, in a single-main one.
func TestStatusNamesDriver(t *testing.T) {
	t.Parallel()
	for _, mode := range []struct {
		name    string
		writers bool
	}{{"writers", true}, {"single-main", false}} {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			w := newWritersWorld(t, mode.writers)
			T := w.fx.Ticket

			if r := w.jigAlice(t, "status", T); strings.Contains(r.Stdout, "driver:") {
				t.Fatalf("a ticket nobody drives has a driver block:\n%s", r.Stdout)
			}
			r := w.jigAlice(t, "claim", T, "--slice", "a", "--session", "s-alice")
			if r.Code != 0 {
				t.Fatalf("alice claim: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
			}
			r = w.jigAlice(t, "status", T)
			for _, want := range []string{"driver:\n", "  ticket: " + T + "\n", "  writer: alice\n", "  session: s-alice\n"} {
				if !strings.Contains(r.Stdout, want) {
					t.Errorf("alice status lacks %q:\n%s", want, r.Stdout)
				}
			}

			// Bob, on another slice, finds the ticket driven. The refusal comes
			// from the log every writer shares, not from his machine.
			r = w.jigBob(t, "claim", T, "--slice", "b", "--session", "s-bob")
			if r.Code != 1 || !strings.Contains(r.Stdout, "code: DRIVER_HELD") || !strings.Contains(r.Stdout, "alice") {
				t.Fatalf("bob claim: exit %d, want 1 with DRIVER_HELD naming alice\n%s%s", r.Code, r.Stdout, r.Stderr)
			}
			if !strings.Contains(r.Stdout, "jig claim "+T+" --slice b --take") {
				t.Errorf("the refusal does not give the command that takes over:\n%s", r.Stdout)
			}
			if r = w.jigBob(t, "status", T); !strings.Contains(r.Stdout, "  writer: alice\n") {
				t.Errorf("bob status does not name alice:\n%s", r.Stdout)
			}
			// Nothing of bob's claim was written to his journal.
			bobStore, err := store.Open(w.bobStore)
			if err != nil {
				t.Fatal(err)
			}
			if lines, err := journal.Read(bobStore, T); err != nil {
				t.Fatal(err)
			} else {
				for _, l := range lines {
					if l.Event == journal.EventClaim && l.Session == "s-bob" {
						t.Errorf("the refused claim of bob left a claim line: %+v", l)
					}
				}
			}

			r = w.jigBob(t, "claim", T, "--slice", "b", "--session", "s-bob", "--take")
			if r.Code != 0 || !strings.Contains(r.Stdout, "result: claimed") {
				t.Fatalf("bob claim --take: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
			}
			if r = w.jigBob(t, "status", T); !strings.Contains(r.Stdout, "  writer: bob\n") || strings.Contains(r.Stdout, "writer: alice") {
				t.Errorf("bob status after taking over:\n%s", r.Stdout)
			}

			// Alice learns of it the next time she touches the store: her claim
			// on another slice is refused, naming bob.
			r = w.jigAlice(t, "claim", T, "--slice", "c", "--session", "s-alice")
			if r.Code != 1 || !strings.Contains(r.Stdout, "code: DRIVER_HELD") || !strings.Contains(r.Stdout, "bob") {
				t.Errorf("alice claim after bob took over: exit %d, want 1 with DRIVER_HELD naming bob\n%s", r.Code, r.Stdout)
			}
			if r = w.jigAlice(t, "status", T); !strings.Contains(r.Stdout, "  writer: bob\n") {
				t.Errorf("alice status after syncing does not name bob:\n%s", r.Stdout)
			}

			// Bob lets go of his only slice: the ticket is free, alice can drive.
			if r = w.jigBob(t, "claim", T, "--slice", "b", "--session", "s-bob", "--release"); r.Code != 0 {
				t.Fatalf("bob release: exit %d\n%s", r.Code, r.Stdout)
			}
			if r = w.jigBob(t, "status", T); strings.Contains(r.Stdout, "driver:") {
				t.Errorf("bob status after his release still has a driver:\n%s", r.Stdout)
			}
			if r = w.jigAlice(t, "claim", T, "--slice", "c", "--session", "s-alice"); r.Code != 0 {
				t.Errorf("alice claim after bob let go: exit %d\n%s", r.Code, r.Stdout)
			}
		})
	}
}

// TestRefusedClaimTakesNoDriver: a claim refused unattended for its
// conditions leaves no driver behind.
func TestRefusedClaimTakesNoDriver(t *testing.T) {
	t.Parallel()
	w := newWritersWorld(t, true)
	st, err := store.Open(w.fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	writeBlockedTicket(t, st, "merged")
	approve(t, st)

	r := w.jigAlice(t, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1")
	if r.Code != 1 || !strings.Contains(r.Stdout, "CLAIM_BLOCKED_MERGED") {
		t.Fatalf("unattended claim: exit %d, want CLAIM_BLOCKED_MERGED\n%s", r.Code, r.Stdout)
	}
	if r = w.jigAlice(t, "status", "JIG-2"); strings.Contains(r.Stdout, "driver:") {
		t.Errorf("a refused claim took the driver:\n%s", r.Stdout)
	}
}

// TestRewrittenWriterRefused: a writer branch force-pushed to something that
// does not descend from what was merged is not merged: the next command
// of another writer warns WRITER_REWRITTEN, goes on with the rest, and the
// rewritten commits never reach its branch.
func TestRewrittenWriterRefused(t *testing.T) {
	t.Parallel()
	w := newWritersWorld(t, true)

	// Bob works; alice's next command merges it.
	r := w.jigBob(t, "ticket", "new", "--title", "bob work")
	if r.Code != 0 || !strings.Contains(r.Stdout, "id: JIG-2") {
		t.Fatalf("bob ticket new: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r = w.jigAlice(t, "ticket", "new", "--title", "alice work")
	if r.Code != 0 || strings.Contains(r.Stdout, "WRITER_REWRITTEN") {
		t.Fatalf("alice ticket new: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(w.fx.StoreDir, "tickets", "JIG-2")); err != nil {
		t.Fatalf("alice did not merge bob's ticket: %v", err)
	}
	aliceHead := gitLog(t, w.fx.StoreDir, "rev-parse", "HEAD")

	// Someone rewrites writers/bob: reset to main, a new commit, forced.
	scratch := filepath.Join(t.TempDir(), "scratch")
	gitLog(t, "", "clone", w.fx.StoreRemote, scratch)
	gitLog(t, scratch, "checkout", "-q", "-B", "writers/bob", "origin/main")
	if err := os.WriteFile(filepath.Join(scratch, "rewritten.txt"), []byte("not bob's history\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitLog(t, scratch, "add", "-A")
	gitLog(t, scratch, "commit", "-q", "-m", "rewritten")
	rewritten := gitLog(t, scratch, "rev-parse", "HEAD")
	gitLog(t, scratch, "push", "-q", "--force", "origin", "writers/bob")

	r = w.jigAlice(t, "ticket", "new", "--title", "alice work again")
	if r.Code != 0 {
		t.Fatalf("alice ticket new over a rewritten branch: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "WRITER_REWRITTEN") || !strings.Contains(r.Stdout, "writers/bob") {
		t.Errorf("alice was not warned WRITER_REWRITTEN about writers/bob:\n%s%s", r.Stdout, r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(w.fx.StoreDir, "rewritten.txt")); err == nil {
		t.Error("alice merged the rewritten branch")
	}
	if out := gitLog(t, w.fx.StoreDir, "branch", "--contains", rewritten); strings.Contains(out, "writers/alice") {
		t.Errorf("alice's branch contains the rewritten commit:\n%s", out)
	}
	// What she had merged before is still hers, and her new ticket is made.
	gitLog(t, w.fx.StoreDir, "merge-base", "--is-ancestor", aliceHead, "HEAD")
	if _, err := os.Stat(filepath.Join(w.fx.StoreDir, "tickets", "JIG-2")); err != nil {
		t.Errorf("alice lost bob's earlier ticket: %v", err)
	}
	// Nothing was repaired: the origin still has bob's branch as rewritten.
	if got := gitLog(t, w.fx.StoreRemote, "rev-parse", "refs/heads/writers/bob"); got != rewritten {
		t.Errorf("writers/bob on the origin = %s, want the rewritten %s untouched", got, rewritten)
	}
}
