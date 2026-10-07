package frontier

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// The recording contract (ADR 0029, step 3): jig's oracle run at a builder's
// green gives the scenarios a directory in JIG_RECORD_DIR, verifies what they
// wrote when the run passes, and journals it with the commit it ran at and the
// run that wrote it.

const recordTestOracle = "run the scenarios"

// oracleRun is one call the oracle stub received.
type oracleRun struct {
	cmd, dir  string
	env       []string
	recordDir string // JIG_RECORD_DIR in env, "" when it is not set
}

// recordingOracle is an oracle stub that writes files into the directory its
// env argument names, like a scenario that records, and then passes or fails.
type recordingOracle struct {
	files map[string]string // name -> content, written when a directory is given
	dirs  []string          // subdirectories made beside them
	fail  bool
	// act, when set, runs first with the record directory, as a harness that
	// replaces the directory or its parents would.
	act func(recordDir string) error

	mu   sync.Mutex
	runs []oracleRun
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

func (o *recordingOracle) run(cmd, dir string, env []string) (string, error) {
	rd := envValue(env, recordDirEnv)
	o.mu.Lock()
	o.runs = append(o.runs, oracleRun{cmd: cmd, dir: dir, env: env, recordDir: rd})
	o.mu.Unlock()
	if rd != "" {
		if o.act != nil {
			if err := o.act(rd); err != nil {
				return "", err
			}
		}
		for name, content := range o.files {
			if err := os.WriteFile(filepath.Join(rd, name), []byte(content), 0o644); err != nil {
				return "", err
			}
		}
		for _, name := range o.dirs {
			if err := os.Mkdir(filepath.Join(rd, name), 0o755); err != nil {
				return "", err
			}
		}
	}
	if o.fail {
		return "red on purpose", errors.New("exit status 1")
	}
	return "ok", nil
}

func (o *recordingOracle) seen() []oracleRun {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]oracleRun(nil), o.runs...)
}

// recordRun is a builder's green on a lease with a clean tree: a repo with a
// start commit and the commit the builder reports, and a runCtx whose journal
// is captured.
type recordRun struct {
	t       *testing.T
	rc      *runCtx
	oracle  *recordingOracle
	lease   pool.Lease
	start   string
	head    string
	home    string
	storeID string

	mu    sync.Mutex
	lines []journal.Line
}

func newRecordRun(t *testing.T, o *recordingOracle) *recordRun {
	t.Helper()
	leaseDir := t.TempDir()
	start := initTestGitRepo(t, leaseDir)
	if err := os.WriteFile(filepath.Join(leaseDir, "g.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, leaseDir, "add", "-A")
	runGitT(t, leaseDir, "commit", "-m", "work")
	head := runGitT(t, leaseDir, "rev-parse", "HEAD")

	st := newTestStore(t)
	id, err := st.ID()
	if err != nil {
		t.Fatal(err)
	}
	r := &recordRun{t: t, oracle: o, lease: pool.Lease{Dir: leaseDir}, start: start, head: head, home: t.TempDir(), storeID: id}
	r.rc = &runCtx{
		d: Deps{
			Store: st,
			Home:  r.home,
			Journal: func(l journal.Line) error {
				r.mu.Lock()
				defer r.mu.Unlock()
				r.lines = append(r.lines, l)
				return nil
			},
			Oracle: o.run,
		},
		ticket: "JIG_1",
	}
	return r
}

// atGreen runs the oracle at the builder's reported green, on attempt 1.
func (r *recordRun) atGreen() outcome.Result { return r.atGreenAttempt(1) }

// atGreenAttempt runs the oracle at the builder's reported green on attempt n.
func (r *recordRun) atGreenAttempt(n int) outcome.Result {
	r.t.Helper()
	res := outcome.Result{Outcome: outcome.Green, Commit: r.head}
	return r.rc.oracleAtGreen(store.Slice{ID: "a"}, r.lease, n, session.Dispatch{}, "", recordTestOracle, r.start, res)
}

// top is the evidence directory of the jig home.
func (r *recordRun) top() string { return filepath.Join(r.home, "evidence") }

// recordDir is where the named run's recordings belong.
func (r *recordRun) recordDir(run string) string {
	r.t.Helper()
	dir, err := home.RecordDir(r.home, r.storeID, "JIG_1", r.head, run)
	if err != nil {
		r.t.Fatal(err)
	}
	return dir
}

// firstRun is the name of attempt 1's first oracle run of slice a.
const firstRun = "a-a1-f0"

func (r *recordRun) journaled() []journal.Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]journal.Line(nil), r.lines...)
}

