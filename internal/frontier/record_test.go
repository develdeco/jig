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
// wrote when the run passes, and journals it with the commit it ran at.

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
		ticket: "JIG-1",
	}
	return r
}

// atGreen runs the oracle at the builder's reported green.
func (r *recordRun) atGreen() outcome.Result {
	r.t.Helper()
	res := outcome.Result{Outcome: outcome.Green, Commit: r.head}
	return r.rc.oracleAtGreen(store.Slice{ID: "a"}, r.lease, 1, session.Dispatch{}, "", recordTestOracle, r.start, res)
}

// recordDir is where the run's recordings belong.
func (r *recordRun) recordDir() string {
	r.t.Helper()
	dir, err := home.RecordDir(r.home, r.storeID, "JIG-1", r.head)
	if err != nil {
		r.t.Fatal(err)
	}
	return dir
}

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

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s: Lstat err = %v, want it gone", path, err)
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
// "recorded" line keyed by the commit the oracle ran at, tags give a
// recording its scenario, flow, step and caption, a recording without a tag
// takes its scenario from its name, and the files stay on disk. The oracle
// line is exactly what it was without recording, so the gate can still reuse
// it.
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

	dir := r.recordDir()
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
	if rec.Slice != "a" || rec.Commit != r.head || rec.Attempt != 1 || rec.Outcome != "" {
		t.Errorf("recorded line = %+v, want slice a, attempt 1, commit %s and no outcome", rec, r.head)
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

// TestOracleAtGreenDiscardsAFailedRunsRecordings: only a green scenario is
// evidence, so a red run journals no recorded line and leaves nothing.
func TestOracleAtGreenDiscardsAFailedRunsRecordings(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{files: map[string]string{"login.svg": loginSVG}, fail: true}
	r := newRecordRun(t, o)

	if res := r.atGreen(); res.Outcome == outcome.Green {
		t.Fatalf("a red oracle run came back green: %+v", res)
	}
	if runs := o.seen(); len(runs) != 1 || runs[0].recordDir != r.recordDir() {
		t.Fatalf("oracle runs = %+v, want one that was given the record dir", runs)
	}
	lines := r.journaled()
	if got := recordedLines(lines); len(got) != 0 {
		t.Errorf("recorded lines after a red run: %+v", got)
	}
	if len(lines) != 1 || lines[0].Event != "oracle" || lines[0].Outcome != "fail" {
		t.Errorf("journal = %+v, want only the failed oracle line", lines)
	}
	mustNotExist(t, r.recordDir())
}

// TestOracleAtGreenRefusesTheWholeCollection: a recording that fails the
// media rules (here an empty png) refuses everything the run wrote, journals
// why, removes the directory, and still leaves the slice green.
func TestOracleAtGreenRefusesTheWholeCollection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"an empty recording", map[string]string{"good.svg": loginSVG, "empty.png": ""}, `file "empty.png" is empty`},
		{"a tag that is not valid", map[string]string{"good.svg": loginSVG, "good.svg.json": `{"scenario":"x","colour":"red"}`}, `tag "good.svg.json" is not valid`},
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
			if rec[0].Commit != r.head || len(rec[0].Recordings) != 0 {
				t.Errorf("recorded line = %+v, want the commit and no recordings", rec[0])
			}
			for _, host := range []string{r.home, filepath.ToSlash(r.home)} {
				if strings.Contains(rec[0].Outcome, host) {
					t.Errorf("the refusal names a path of this machine: %q", rec[0].Outcome)
				}
			}
			mustNotExist(t, r.recordDir())
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
		if _, err := os.Lstat(filepath.Join(r.recordDir(), name)); err != nil {
			t.Errorf("%s was not left in place: %v", name, err)
		}
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
		parent := filepath.Dir(r.recordDir()) // .../<ticket>/recordings
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
	if runs := o.seen(); len(runs) != 1 || runs[0].recordDir != r.recordDir() {
		t.Errorf("oracle runs = %+v, want one given the record dir", runs)
	}
	if lines := r.journaled(); len(lines) != 1 || lines[0].Event != "oracle" {
		t.Errorf("journal = %+v, want only the oracle line", lines)
	}
	mustNotExist(t, r.recordDir())
}

