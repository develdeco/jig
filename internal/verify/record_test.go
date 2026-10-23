package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
	"github.com/develdeco/jig/internal/outcome"
)

// TestRecorderKeepsEveryRunsRecordings: each oracle run has a directory
// of its own, so a later run at the same commit (another attempt, another
// slice that built nothing, a retry after a crash) never clears what an
// earlier run's journal line describes, and a red run removes only its own.
func TestRecorderKeepsEveryRunsRecordings(t *testing.T) {
	t.Parallel()
	green := &recordingOracle{files: map[string]string{"login.svg": loginSVG}}
	r := newRecordRun(t, green)

	r.atGreenAttempt(1)
	r.atGreenAttempt(2)
	r.atGreenAttempt(2) // the same attempt again: a run of its own, not the earlier one's directory

	rec := recordedLines(r.journaled())
	if len(rec) != 3 {
		t.Fatalf("recorded lines = %+v, want one per run", rec)
	}
	seen := map[string]bool{}
	for i, wantRun := range []string{"a-v1", "a-v2", "a-v2-2"} {
		if rec[i].RecordRun != wantRun || seen[rec[i].RecordRun] || rec[i].Commit != r.sha {
			t.Errorf("recorded line %d = %+v, want run %s at %s", i, rec[i], wantRun, r.sha)
		}
		seen[rec[i].RecordRun] = true
		got, err := os.ReadFile(filepath.Join(r.recordDir(rec[i].RecordRun), "login.svg"))
		if err != nil || string(got) != loginSVG {
			t.Errorf("run %s's recording: %q, %v; want it intact", rec[i].RecordRun, got, err)
		}
	}

	// A red run at the same commit removes its own directory and no other.
	r.oracle = &recordingOracle{files: map[string]string{"red.svg": loginSVG}, fail: true}
	r.atGreenAttempt(3)
	mustNotExist(t, r.recordDir("a-v3"))
	for run := range seen {
		mustExist(t, filepath.Join(r.recordDir(run), "login.svg"))
	}
	if got := recordedLines(r.journaled()); len(got) != 3 {
		t.Errorf("a red run journaled a recorded line: %+v", got)
	}
}

// TestRecorderDiscardsAFailedRunsRecordings: only a green scenario is
// evidence, so a red run journals no recorded line and leaves nothing.
func TestRecorderDiscardsAFailedRunsRecordings(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{files: map[string]string{"login.svg": loginSVG}, fail: true}
	r := newRecordRun(t, o)

	if res := r.atGreen(); res.Outcome == outcome.Green {
		t.Fatalf("a red oracle run came back green: %+v", res)
	}
	if runs := o.seen(); len(runs) != 1 || runs[0].recordDir != r.recordDir(firstRun) {
		t.Fatalf("oracle runs = %+v, want one that was given the record dir", runs)
	}
	lines := r.journaled()
	if got := recordedLines(lines); len(got) != 0 {
		t.Errorf("recorded lines after a red run: %+v", got)
	}
	if len(lines) != 0 {
		t.Errorf("journal = %+v, want nothing: a red run's recordings leave no line", lines)
	}
	mustNotExist(t, r.recordDir(firstRun))
}

// TestRecorderRefusesTheWholeCollection: a recording that fails the
// media rules (here an empty png) refuses everything the run wrote, journals
// why, removes the directory, and still leaves the slice green. A tag is
// refused in jig's words, not the decoder's, whatever the tag holds.
func TestRecorderRefusesTheWholeCollection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"an empty recording", map[string]string{"good.svg": loginSVG, "empty.png": ""}, `file "empty.png" is empty`},
		{"a tag that is not valid", map[string]string{"good.svg": loginSVG, "good.svg.json": `{"scenario":"x","colour":"red"}`}, `tag "good.svg.json" is not valid: it has a field other than`},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := &recordingOracle{files: tc.files}
			r := newRecordRun(t, o)

			if res := r.atGreen(); res.Outcome != outcome.Green {
				t.Fatalf("outcome = %q, want green: a refused collection never changes it", res.Outcome)
			}
			rec := recordedLines(r.journaled())
			if len(rec) != 1 {
				t.Fatalf("recorded lines = %+v, want exactly one", rec)
			}
			if !strings.HasPrefix(rec[0].Outcome, "refused: ") || !strings.Contains(rec[0].Outcome, tc.want) {
				t.Errorf("Outcome = %q, want a refusal naming %q", rec[0].Outcome, tc.want)
			}
			if rec[0].Commit != r.sha || rec[0].RecordRun != firstRun || len(rec[0].Recordings) != 0 {
				t.Errorf("recorded line = %+v, want the commit, the run and no recordings", rec[0])
			}
			mustNotExist(t, r.recordDir(firstRun))
		})
	}
}