// recordedLines returns the journal's "recorded" lines.
func recordedLines(lines []journal.Line) []journal.Line {
	var out []journal.Line
	for _, l := range lines {
		if l.Event == "recorded" {
			out = append(out, l)
		}
	}
	return out
}

func sum(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// asJSON is s as the inside of a JSON string: each backslash written as two,
// the way a Windows path appears in a JSON key or in a decoder's message.
func asJSON(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s: Lstat err = %v, want it gone", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s: %v, want it left in place", path, err)
	}
}

// symlinkOrSkip makes a directory symbolic link, or skips the test where the
// machine does not allow one.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
}

const (
	loginSVG   = "<svg xmlns='http://www.w3.org/2000/svg'><title>login</title></svg>"
	payPNG     = "\x89PNG pretend bytes of a screenshot"
	payTagJSON = `{"scenario":"checkout","flow":"purchase","step":2,"caption":"pays for the cart"}`
)

// TestOracleAtGreenJournalsWhatTheScenariosRecorded: the run is handed a
// directory under the jig home in JIG_RECORD_DIR (and only that variable
// changes), the recordings it leaves there are verified and journaled as one
// "recorded" line keyed by the commit the oracle ran at and the run that
// wrote them, tags give a recording its scenario, flow, step and caption, a
// recording without a tag takes its scenario from its name, and the files stay
// on disk. The oracle line is exactly what it was without recording, so the
// gate can still reuse it.
func TestOracleAtGreenJournalsWhatTheScenariosRecorded(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{files: map[string]string{
		"login.svg":    loginSVG,
		"pay.png":      payPNG,
		"pay.png.json": payTagJSON,
	}}
	r := newRecordRun(t, o)

	res := r.atGreen()
	if res.Outcome != outcome.Green {
		t.Fatalf("outcome = %q, want green: recording never changes the outcome", res.Outcome)
	}

	dir := r.recordDir(firstRun)
	runs := o.seen()
	if len(runs) != 1 || runs[0].recordDir != dir {
		t.Fatalf("oracle runs = %+v, want one with JIG_RECORD_DIR=%s", runs, dir)
	}
	if runs[0].cmd != recordTestOracle || runs[0].dir != r.lease.Dir {
		t.Errorf("the oracle ran %q in %q, want %q in the lease %q", runs[0].cmd, runs[0].dir, recordTestOracle, r.lease.Dir)
	}
	if len(runs[0].env) != 1 {
		t.Errorf("the oracle was handed env %v, want only JIG_RECORD_DIR", runs[0].env)
	}

	lines := r.journaled()
	if len(lines) != 2 || lines[0].Event != "oracle" || lines[1].Event != "recorded" {
		t.Fatalf("journal = %+v, want the oracle line and then one recorded line", lines)
	}
	oracleLine, rec := lines[0], lines[1]
	if oracleLine.Command != recordTestOracle || oracleLine.Outcome != "pass" || oracleLine.Commit != r.head {
		t.Errorf("oracle line = %+v, want the unmodified command, a pass, at %s", oracleLine, r.head)
	}
	if rec.Slice != "a" || rec.Commit != r.head || rec.Attempt != 1 || rec.RecordRun != firstRun || rec.Outcome != "" {
		t.Errorf("recorded line = %+v, want slice a, attempt 1, commit %s, run %s and no outcome", rec, r.head, firstRun)
	}
	want := []journal.Recording{
		{File: "login.svg", SHA256: sum(loginSVG), Size: int64(len(loginSVG)), Scenario: "login"},
		{File: "pay.png", SHA256: sum(payPNG), Size: int64(len(payPNG)), Scenario: "checkout", Flow: "purchase", Step: 2, Caption: "pays for the cart"},
	}
	if !reflect.DeepEqual(rec.Recordings, want) {
		t.Errorf("Recordings = %+v\nwant         %+v", rec.Recordings, want)
	}

	for name, content := range o.files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != content {
			t.Errorf("%s under the record dir: %q, %v; want it kept as written", name, got, err)
		}
	}
	// The recordings are under the jig home, never in the lease.
	if rel, err := filepath.Rel(r.home, dir); err != nil || !filepath.IsLocal(rel) {
		t.Errorf("record dir %s is not under the jig home %s", dir, r.home)
	}
}

