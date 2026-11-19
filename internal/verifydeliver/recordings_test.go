package verifydeliver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// recordingsLease is a lease whose history a test records into: seed <- c1 <-
// c2 on main, with c2 the head the reviewer reviews, and a commit s1 off the
// seed that main does not hold.
type recordingsLease struct {
	dir          string
	seed, c1, c2 string
	s1           string
}

func newRecordingsLease(t *testing.T) recordingsLease {
	t.Helper()
	l := recordingsLease{dir: newReviewLease(t, "main")}
	var err error
	if l.seed, err = gitx.RevParse(l.dir, "HEAD"); err != nil {
		t.Fatal(err)
	}
	writeReviewFile(t, l.dir, "a.go", "a")
	l.c1 = commitReviewLease(t, l.dir, "c1")
	writeReviewFile(t, l.dir, "b.go", "b")
	l.c2 = commitReviewLease(t, l.dir, "c2")
	if _, err := gitx.RunEnv(l.dir, reviewGitEnv, "checkout", "-b", "side", l.seed); err != nil {
		t.Fatal(err)
	}
	writeReviewFile(t, l.dir, "side.go", "side")
	l.s1 = commitReviewLease(t, l.dir, "s1")
	if _, err := gitx.RunEnv(l.dir, reviewGitEnv, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	return l
}

// journalLines appends lines to ticket's journal in the store.
func journalLines(t *testing.T, st *store.Store, ticket string, lines ...journal.Line) {
	t.Helper()
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if err := journal.Append(st, ticket, l); err != nil {
			t.Fatalf("journal.Append: %v", err)
		}
	}
}

// roundInput is the reviewer round's input over l, with the jig home jigHome
// ("" for none).
func (l recordingsLease) roundInput(st *store.Store, jigHome string) RoundInput {
	return RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, CloneDir: l.dir,
		RepoName: "fixture-repo", Target: "main",
		Manifest: oneOracleManifest(), Home: jigHome,
	}
}

// reviewListPath is where the round's recordings.json belongs.
func reviewListPath(t *testing.T, st *store.Store, jigHome string, round int) string {
	t.Helper()
	id, err := st.ID()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := home.ReviewDir(jigHome, id, "JIG-1", round)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "recordings.json")
}

// recordedList reads the recordings.json a round wrote at path.
func recordedList(t *testing.T, path string) ReviewRecordings {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recordings.json: %v", err)
	}
	var list ReviewRecordings
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("parse recordings.json: %v\n%s", err, data)
	}
	return list
}

// hostSpellingsIn reports which spellings of dir (as the machine's paths are
// scrubbed, the WSL mount among them) s still holds.
func hostSpellingsIn(s, dir string) []string {
	var found []string
	for _, sp := range hostPathSpellings(true, hostDir{dir, "x"}) {
		if strings.Contains(s, sp.text) {
			found = append(found, sp.text)
		}
	}
	return found
}