func manyFiles(n int) map[string]string {
	files := map[string]string{}
	for i := 0; i < n; i++ {
		files[string(rune('a'+i/26))+string(rune('a'+i%26))+".svg"] = loginSVG
	}
	return files
}

// sortedNames returns the keys of files in name order.
func sortedNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestRecorderKeepsMoreRecordingsThanOneAttachTakes: jig's own end-to-end
// suite makes dozens of runs, so a run's collection is bounded by
// maxRecordings, not by the 50 files one `gh --attach` takes (media.MaxFiles),
// which binds publish's pick. Each recording is verified on its own and all are
// kept. Beyond maxRecordings the later ones by name are left out and counted,
// not a reason to refuse the rest, and a tag of one left out is not read.
func TestRecorderKeepsMoreRecordingsThanOneAttachTakes(t *testing.T) {
	t.Parallel()
	t.Run("more than the attach limit, within the bound", func(t *testing.T) {
		t.Parallel()
		files := manyFiles(media.MaxFiles + 10)
		r := newRecordRun(t, &recordingOracle{files: files})
		if res := r.atGreen(); res.Outcome != outcome.Green {
			t.Fatalf("outcome = %q, want green", res.Outcome)
		}
		rec := recordedLines(r.journaled())
		if len(rec) != 1 || rec[0].Outcome != "" || len(rec[0].Recordings) != len(files) {
			t.Fatalf("recorded lines = %+v, want one with all %d recordings and no outcome", rec, len(files))
		}
		for i, name := range sortedNames(files) {
			if got := rec[0].Recordings[i]; got.File != name || got.SHA256 != sum(loginSVG) {
				t.Errorf("recording %d = %+v, want %s intact", i, got, name)
			}
		}
	})
	t.Run("beyond the bound", func(t *testing.T) {
		t.Parallel()
		files := manyFiles(maxRecordings + 5)
		names := sortedNames(files)
		// The tag of a recording left out is never read, so it cannot refuse the rest.
		files[names[len(names)-1]+".json"] = "not json"
		r := newRecordRun(t, &recordingOracle{files: files})
		if res := r.atGreen(); res.Outcome != outcome.Green {
			t.Fatalf("outcome = %q, want green", res.Outcome)
		}
		rec := recordedLines(r.journaled())
		if len(rec) != 1 || len(rec[0].Recordings) != maxRecordings {
			t.Fatalf("recorded lines = %+v, want one keeping %d recordings", rec, maxRecordings)
		}
		if want := "5 recordings beyond the first 200 left out"; rec[0].Outcome != want {
			t.Errorf("Outcome = %q, want %q", rec[0].Outcome, want)
		}
		for i, name := range names[:maxRecordings] {
			if rec[0].Recordings[i].File != name {
				t.Errorf("recording %d = %s, want %s: the first by name are kept", i, rec[0].Recordings[i].File, name)
				break
			}
		}
		mustExist(t, filepath.Join(r.recordDir(firstRun), names[len(names)-1]))
	})
	t.Run("a refusal still refuses the whole collection", func(t *testing.T) {
		t.Parallel()
		files := manyFiles(media.MaxFiles + 10)
		files["aa.svg"] = ""
		r := newRecordRun(t, &recordingOracle{files: files})
		r.atGreen()
		rec := recordedLines(r.journaled())
		if len(rec) != 1 || !strings.HasPrefix(rec[0].Outcome, "refused: ") || !strings.Contains(rec[0].Outcome, `file "aa.svg" is empty`) || len(rec[0].Recordings) != 0 {
			t.Fatalf("recorded lines = %+v, want one refusal naming aa.svg", rec)
		}
		mustNotExist(t, r.recordDir(firstRun))
	})
}