// TestOracleAtGreenKeepsEveryRunsRecordings: each oracle run has a directory
// of its own, so a later run at the same commit (another attempt, another
// slice that built nothing, a retry after a crash) never clears what an
// earlier run's journal line describes, and a red run removes only its own.
func TestOracleAtGreenKeepsEveryRunsRecordings(t *testing.T) {
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
	for i, wantRun := range []string{"a-a1-f0", "a-a2-f0", "a-a2-f0-2"} {
		if rec[i].RecordRun != wantRun || seen[rec[i].RecordRun] || rec[i].Commit != r.head {
			t.Errorf("recorded line %d = %+v, want run %s at %s", i, rec[i], wantRun, r.head)
		}
		seen[rec[i].RecordRun] = true
		got, err := os.ReadFile(filepath.Join(r.recordDir(rec[i].RecordRun), "login.svg"))
		if err != nil || string(got) != loginSVG {
			t.Errorf("run %s's recording: %q, %v; want it intact", rec[i].RecordRun, got, err)
		}
	}

	// A red run at the same commit removes its own directory and no other.
	r.rc.d.Oracle = (&recordingOracle{files: map[string]string{"red.svg": loginSVG}, fail: true}).run
	r.atGreenAttempt(3)
	mustNotExist(t, r.recordDir("a-a3-f0"))
	for run := range seen {
		mustExist(t, filepath.Join(r.recordDir(run), "login.svg"))
	}
	if got := recordedLines(r.journaled()); len(got) != 3 {
		t.Errorf("a red run journaled a recorded line: %+v", got)
	}
}

// TestOracleAtGreenDiscardsAFailedRunsRecordings: only a green scenario is
// evidence, so a red run journals no recorded line and leaves nothing.
func TestOracleAtGreenDiscardsAFailedRunsRecordings(t *testing.T) {
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
	if len(lines) != 1 || lines[0].Event != "oracle" || lines[0].Outcome != "fail" {
		t.Errorf("journal = %+v, want only the failed oracle line", lines)
	}
	mustNotExist(t, r.recordDir(firstRun))
}