// TestOracleAtGreenClearsAnEarlierRunsRecordings: a second oracle run at the
// same commit (a fix turn that changed nothing) records into a fresh
// directory, so what an earlier run left is not collected again.
func TestOracleAtGreenClearsAnEarlierRunsRecordings(t *testing.T) {
	t.Parallel()
	o := &recordingOracle{files: map[string]string{"second.svg": loginSVG}}
	r := newRecordRun(t, o)
	if err := os.MkdirAll(r.recordDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(r.recordDir(), "first.svg")
	if err := os.WriteFile(stale, []byte(loginSVG), 0o644); err != nil {
		t.Fatal(err)
	}

	r.atGreen()

	rec := recordedLines(r.journaled())
	if len(rec) != 1 || len(rec[0].Recordings) != 1 || rec[0].Recordings[0].File != "second.svg" || rec[0].Outcome != "" {
		t.Fatalf("recorded lines = %+v, want only second.svg", rec)
	}
	mustNotExist(t, stale)
}

// TestRunJournalsRecordingsBeforeTheSliceVerifies drives the whole loop: each
// slice whose oracle passed has one recorded line right after its oracle
// line and before it verified, keyed by the commit the oracle ran at, and the
// recordings are on disk under the jig home.
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
	verified := map[string]bool{}
	for i, l := range lines {
		if l.Event != "oracle" || l.Outcome != "pass" {
			continue
		}
		passes++
		if i+1 >= len(lines) || lines[i+1].Event != "recorded" {
			t.Errorf("the line after slice %s's passing oracle run is %+v, want its recorded line", l.Slice, lines[min(i+1, len(lines)-1)])
			continue
		}
		rec := lines[i+1]
		if rec.Slice != l.Slice || rec.Commit != l.Commit || rec.Attempt != l.Attempt || len(rec.Recordings) != 1 || rec.Recordings[0].File != "login.svg" {
			t.Errorf("recorded line %+v does not match the oracle line %+v", rec, l)
		}
		for j := i + 1; j < len(lines); j++ {
			if lines[j].Event == "verified" && lines[j].Slice == l.Slice {
				verified[l.Slice] = true
				if j < i+2 {
					t.Errorf("slice %s verified before its recording was journaled", l.Slice)
				}
				break
			}
		}
		dir, err := home.RecordDir(fx.Home, id, fx.Ticket, l.Commit)
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
	if len(verified) == 0 {
		t.Errorf("no slice verified after its recording; journal: %+v", lines)
	}
}

// TestCollectRecordings covers the collection itself in a directory.
func TestCollectRecordings(t *testing.T) {
	t.Parallel()
	collect := func(t *testing.T, files map[string]string) ([]journal.Recording, []string, error) {
		t.Helper()
		dir := t.TempDir()
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		made, err := media.LstatPinned(dir)
		if err != nil {
			t.Fatal(err)
		}
		return collectRecordings(dir, made)
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
		for _, body := range []string{
			``, `not json`, `[]`, `{"scenario":5}`, `{"step":"1"}`, `{"step":1.5}`, `{"step":-1}`,
			`{"unknown":1}`, `{"scenario":"a"} {"scenario":"b"}`, `{"scenario":"a"}}`, `null x`,
			`{"caption":"` + strings.Repeat("x", maxTagBytes) + `"}`,
		} {
			recs, _, err := collect(t, map[string]string{"a.png": "x", "a.png.json": body, "ok.svg": "y"})
			if err == nil || !strings.Contains(err.Error(), `"a.png.json"`) {
				t.Errorf("tag %.40q: recs=%+v err=%v, want a refusal naming a.png.json", body, recs, err)
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
	t.Run("a directory that is not the one jig made is refused", func(t *testing.T) {
		t.Parallel()
		dir, other := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		made, err := media.LstatPinned(other)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := collectRecordings(dir, made); err == nil || !strings.Contains(err.Error(), "not the directory jig made") {
			t.Errorf("err = %v, want the identity refusal", err)
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