// TestRecorderNeverCommitsAHostPathOrAnUnboundedReason: a tag may name
// anything as a field (the record directory itself, as a JSON string writes
// its backslashes), and its text may be huge. The refusal journaled for it
// says what is wrong in jig's words, holds no path of this machine, and stays
// short.
func TestRecorderNeverCommitsAHostPathOrAnUnboundedReason(t *testing.T) {
	t.Parallel()
	for name, tag := range map[string]func(dir string) string{
		"a field named as the directory": func(dir string) string { return `{"` + asJSON(dir) + `":1}` },
		"a huge field name":              func(string) string { return `{"` + strings.Repeat("k", maxTagBytes/2) + `":1}` },
		"a value of the wrong type":      func(dir string) string { return `{"step":"` + asJSON(dir) + `"}` },
		"a long scenario":                func(string) string { return `{"scenario":"` + strings.Repeat("s", maxScenarioBytes+1) + `"}` },
		"a long flow":                    func(string) string { return `{"flow":"` + strings.Repeat("f", maxFlowBytes+1) + `"}` },
		"a long caption":                 func(string) string { return `{"caption":"` + strings.Repeat("c", maxCaptionBytes+1) + `"}` },
	} {
		tag := tag
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := &recordingOracle{files: map[string]string{"a.svg": loginSVG}}
			r := newRecordRun(t, o)
			dir := r.recordDir(firstRun)
			o.files["a.svg.json"] = tag(dir)

			r.atGreen()

			rec := recordedLines(r.journaled())
			if len(rec) != 1 || !strings.HasPrefix(rec[0].Outcome, `refused: tag "a.svg.json" is not valid`) {
				t.Fatalf("recorded lines = %+v, want one refusing the tag", rec)
			}
			if len(rec[0].Outcome) > maxOutcomeBytes+len("...") {
				t.Errorf("the reason is %d bytes", len(rec[0].Outcome))
			}
			for _, p := range []string{dir, filepath.ToSlash(dir), asJSON(dir), r.home, filepath.ToSlash(r.home), asJSON(r.home)} {
				if strings.Contains(rec[0].Outcome, p) {
					t.Errorf("the reason holds %q: %s", p, rec[0].Outcome)
				}
			}
		})
	}
}

// TestLeaveOutHostPathsNamesTheDirectoriesJigChose covers every spelling an
// error text may carry a path in.
func TestLeaveOutHostPathsNamesTheDirectoriesJigChose(t *testing.T) {
	t.Parallel()
	jigHome := filepath.Join(t.TempDir(), "jig home")
	recordDir := filepath.Join(jigHome, "evidence", "id", "T-1", "recordings", "abc", "run")
	for name, spell := range map[string]func(string) string{
		"as they are":                func(p string) string { return p },
		"with forward slashes":       filepath.ToSlash,
		"as a JSON string writes it": asJSON,
	} {
		spell := spell
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := "open " + spell(filepath.Join(recordDir, "a.png")) + ": denied; " + spell(filepath.Join(jigHome, "other"))
			got := leaveOutHostPaths(in, recordDir, jigHome)
			for _, bad := range []string{jigHome, filepath.ToSlash(jigHome), asJSON(jigHome)} {
				if strings.Contains(got, bad) {
					t.Errorf("leaveOutHostPaths(%q) = %q, still holds %q", in, got, bad)
				}
			}
			if !strings.Contains(got, recordDirEnv) || !strings.Contains(got, "<jig home>") {
				t.Errorf("leaveOutHostPaths(%q) = %q, want the directories named by role", in, got)
			}
		})
	}
	if got := leaveOutHostPaths("nothing here", "", ""); got != "nothing here" {
		t.Errorf("leaveOutHostPaths with no directories changed the text: %q", got)
	}
}

// TestLeaveOutHostPathsInAWindowsSpelling runs on every OS with Windows-style
// paths: an error text that quotes one inside a JSON string has each
// separator doubled, and the reason must not keep it.
func TestLeaveOutHostPathsInAWindowsSpelling(t *testing.T) {
	t.Parallel()
	jigHome := `C:\Users\Someone\.config\jig`
	recordDir := jigHome + `\evidence\id\T-1\recordings\abc\run`
	in := `json: unknown field "` + asJSON(recordDir) + `"; open ` + recordDir + `\a.png: denied; ` + asJSON(jigHome)
	got := leaveOutHostPaths(in, recordDir, jigHome)
	for _, bad := range []string{jigHome, asJSON(jigHome), "Someone"} {
		if strings.Contains(got, bad) {
			t.Errorf("leaveOutHostPaths(%q) = %q, still holds %q", in, got, bad)
		}
	}
	if want := `json: unknown field "JIG_RECORD_DIR"; open JIG_RECORD_DIR\a.png: denied; <jig home>`; got != want {
		t.Errorf("leaveOutHostPaths = %q, want %q", got, want)
	}
}