// TestOracleAtGreenRefusesTheWholeCollection: a recording that fails the
// media rules (here an empty png) refuses everything the run wrote, journals
// why, removes the directory, and still leaves the slice green. A tag is
// refused in jig's words, not the decoder's, whatever the tag holds.
func TestOracleAtGreenRefusesTheWholeCollection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"an empty recording", map[string]string{"good.svg": loginSVG, "empty.png": ""}, `file "empty.png" is empty`},
		{"a tag that is not valid", map[string]string{"good.svg": loginSVG, "good.svg.json": `{"scenario":"x","colour":"red"}`}, `tag "good.svg.json" is not valid: it has a field other than`},
		{"too many recordings", manyFiles(media.MaxFiles + 1), "at most 50 are accepted"},
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
			if rec[0].Commit != r.head || rec[0].RecordRun != firstRun || len(rec[0].Recordings) != 0 {
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

// TestOracleAtGreenNeverCommitsAHostPathOrAnUnboundedReason: a tag may name
// anything as a field (the record directory itself, as a JSON string writes
// its backslashes), and its text may be huge. The refusal journaled for it
// says what is wrong in jig's words, holds no path of this machine, and stays
// short.
func TestOracleAtGreenNeverCommitsAHostPathOrAnUnboundedReason(t *testing.T) {
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

// TestOracleAtGreenNamesWhatItDropped: a file that is not a recording, a
// subdirectory, and a tag whose recording is missing are left where they are
// and named in the line's outcome beside the recordings that were collected.
func TestOracleAtGreenNamesWhatItDropped(t *testing.T) {
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

// TestOracleAtGreenDropsALinkAndDoesNotFollowIt: a link with a recording's
// name is not a regular file, so it is dropped and named; what it points at
// is never read or hashed.
func TestOracleAtGreenDropsALinkAndDoesNotFollowIt(t *testing.T) {
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

// TestOracleAtGreenAcceptsADirectoryTheHarnessRecreated: a harness may remove
// and recreate its output directory (Playwright clears its own), which gives
// it a new identity. What is required is a plain directory under plain
// parents at collection, not the very directory jig made, so the recordings
// are collected. A directory removed and not recreated held nothing.
func TestOracleAtGreenAcceptsADirectoryTheHarnessRecreated(t *testing.T) {
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

// TestOracleAtGreenLeavesWhatASwappedDirectoryLeadsTo: a link in place of the
// record directory, or of a directory above it, is never read or removed
// through: a link's target is never touched, whether the run passed or failed.
// A passing run journals the refusal. A failing run journals nothing, as for
// any failing run, and leaves the directory as it is.
func TestOracleAtGreenLeavesWhatASwappedDirectoryLeadsTo(t *testing.T) {
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
				filepath.Join(elsewhere, r.head, firstRun, "x.png"),
				filepath.Join(elsewhere, "x.png.json"),
			}
			if err := os.MkdirAll(filepath.Join(elsewhere, r.head, firstRun), 0o755); err != nil {
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

// TestOracleAtGreenWithoutAJigHomeDoesNotRecord: no home means nowhere to put
// recordings. The oracle is given no JIG_RECORD_DIR (a scenario that sees it
// unset does not record), nothing is journaled but the oracle line, and the
// slice is green.
func TestOracleAtGreenWithoutAJigHomeDoesNotRecord(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{files: map[string]string{"login.svg": loginSVG}}
	r := newRecordRun(t, o)
	r.rc.d.Home = ""

	if res := r.atGreen(); res.Outcome != outcome.Green {
		t.Fatalf("outcome = %q, want green", res.Outcome)
	}
	runs := o.seen()
	if len(runs) != 1 || runs[0].recordDir != "" || len(runs[0].env) != 0 {
		t.Errorf("oracle runs = %+v, want one with no JIG_RECORD_DIR and no env", runs)
	}
	lines := r.journaled()
	if len(lines) != 1 || lines[0].Event != "oracle" || lines[0].Command != recordTestOracle || lines[0].Outcome != "pass" {
		t.Errorf("journal = %+v, want only the passing oracle line", lines)
	}
}

// TestOracleAtGreenDoesNotRecordWhenItCannotMakeADirectory: recording is best
// effort. A run whose commit is unknown (a dirty tree), or whose directory
// cannot be made plain, goes ahead without one and still passes.
func TestOracleAtGreenDoesNotRecordWhenItCannotMakeADirectory(t *testing.T) {
	t.Parallel()
	t.Run("an untracked file means no clean commit", func(t *testing.T) {
		t.Parallel()
		o := &recordingOracle{files: map[string]string{"login.svg": loginSVG}}
		r := newRecordRun(t, o)
		if err := os.WriteFile(filepath.Join(r.lease.Dir, "untracked.txt"), []byte("x"), 0o644); err != nil {
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
	})
	t.Run("a file where a directory belongs", func(t *testing.T) {
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
	})
}

// TestOracleAtGreenWritingNothingJournalsNothing: a run that wrote no
// recording leaves no line and no directory behind.
func TestOracleAtGreenWritingNothingJournalsNothing(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{}
	r := newRecordRun(t, o)
	if res := r.atGreen(); res.Outcome != outcome.Green {
		t.Fatalf("outcome = %q, want green", res.Outcome)
	}
	if runs := o.seen(); len(runs) != 1 || runs[0].recordDir != r.recordDir(firstRun) {
		t.Errorf("oracle runs = %+v, want one given the record dir", runs)
	}
	if lines := r.journaled(); len(lines) != 1 || lines[0].Event != "oracle" {
		t.Errorf("journal = %+v, want only the oracle line", lines)
	}
	mustNotExist(t, r.recordDir(firstRun))
}

// TestRunJournalsRecordingsBeforeTheSliceVerifies drives the whole loop: each
// slice whose oracle passed has one recorded line right after its oracle
// line and before it verified, keyed by the commit the oracle ran at and the
// run, and the recordings are on disk under the jig home.
func TestRunJournalsRecordingsBeforeTheSliceVerifies(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	o := &recordingOracle{files: map[string]string{"login.svg": loginSVG}}
	d.Oracle = o.run
	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsID(report.Green, "a") {
		t.Fatalf("Green = %v, want slice a among them", report.Green)
	}
	id, err := st.ID()
	if err != nil {
		t.Fatal(err)
	}

	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	passes := 0
	for i, l := range lines {
		if l.Event != "oracle" || l.Outcome != "pass" {
			continue
		}
		passes++
		if i+1 >= len(lines) || lines[i+1].Event != "recorded" {
			t.Errorf("the line after slice %s's passing oracle run is not its recorded line; journal: %+v", l.Slice, lines)
			continue
		}
		rec := lines[i+1]
		if rec.Slice != l.Slice || rec.Commit != l.Commit || rec.Attempt != l.Attempt || rec.RecordRun == "" || len(rec.Recordings) != 1 || rec.Recordings[0].File != "login.svg" {
			t.Errorf("recorded line %+v does not match the oracle line %+v", rec, l)
		}
		// The slice's verified line for this attempt is journaled after the
		// recorded line, never before the oracle ran.
		verifiedAt := -1
		for j, v := range lines {
			if v.Event == "verified" && v.Slice == l.Slice && v.Attempt == l.Attempt {
				verifiedAt = j
				break
			}
		}
		if verifiedAt <= i+1 {
			t.Errorf("slice %s attempt %d: verified line at %d, want it after its recorded line at %d", l.Slice, l.Attempt, verifiedAt, i+1)
		}
		dir, err := home.RecordDir(fx.Home, id, fx.Ticket, rec.Commit, rec.RecordRun)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "login.svg")); err != nil {
			t.Errorf("slice %s's recording is not under the jig home: %v", l.Slice, err)
		}
	}
	if passes == 0 {
		t.Fatalf("no passing oracle line; journal: %+v", lines)
	}
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
		return collectRecordings(top, dir)
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
		recs, dropped, err := collectRecordings(top, filepath.Join(top, "id", "T", "recordings", "sha", "run"))
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
		if _, _, err := collectRecordings(top, dir); err == nil || !strings.Contains(err.Error(), "is not a plain directory") {
			t.Errorf("err = %v, want the refusal", err)
		}
	})
	t.Run("a directory outside the evidence directory is refused", func(t *testing.T) {
		t.Parallel()
		top := filepath.Join(t.TempDir(), "evidence")
		if _, _, err := collectRecordings(top, t.TempDir()); err == nil {
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
