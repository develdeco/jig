package e2e

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
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
	// How often a loser fetches, merges and pushes again is the scheduler's
	// choice, not the code's, so the race runs without git's trace: the
	// git-process budget counts only work that is the same on every run.
	race := w
	race.alice, race.bob = w.alice.without(gitTraceEnv), w.bob.without(gitTraceEnv)

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
					results <- minted{who, race.jigAlice(t, "ticket", "new", "--title", title)}
				} else {
					results <- minted{who, race.jigBob(t, "ticket", "new", "--title", title)}
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