// TestCapText keeps a reason short and whole.
func TestCapText(t *testing.T) {
	t.Parallel()
	if got := capText("short", 10); got != "short" {
		t.Errorf("capText = %q", got)
	}
	if got := capText("abcdefghij", 4); got != "abcd..." {
		t.Errorf("capText = %q", got)
	}
	// A cut inside a multi-byte rune ends before the rune.
	if got := capText("abécd", 3); got != "ab..." {
		t.Errorf("capText = %q, want the cut before the rune it would split", got)
	}
}

// TestRecorderNamesWhatItDropped: a file that is not a recording, a
// subdirectory, and a tag whose recording is missing are left where they are
// and named in the line's outcome beside the recordings that were collected.
func TestRecorderNamesWhatItDropped(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{
		files: map[string]string{
			"login.svg":     loginSVG,
			"notes.txt":     "not a recording",
			"gone.png.json": `{"scenario":"x"}`, // the tag of a recording that is not there
		},
		dirs: []string{"sub"},
	}
	r := newRecordRun(t, o)

	if res := r.atGreen(); res.Outcome != outcome.Green {
		t.Fatalf("outcome = %q, want green", res.Outcome)
	}
	rec := recordedLines(r.journaled())
	if len(rec) != 1 {
		t.Fatalf("recorded lines = %+v, want one", rec)
	}
	if want := "dropped: gone.png.json, notes.txt, sub"; rec[0].Outcome != want {
		t.Errorf("Outcome = %q, want %q", rec[0].Outcome, want)
	}
	if len(rec[0].Recordings) != 1 || rec[0].Recordings[0].File != "login.svg" {
		t.Errorf("Recordings = %+v, want only login.svg", rec[0].Recordings)
	}
	for _, name := range []string{"login.svg", "notes.txt", "gone.png.json", "sub"} {
		mustExist(t, filepath.Join(r.recordDir(firstRun), name))
	}
}

// TestRecorderDropsALinkAndDoesNotFollowIt: a link with a recording's
// name is not a regular file, so it is dropped and named; what it points at
// is never read or hashed.
func TestRecorderDropsALinkAndDoesNotFollowIt(t *testing.T) {
	t.Parallel()
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "secret.png"), []byte("not for the journal"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &recordingOracle{files: map[string]string{"ok.svg": loginSVG}}
	o.act = func(rd string) error {
		return os.Symlink(filepath.Join(elsewhere, "secret.png"), filepath.Join(rd, "link.png"))
	}
	r := newRecordRun(t, o)
	if err := os.Symlink(filepath.Join(elsewhere, "secret.png"), filepath.Join(t.TempDir(), "probe.png")); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}

	if res := r.atGreen(); res.Outcome != outcome.Green {
		t.Fatalf("outcome = %q, want green", res.Outcome)
	}
	rec := recordedLines(r.journaled())
	if len(rec) != 1 || rec[0].Outcome != "dropped: link.png" || len(rec[0].Recordings) != 1 || rec[0].Recordings[0].File != "ok.svg" {
		t.Fatalf("recorded lines = %+v, want ok.svg recorded and link.png dropped", rec)
	}
	mustExist(t, filepath.Join(elsewhere, "secret.png"))
}

// TestRecorderAcceptsADirectoryTheHarnessRecreated: a harness may remove
// and recreate its output directory (Playwright clears its own), which gives
// it a new identity. What is required is a plain directory under plain
// parents at collection, not the very directory jig made, so the recordings
// are collected. A directory removed and not recreated held nothing.
func TestRecorderAcceptsADirectoryTheHarnessRecreated(t *testing.T) {
	t.Parallel()
	t.Run("recreated", func(t *testing.T) {
		t.Parallel()
		o := &recordingOracle{files: map[string]string{"login.svg": loginSVG}}
		o.act = func(rd string) error {
			if err := os.RemoveAll(rd); err != nil {
				return err
			}
			return os.Mkdir(rd, 0o755)
		}
		r := newRecordRun(t, o)
		if res := r.atGreen(); res.Outcome != outcome.Green {
			t.Fatalf("outcome = %q, want green", res.Outcome)
		}
		rec := recordedLines(r.journaled())
		if len(rec) != 1 || rec[0].Outcome != "" || len(rec[0].Recordings) != 1 || rec[0].Recordings[0].SHA256 != sum(loginSVG) {
			t.Fatalf("recorded lines = %+v, want login.svg recorded", rec)
		}
		mustExist(t, filepath.Join(r.recordDir(firstRun), "login.svg"))
	})
	t.Run("removed", func(t *testing.T) {
		t.Parallel()
		o := &recordingOracle{}
		o.act = func(rd string) error { return os.RemoveAll(rd) }
		r := newRecordRun(t, o)
		if res := r.atGreen(); res.Outcome != outcome.Green {
			t.Fatalf("outcome = %q, want green", res.Outcome)
		}
		if got := recordedLines(r.journaled()); len(got) != 0 {
			t.Errorf("recorded lines = %+v, want none for a directory that is gone", got)
		}
	})
}

