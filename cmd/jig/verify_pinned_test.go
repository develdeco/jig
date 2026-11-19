package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verify"
)

const (
	pinnedLong  = "The retry loop waits twice as long after every failed attempt here."
	pinnedShort = "Too short to count."
	pinnedNoEnd = "This line has plenty of words but never reaches a final mark"
	pinnedKept  = "Another long sentence that the file itself keeps in its new version."
)

// pinnedRig is a verify rig whose clone holds, at its base, a doc that holds a
// long sentence, a short one, one with no final mark and one it will keep, and
// a second file quoting all four; the origin target sits at that base.
func pinnedRig(t *testing.T) *verifyRig {
	t.Helper()
	r := newFinalRig(t)
	r.commit("", "setup: a doc and a file that quotes it", map[string]string{
		"docs/notes.md": "# Notes\n" + pinnedLong + " " + pinnedShort + "\n" + pinnedNoEnd + "\n" + pinnedKept + "\n",
		"docs/quote.md": "see: " + pinnedLong + "\n" + pinnedShort + "\n" + pinnedNoEnd + "\n" + pinnedKept + "\n",
	})
	gitMust(t, r.fx.RepoDir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return r
}

// removeFromNotes is the slice's change: the notes lose the long, the short and
// the unfinished sentences and keep the other, which moves to another line.
func (r *verifyRig) removeFromNotes() string {
	r.t.Helper()
	return r.commit("a", "trim the notes", map[string]string{
		"docs/notes.md": "# Notes\nAdded: " + pinnedKept + "\n",
	})
}

// TestVerifyPinnedTextNamesTheFileThatStillQuotesRemovedText: a slice's run
// and the final run name the file:line of the file that still holds a removed
// sentence and the file it left, and only for the sentence that counts.
func TestVerifyPinnedTextNamesTheFileThatStillQuotesRemovedText(t *testing.T) {
	t.Parallel()
	r := pinnedRig(t)
	r.removeFromNotes()

	for _, args := range [][]string{{"verify"}, {"verify", "--final"}, {"verify", "--", "echo", "hi"}} {
		out, code := r.run(args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d, want 0:\n%s", args, code, out)
		}
		if !strings.Contains(out, "pinned_text") || !strings.Contains(out, "docs/quote.md:1") || !strings.Contains(out, "docs/notes.md") {
			t.Errorf("%v: output lacks the hit docs/quote.md:1 from docs/notes.md:\n%s", args, out)
		}
		for _, line := range []string{"docs/quote.md:2", "docs/quote.md:3", "docs/quote.md:4"} {
			if strings.Contains(out, line) {
				t.Errorf("%v: output names %s, which holds no taken sentence:\n%s", args, line, out)
			}
		}
	}
}

// TestVerifyPinnedTextNeverChangesTheRun: the exit code, the state, covers and
// the lines the run records are the same with a hit and without one.
func TestVerifyPinnedTextNeverChangesTheRun(t *testing.T) {
	t.Parallel()
	with := pinnedRig(t)
	with.removeFromNotes()
	without := pinnedRig(t)
	without.commit("a", "trim the notes and the quote", map[string]string{
		"docs/notes.md": "# Notes\nAdded: " + pinnedKept + "\n",
		"docs/quote.md": "gone\n",
	})

	a, codeA := with.run("verify")
	b, codeB := without.run("verify")
	if codeA != codeB {
		t.Fatalf("exit with a hit = %d, without = %d", codeA, codeB)
	}
	if !strings.Contains(a, "pinned_text") || strings.Contains(b, "pinned_text") {
		t.Fatalf("rows: with a hit:\n%s\nwithout:\n%s", a, b)
	}
	for _, want := range []string{"state: pass", "covers: yes"} {
		if !strings.Contains(a, want) || !strings.Contains(b, want) {
			t.Errorf("both runs should say %q:\n%s\n%s", want, a, b)
		}
	}
	kinds := func(r *verifyRig) []string {
		var out []string
		for _, l := range r.journal() {
			out = append(out, l.Event+":"+l.Outcome)
		}
		return out
	}
	if !reflect.DeepEqual(kinds(with), kinds(without)) {
		t.Errorf("journal with a hit = %v, without = %v", kinds(with), kinds(without))
	}
}

// TestVerifyPinnedTextShowsNothingWhenNothingPins: a change that only adds
// lines, one whose removed sentence nothing else holds, and a --dry-run show no
// rows, and --json carries an empty list.
func TestVerifyPinnedTextShowsNothingWhenNothingPins(t *testing.T) {
	t.Parallel()
	r := pinnedRig(t)
	r.commit("a", "only add", map[string]string{"docs/extra.md": "A new file with no removed text at all in it.\n"})
	out, code := r.run("verify")
	if code != 0 || strings.Contains(out, "pinned_text") {
		t.Fatalf("an add-only change: exit %d, output:\n%s", code, out)
	}

	r.commit("a", "remove what nothing holds", map[string]string{"docs/extra.md": "x\n", "docs/notes.md": "# Notes\n", "docs/quote.md": "nothing\n"})
	out, code = r.run("verify", "--json")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	var report struct {
		Pinned []map[string]any `json:"pinned"`
	}
	if err := json.Unmarshal([]byte(lastJSONLine(out)), &report); err != nil || report.Pinned == nil || len(report.Pinned) != 0 {
		t.Fatalf("--json pinned = %+v (err %v), want an empty list:\n%s", report.Pinned, err, out)
	}

	dry, code := r.run("verify", "--dry-run")
	if code != 0 || strings.Contains(dry, "pinned_text") {
		t.Fatalf("--dry-run: exit %d, output:\n%s", code, dry)
	}
}

// TestVerifyPinnedTextJSONCarriesTheRows: --json lists each hit with the file
// it left.
func TestVerifyPinnedTextJSONCarriesTheRows(t *testing.T) {
	t.Parallel()
	r := pinnedRig(t)
	r.removeFromNotes()
	out, code := r.run("verify", "--json")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	var report struct {
		Pinned []struct {
			File        string `json:"file"`
			Line        int    `json:"line"`
			RemovedFrom string `json:"removed_from"`
		} `json:"pinned"`
	}
	if err := json.Unmarshal([]byte(lastJSONLine(out)), &report); err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	if len(report.Pinned) != 1 || report.Pinned[0].File != "docs/quote.md" || report.Pinned[0].Line != 1 || report.Pinned[0].RemovedFrom != "docs/notes.md" {
		t.Fatalf("pinned = %+v, want docs/quote.md:1 from docs/notes.md", report.Pinned)
	}
}

// TestVerifyPinnedTextSkipsALineTheChangeAdded: a file the change rewrites to
// hold the sentence again, and a moved file, are not hits.
func TestVerifyPinnedTextSkipsALineTheChangeAdded(t *testing.T) {
	t.Parallel()
	r := pinnedRig(t)
	r.commit("a", "move the notes", map[string]string{
		"docs/notes.md": "# Notes\n",
		"docs/moved.md": "# Notes\n" + pinnedLong + "\n",
		"docs/quote.md": "gone\n",
	})
	out, code := r.run("verify")
	if code != 0 || strings.Contains(out, "pinned_text") {
		t.Fatalf("a moved sentence: exit %d, output:\n%s", code, out)
	}
}

// TestVerifyPinnedTextShowsTwentyAndCountsTheRest: more than 20 hits show the
// first 20 in path order and one line with the count of the rest.
func TestVerifyPinnedTextShowsTwentyAndCountsTheRest(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.setOracle("a", "echo ran-a")
	files := map[string]string{"docs/notes.md": pinnedLong + "\n"}
	for i := 0; i < 23; i++ {
		files[fmt.Sprintf("copies/f%02d.md", i)] = pinnedLong + "\n"
	}
	r.commit("", "setup", files)
	gitMust(t, r.fx.RepoDir, "update-ref", "refs/remotes/origin/main", "HEAD")
	r.commit("a", "remove", map[string]string{"docs/notes.md": "gone\n"})

	out, code := r.run("verify")
	if code != 0 {
		t.Fatalf("exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "copies/f00.md:1") || !strings.Contains(out, "copies/f19.md:1") || strings.Contains(out, "copies/f20.md") {
		t.Errorf("want the first 20 files in path order:\n%s", out)
	}
	if !strings.Contains(out, "not_shown: 3") {
		t.Errorf("want a count of 3 not shown:\n%s", out)
	}
}

// TestVerifyPinnedTextFailureToLookIsOneLine: with no base for the change the
// run reports its own outcome and one line saying why no rows were made.
func TestVerifyPinnedTextFailureToLookIsOneLine(t *testing.T) {
	t.Parallel()
	r := pinnedRig(t)
	r.removeFromNotes()
	gitMust(t, r.fx.RepoDir, "update-ref", "-d", "refs/remotes/origin/main")
	out, code := r.run("verify")
	if code != 0 || !strings.Contains(out, "state: pass") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if strings.Contains(out, "ERROR") {
		t.Errorf("a look that cannot be made is no error:\n%s", out)
	}
	// The reason a look was not made is one line of its own, and no table.
	blocks := pinnedBlocks(verify.Pinned{Note: "no base for the change is known"})
	if len(blocks) != 1 || !strings.Contains(blocks[0], "no base for the change is known") || strings.Contains(blocks[0], "file:line") {
		t.Errorf("blocks for a failed look = %q, want the one reason line", blocks)
	}
}

// lastJSONLine is the closing report of a --json run: the last line that
// opens a JSON object.
func lastJSONLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "{") {
			return lines[i]
		}
	}
	return ""
}
