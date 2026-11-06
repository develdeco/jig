package verify

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/outcome"
)

// TestRecorderKeepsTheChangesOwnScenariosFirst: a passing run that holds more
// recordings than the collection keeps keeps the recordings of a test file the
// branch changed first, whatever their names sort to, then the rest by name.
// The recordings left out are removed from disk and counted on the line.
func TestRecorderKeepsTheChangesOwnScenariosFirst(t *testing.T) {
	t.Parallel()
	files := manyFiles(maxRecordings + 5)
	names := sortedNames(files)
	// The last three by name belong to a test file the branch changed.
	own := names[len(names)-3:]
	for _, name := range own {
		files[name+".json"] = `{"file":"e2e/own_test.go"}`
	}
	for _, name := range names[:len(names)-3] {
		files[name+".json"] = `{"file":"e2e/other_test.go"}`
	}
	r := newRecordRun(t, &recordingOracle{files: files})
	r.rec.changed = func() []string { return []string{"e2e/own_test.go", "README.md"} }
	if res := r.atGreen(); res.Outcome != outcome.Green {
		t.Fatalf("outcome = %q, want green", res.Outcome)
	}
	rec := recordedLines(r.journaled())
	if len(rec) != 1 || len(rec[0].Recordings) != maxRecordings {
		t.Fatalf("recorded lines = %+v, want one keeping %d recordings", rec, maxRecordings)
	}
	kept := map[string]bool{}
	for _, got := range rec[0].Recordings {
		kept[got.File] = true
	}
	for _, name := range own {
		if !kept[name] {
			t.Errorf("%s is the changed file's scenario and was left out", name)
		}
	}
	// The rest are the first by name, and the 5 beyond them are gone.
	for i, name := range names[:maxRecordings-len(own)] {
		if !kept[name] {
			t.Errorf("recording %d (%s) was left out, want the first by name kept", i, name)
			break
		}
	}
	if want := fmt.Sprintf("5 recordings beyond the first %d left out", maxRecordings); rec[0].Outcome != want {
		t.Errorf("Outcome = %q, want %q", rec[0].Outcome, want)
	}
	for i := 1; i < len(rec[0].Recordings); i++ {
		if strings.Compare(rec[0].Recordings[i-1].File, rec[0].Recordings[i].File) >= 0 {
			t.Fatalf("recordings are not in name order at %d", i)
		}
	}
	for _, name := range names[maxRecordings-len(own) : len(names)-3] {
		mustNotExist(t, filepath.Join(r.recordDir(firstRun), name))
		mustNotExist(t, filepath.Join(r.recordDir(firstRun), name+".json"))
	}
}