// TestRecorderLeavesWhatASwappedDirectoryLeadsTo: a link in place of the
// record directory, or of a directory above it, is never read or removed
// through: a link's target is never touched, whether the run passed or failed.
// A passing run journals the refusal. A failing run journals nothing, as for
// any failing run, and leaves the directory as it is.
func TestRecorderLeavesWhatASwappedDirectoryLeadsTo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		fail bool
		swap func(t *testing.T, r *recordRun, rd, elsewhere string) error
		want string // the refusal a recorded line says; "" for no recorded line
	}{
		{"the record directory", false, func(t *testing.T, r *recordRun, rd, elsewhere string) error {
			if err := os.RemoveAll(rd); err != nil {
				return err
			}
			return os.Symlink(elsewhere, rd)
		}, "is not a plain directory"},
		{"a directory above it, on a passing run", false, func(t *testing.T, r *recordRun, rd, elsewhere string) error {
			parent := filepath.Dir(filepath.Dir(rd)) // .../<ticket>/recordings
			if err := os.Rename(parent, parent+".moved"); err != nil {
				return err
			}
			return os.Symlink(elsewhere, parent)
		}, "is not a plain directory"},
		{"the record directory, on a failing run", true, func(t *testing.T, r *recordRun, rd, elsewhere string) error {
			if err := os.RemoveAll(rd); err != nil {
				return err
			}
			return os.Symlink(elsewhere, rd)
		}, ""},
		{"a directory above it, on a failing run", true, func(t *testing.T, r *recordRun, rd, elsewhere string) error {
			parent := filepath.Dir(filepath.Dir(rd))
			if err := os.Rename(parent, parent+".moved"); err != nil {
				return err
			}
			return os.Symlink(elsewhere, parent)
		}, ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// elsewhere holds a directory shaped like the run's own, with a file in it.
			elsewhere := t.TempDir()
			o := &recordingOracle{fail: tc.fail}
			r := newRecordRun(t, o)
			precious := []string{
				filepath.Join(elsewhere, "x.png"),
				filepath.Join(elsewhere, r.sha, firstRun, "x.png"),
				filepath.Join(elsewhere, "x.png.json"),
			}
			if err := os.MkdirAll(filepath.Join(elsewhere, r.sha, firstRun), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, p := range precious {
				if err := os.WriteFile(p, []byte("not mine"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			o.act = func(rd string) error { return tc.swap(t, r, rd, elsewhere) }
			if err := os.Symlink(elsewhere, filepath.Join(t.TempDir(), "probe")); err != nil {
				t.Skipf("cannot make a symbolic link here: %v", err)
			}

			r.atGreen()

			rec := recordedLines(r.journaled())
			if tc.want == "" {
				if len(rec) != 0 {
					t.Errorf("a failing run journaled a recorded line: %+v", rec)
				}
			} else {
				if len(rec) != 1 || !strings.HasPrefix(rec[0].Outcome, "refused: ") || !strings.Contains(rec[0].Outcome, tc.want) {
					t.Fatalf("recorded lines = %+v, want one refusal saying %q", rec, tc.want)
				}
				if len(rec[0].Recordings) != 0 {
					t.Errorf("recordings were collected through a link: %+v", rec[0].Recordings)
				}
				if strings.Contains(rec[0].Outcome, "x.png") {
					t.Errorf("the refusal quotes what the link leads to: %s", rec[0].Outcome)
				}
			}
			for _, p := range precious {
				mustExist(t, p)
			}
		})
	}
}

// TestRecorderWritingNothingJournalsNothing: a run that wrote no
// recording leaves no line and no directory behind.
func TestRecorderWritingNothingJournalsNothing(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{}
	r := newRecordRun(t, o)
	if res := r.atGreen(); res.Outcome != outcome.Green {
		t.Fatalf("outcome = %q, want green", res.Outcome)
	}
	if runs := o.seen(); len(runs) != 1 || runs[0].recordDir != r.recordDir(firstRun) {
		t.Errorf("oracle runs = %+v, want one given the record dir", runs)
	}
	if lines := r.journaled(); len(lines) != 0 {
		t.Errorf("journal = %+v, want nothing", lines)
	}
	mustNotExist(t, r.recordDir(firstRun))
}

// TestPickRecordings chooses the recordings of a directory's entries and
// drops, without refusing, everything else: a name jig cannot accept, a
// case-insensitive duplicate (the later one), a link or directory with a
// recording's name, an unaccepted type, and a tag with no recording.
func TestPickRecordings(t *testing.T) {
	t.Parallel()
	entries := []dirEntry{ // in name order, as os.ReadDir gives them
		{"Login.png", true},
		{"Login.png.json", true},
		{`a:b.png`, true},
		{`c\d.png`, true},
		{"dir.png", false},
		{"link.png", false},
		{"login.png", true}, // the same name as Login.png where the file system folds case
		{"login.png.json", true},
		{"ok.mp4", true},
		{"ok.mp4.json", true},
		{"orphan.png.json", true},
		{"readme", true},
		{"trace.zip", true},
	}
	picks, dropped := pickRecordings(entries)
	wantPicks := []pick{{"Login.png", "Login.png.json"}, {"ok.mp4", "ok.mp4.json"}}
	wantDropped := []string{`a:b.png`, `c\d.png`, "dir.png", "link.png", "login.png", "login.png.json", "orphan.png.json", "readme", "trace.zip"}
	if !reflect.DeepEqual(picks, wantPicks) {
		t.Errorf("picks = %+v, want %+v", picks, wantPicks)
	}
	if !reflect.DeepEqual(dropped, wantDropped) {
		t.Errorf("dropped = %v, want %v", dropped, wantDropped)
	}
}

// TestCollectRecordings covers the collection itself in a directory.
func TestCollectRecordings(t *testing.T) {
	t.Parallel()
	collect := func(t *testing.T, files map[string]string) ([]journal.Recording, []string, error) {
		t.Helper()
		top := filepath.Join(t.TempDir(), "evidence")
		dir := filepath.Join(top, "id", "JIG_1", "recordings", "sha", "run")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := collectRecordings(top, dir)
		return got.recs, got.dropped, err
	}

	t.Run("recordings come back in name order, scenario from the name", func(t *testing.T) {
		t.Parallel()
		recs, dropped, err := collect(t, map[string]string{"b.v2.WEBP": "bb", "a.mp4": "aaa", "c.png": "c"})
		if err != nil || len(dropped) != 0 {
			t.Fatalf("recs=%+v dropped=%v err=%v", recs, dropped, err)
		}
		var got []string
		for _, r := range recs {
			got = append(got, r.File+"="+r.Scenario)
		}
		if want := "a.mp4=a b.v2.WEBP=b.v2 c.png=c"; strings.Join(got, " ") != want {
			t.Errorf("recordings = %v, want %s", got, want)
		}
	})
	t.Run("a tag may set any of its fields", func(t *testing.T) {
		t.Parallel()
		for body, want := range map[string]journal.Recording{
			`{}`:                             {Scenario: "a"},
			`{"step":3}`:                     {Scenario: "a", Step: 3},
			`{"flow":"f"}`:                   {Scenario: "a", Flow: "f"},
			`{"caption":"c","scenario":"s"}`: {Scenario: "s", Caption: "c"},
			"  {\"flow\": \"f\"}\n\n":        {Scenario: "a", Flow: "f"},
		} {
			recs, _, err := collect(t, map[string]string{"a.png": "x", "a.png.json": body})
			if err != nil || len(recs) != 1 {
				t.Fatalf("tag %q: recs=%+v err=%v", body, recs, err)
			}
			want.File, want.SHA256, want.Size = "a.png", sum("x"), 1
			if recs[0] != want {
				t.Errorf("tag %q: %+v, want %+v", body, recs[0], want)
			}
		}
	})
	t.Run("an invalid tag refuses the collection and names its file", func(t *testing.T) {
		t.Parallel()
		for body, why := range map[string]string{
			``:                                  "it is empty or ends early",
			`not json`:                          "it is not JSON",
			`[]`:                                "it is not a JSON object",
			`{"scenario":5}`:                    `field "scenario" has the wrong type`,
			`{"step":"1"}`:                      `field "step" has the wrong type`,
			`{"step":1.5}`:                      `field "step" has the wrong type`,
			`{"step":-1}`:                       "step is negative",
			`{"unknown":1}`:                     "it has a field other than scenario, flow, step and caption",
			`{"scenario":"a"} {"scenario":"b"}`: "more than one JSON value",
			`{"scenario":"a"}}`:                 "more than one JSON value",
			`null x`:                            "more than one JSON value",
			`{"caption":"` + strings.Repeat("x", maxTagBytes) + `"}`: "bytes",
		} {
			recs, _, err := collect(t, map[string]string{"a.png": "x", "a.png.json": body, "ok.svg": "y"})
			if err == nil || !strings.Contains(err.Error(), `tag "a.png.json" is`) {
				t.Errorf("tag %.40q: recs=%+v err=%v, want a refusal naming a.png.json", body, recs, err)
				continue
			}
			if !strings.Contains(err.Error(), why) {
				t.Errorf("tag %.40q: err=%v, want it to say %q", body, err, why)
			}
			if strings.Contains(err.Error(), "json:") || strings.Contains(err.Error(), "unknown field") {
				t.Errorf("tag %.40q: the refusal quotes the decoder: %v", body, err)
			}
			if recs != nil {
				t.Errorf("tag %.40q: a refused collection returned %+v", body, recs)
			}
		}
	})
	t.Run("what is not a recording is dropped and named", func(t *testing.T) {
		t.Parallel()
		_, dropped, err := collect(t, map[string]string{
			"a.png": "x", "a.png.json": `{}`, "b.json": `{}`, "c.txt": "t", "d.png.json": `{}`, "noext": "n",
		})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"b.json", "c.txt", "d.png.json", "noext"}; !reflect.DeepEqual(dropped, want) {
			t.Errorf("dropped = %v, want %v", dropped, want)
		}
	})
	t.Run("an empty directory holds nothing", func(t *testing.T) {
		t.Parallel()
		recs, dropped, err := collect(t, nil)
		if err != nil || len(recs) != 0 || len(dropped) != 0 {
			t.Errorf("recs=%+v dropped=%v err=%v", recs, dropped, err)
		}
	})
	t.Run("a directory that is gone holds nothing", func(t *testing.T) {
		t.Parallel()
		top := filepath.Join(t.TempDir(), "evidence")
		got, err := collectRecordings(top, filepath.Join(top, "id", "T", "recordings", "sha", "run"))
		recs, dropped := got.recs, got.dropped
		if err != nil || len(recs) != 0 || len(dropped) != 0 {
			t.Errorf("recs=%+v dropped=%v err=%v", recs, dropped, err)
		}
	})
	t.Run("a regular file where the directory belongs is refused", func(t *testing.T) {
		t.Parallel()
		top := filepath.Join(t.TempDir(), "evidence")
		dir := filepath.Join(top, "id", "T", "recordings", "sha", "run")
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := collectRecordings(top, dir); err == nil || !strings.Contains(err.Error(), "is not a plain directory") {
			t.Errorf("err = %v, want the refusal", err)
		}
	})
	t.Run("a directory outside the evidence directory is refused", func(t *testing.T) {
		t.Parallel()
		top := filepath.Join(t.TempDir(), "evidence")
		if _, err := collectRecordings(top, t.TempDir()); err == nil {
			t.Error("a directory that is not below the evidence directory was read")
		}
	})
}

// TestNameDroppedCapsTheNamesItLists keeps a journal line short when a
// scenario leaves many stray files.
func TestNameDroppedCapsTheNamesItLists(t *testing.T) {
	t.Parallel()
	if got := nameDropped([]string{"a", "b"}); got != "a, b" {
		t.Errorf("nameDropped = %q", got)
	}
	if got := nameDropped([]string{"a", "b", "c", "d", "e", "f", "g"}); got != "a, b, c, d, e (and 2 more)" {
		t.Errorf("nameDropped = %q", got)
	}
}

// TestRecorderWithoutAJigHomeOrACommitDoesNotRecord: no home means nowhere to
// put recordings, and no commit nothing to key them by. The oracle is given no
// JIG_RECORD_DIR (a scenario that sees it unset does not record) and nothing
// is journaled.
func TestRecorderWithoutAJigHomeOrACommitDoesNotRecord(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(r *recordRun){
		"no home":      func(r *recordRun) { r.rec.home = "" },
		"no commit":    func(r *recordRun) { r.sha = "" },
		"no store id":  func(r *recordRun) { r.rec.storeID = "" },
		"a bad ticket": func(r *recordRun) { r.rec.ticket = "../escape" },
	} {
		change := change
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := &recordingOracle{files: map[string]string{"login.svg": loginSVG}}
			r := newRecordRun(t, o)
			change(r)
			if res := r.atGreen(); res.Outcome != outcome.Green {
				t.Fatalf("outcome = %q, want green", res.Outcome)
			}
			runs := o.seen()
			if len(runs) != 1 || runs[0].recordDir != "" || len(runs[0].env) != 0 {
				t.Errorf("oracle runs = %+v, want one with no JIG_RECORD_DIR and no env", runs)
			}
			if lines := r.journaled(); len(lines) != 0 {
				t.Errorf("journal = %+v, want nothing", lines)
			}
		})
	}
}

// TestRecorderDoesNotRecordWhenItCannotMakeADirectory: recording is best
// effort. A directory that cannot be made plain goes ahead without one.
func TestRecorderDoesNotRecordWhenItCannotMakeADirectory(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{files: map[string]string{"login.svg": loginSVG}}
	r := newRecordRun(t, o)
	parent := filepath.Dir(filepath.Dir(r.recordDir(firstRun))) // .../<ticket>/recordings
	if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := r.atGreen(); res.Outcome != outcome.Green {
		t.Fatalf("outcome = %q, want green", res.Outcome)
	}
	if runs := o.seen(); len(runs) != 1 || runs[0].recordDir != "" {
		t.Errorf("oracle runs = %+v, want one with no JIG_RECORD_DIR", runs)
	}
	if got := recordedLines(r.journaled()); len(got) != 0 {
		t.Errorf("recorded lines: %+v", got)
	}
}

// TestRecorderJournalsWhatTheScenariosRecorded: a passing run's recordings
// become one recorded line keyed by the commit and the run, tags give a
// recording its scenario, flow, step and caption, a recording without a tag
// takes its scenario from its name, and the files stay on disk under the jig
// home. The oracle is handed only JIG_RECORD_DIR.
func TestRecorderJournalsWhatTheScenariosRecorded(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{files: map[string]string{
		"login.svg":    loginSVG,
		"pay.png":      payPNG,
		"pay.png.json": payTagJSON,
	}}
	r := newRecordRun(t, o)
	r.atGreen()

	dir := r.recordDir(firstRun)
	runs := o.seen()
	if len(runs) != 1 || runs[0].recordDir != dir || len(runs[0].env) != 1 {
		t.Fatalf("oracle runs = %+v, want one with only JIG_RECORD_DIR=%s", runs, dir)
	}
	rec := recordedLines(r.journaled())
	if len(rec) != 1 {
		t.Fatalf("recorded lines = %+v, want one", rec)
	}
	if rec[0].Slice != "a" || rec[0].Commit != r.sha || rec[0].Attempt != 1 || rec[0].RecordRun != firstRun || rec[0].Outcome != "" {
		t.Errorf("recorded line = %+v, want slice a, attempt 1, commit %s, run %s and no outcome", rec[0], r.sha, firstRun)
	}
	want := []journal.Recording{
		{File: "login.svg", SHA256: sum(loginSVG), Size: int64(len(loginSVG)), Scenario: "login"},
		{File: "pay.png", SHA256: sum(payPNG), Size: int64(len(payPNG)), Scenario: "checkout", Flow: "purchase", Step: 2, Caption: "pays for the cart"},
	}
	if !reflect.DeepEqual(rec[0].Recordings, want) {
		t.Errorf("Recordings = %+v\nwant         %+v", rec[0].Recordings, want)
	}
	for name, content := range o.files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != content {
			t.Errorf("%s under the record dir: %q, %v; want it kept as written", name, got, err)
		}
	}
	if rel, err := filepath.Rel(r.home, dir); err != nil || !filepath.IsLocal(rel) {
		t.Errorf("record dir %s is not under the jig home %s", dir, r.home)
	}
}
