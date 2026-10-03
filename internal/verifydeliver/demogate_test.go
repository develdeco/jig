package verifydeliver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// demoStub is a session backend that plays a clean reviewer for every
// review dispatch and hands each demo dispatch to onDemo, recording both
// kinds. It is one test's own value: a test that uses it does not run its
// dispatches in parallel.
type demoStub struct {
	t       *testing.T
	reviews []session.Dispatch
	demos   []session.Dispatch
	onDemo  func(d session.Dispatch) error
}

func (s *demoStub) Run(d session.Dispatch) error {
	if d.Slice == session.GateDemoSlice {
		s.demos = append(s.demos, d)
		return s.onDemo(d)
	}
	s.reviews = append(s.reviews, d)
	writeMustReviewResult(s.t, d)
	return nil
}

// writeDemoResult writes a result.json for d listing the given files, each
// captioned, with the given summary.
func writeDemoResult(t *testing.T, d session.Dispatch, summary string, files ...string) {
	t.Helper()
	res := DemoResult{Media: []DemoMedia{}, Summary: summary}
	for _, f := range files {
		res.Media = append(res.Media, DemoMedia{File: f, Caption: "caption of " + f})
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.ResultJSON, data, 0o644); err != nil {
		t.Fatalf("write demo.result.json: %v", err)
	}
}

// readJSONMap reads path as a plain JSON object.
func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return m
}

// demoLeaseAt returns a lease with a committed change on top of the target
// (so base..head is a real diff), its head sha, the target's sha, and a store.
func demoLeaseAt(t *testing.T) (dir, base, head string, st *store.Store) {
	t.Helper()
	dir = newReviewLease(t, "main")
	var err error
	base, err = gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeReviewFile(t, dir, "feature.go", "package feature\n")
	head = commitReviewLease(t, dir, "the change")
	return dir, base, head, newReviewStore(t)
}

// --- reviewerGateSource.Demo ------------------------------------------------------

// TestDemoDispatchWritesTheContractAndDispatchesInTheLease pins what the
// session is handed: demo.json's exact keys and values, and a dispatch in
// the gate lease with the media directory as its one extra directory.
func TestDemoDispatchWritesTheContractAndDispatchesInTheLease(t *testing.T) {
	t.Parallel()

	dir, base, head, st := demoLeaseAt(t)
	mediaDir := filepath.Join(t.TempDir(), "evidence", "id", "JIG-1", head)
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(t.TempDir(), "brief.md")

	var seen session.Dispatch
	var demoJSON []byte
	staleAtDispatch := false
	staleResult := demoResultJSONPath(st, "JIG-1", 2)
	if err := os.MkdirAll(filepath.Dir(staleResult), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleResult, []byte("garbage from a failed attempt"), 0o644); err != nil {
		t.Fatal(err)
	}
	backend := &demoStub{t: t, onDemo: func(d session.Dispatch) error {
		seen = d
		var err error
		if demoJSON, err = os.ReadFile(d.SliceJSON); err != nil {
			t.Fatalf("read demo.json: %v", err)
		}
		if _, err := os.Stat(d.ResultJSON); err == nil {
			staleAtDispatch = true
		}
		writeMediaFile(t, d.ExtraWriteDir, "shot.png", 12)
		writeDemoResult(t, d, "one shot", "shot.png")
		return nil
	}}

	src := NewReviewerGateSource(backend).(DemoSource)
	res, err := src.Demo(DemoInput{
		Store: st, Ticket: "JIG-1", Round: 2, LeaseDir: dir, Model: "rung-b",
		Intent:  Intent{Source: IntentSourceBrief, Path: briefPath},
		BaseSHA: base, HeadSHA: head, MediaDir: mediaDir,
	})
	if err != nil {
		t.Fatalf("Demo: %v", err)
	}
	if res.Summary != "one shot" || len(res.Media) != 1 || res.Media[0].File != "shot.png" {
		t.Fatalf("result = %+v", res)
	}
	if staleAtDispatch {
		t.Error("a stale demo.result.json from an earlier attempt was still present when the backend ran")
	}

	wantDemoPath := demoJSONPath(absPath(mediaDir))
	wantResultPath := demoResultJSONPath(st, "JIG-1", 2)
	if !strings.HasSuffix(filepath.ToSlash(wantDemoPath), "evidence/id/JIG-1/"+head+".demo.json") || !strings.HasSuffix(filepath.ToSlash(wantResultPath), "JIG-1/work/gate.round-2.demo.result.json") {
		t.Fatalf("contract paths = %q, %q", wantDemoPath, wantResultPath)
	}
	if seen.Slice != "gate-demo" {
		t.Errorf("dispatch Slice = %q, want %q", seen.Slice, "gate-demo")
	}
	if seen.Ticket != "JIG-1" || seen.Attempt != 2 || seen.Worktree != dir || seen.Model != "rung-b" || !seen.Screen {
		t.Errorf("dispatch = %+v", seen)
	}
	if seen.SliceJSON != wantDemoPath || seen.ResultJSON != wantResultPath {
		t.Errorf("dispatch paths = %q, %q; want %q, %q", seen.SliceJSON, seen.ResultJSON, wantDemoPath, wantResultPath)
	}
	if seen.ExtraWriteDir != absPath(mediaDir) {
		t.Errorf("dispatch ExtraWriteDir = %q, want the absolute media dir %q", seen.ExtraWriteDir, absPath(mediaDir))
	}
	if seen.Prompt != RenderDemoPrompt(DemoRequest{Ticket: "JIG-1", Round: 2, BaseSHA: base, HeadSHA: head, MediaDir: absPath(mediaDir)}, wantDemoPath, wantResultPath) {
		t.Errorf("dispatch prompt is not the rendered demo prompt:\n%s", seen.Prompt)
	}

	// demo.json: read as raw keys, so a renamed json tag cannot round-trip
	// through DemoRequest unnoticed.
	var raw map[string]any
	if err := json.Unmarshal(demoJSON, &raw); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(sortedKeys(raw)); got != "[base_sha head_sha intent limits media_dir round ticket]" {
		t.Fatalf("demo.json keys = %s", got)
	}
	if raw["ticket"] != "JIG-1" || raw["round"] != float64(2) || raw["base_sha"] != base || raw["head_sha"] != head || raw["media_dir"] != absPath(mediaDir) {
		t.Errorf("demo.json = %v", raw)
	}
	intent := raw["intent"].(map[string]any)
	if got := fmt.Sprint(sortedKeys(intent)); got != "[path source]" || intent["source"] != "brief" || intent["path"] != briefPath {
		t.Errorf("demo.json intent = %v", intent)
	}
	limits := raw["limits"].(map[string]any)
	if got := fmt.Sprint(sortedKeys(limits)); got != "[image_extensions max_files max_image_bytes max_video_bytes video_extensions]" {
		t.Errorf("demo.json limits keys = %s", got)
	}
	if limits["max_files"] != float64(50) || limits["max_image_bytes"] != float64(10485760) || limits["max_video_bytes"] != float64(104857600) {
		t.Errorf("demo.json limits = %v", limits)
	}
	if !filepath.IsAbs(raw["media_dir"].(string)) {
		t.Errorf("media_dir %q is not absolute", raw["media_dir"])
	}
}

// TestDemoDispatchHoldsTheLeaseToTheReviewersGuard: the session may leave
// untracked scratch behind, but a commit or a tracked-file edit is refused,
// and the lease is restored to the reviewed head whatever happened.
func TestDemoDispatchHoldsTheLeaseToTheReviewersGuard(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		act     func(t *testing.T, dir string)
		refused bool
	}{
		{"untracked scratch is fine", func(t *testing.T, dir string) { writeReviewFile(t, dir, "scratch/out.tmp", "build output") }, false},
		{"a commit", func(t *testing.T, dir string) {
			writeReviewFile(t, dir, "sneaky.go", "the demo edited")
			commitReviewLease(t, dir, "a demo committed, which it must never do")
		}, true},
		{"an uncommitted edit to a tracked file", func(t *testing.T, dir string) { writeReviewFile(t, dir, "feature.go", "package changed\n") }, true},
		{"a staged edit", func(t *testing.T, dir string) {
			writeReviewFile(t, dir, "feature.go", "package staged\n")
			if _, err := gitx.Run(dir, "add", "feature.go"); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"a deleted tracked file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "feature.go")); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, _, head, st := demoLeaseAt(t)
			mediaDir := t.TempDir()
			backend := &demoStub{t: t, onDemo: func(d session.Dispatch) error {
				c.act(t, dir)
				writeDemoResult(t, d, "nothing to show")
				return nil
			}}
			_, err := NewReviewerGateSource(backend).(DemoSource).Demo(DemoInput{
				Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir, BaseSHA: head, HeadSHA: head, MediaDir: mediaDir,
			})
			switch {
			case c.refused && (err == nil || !strings.Contains(err.Error(), "the demo session changed the gate lease")):
				t.Fatalf("Demo = %v, want the lease-changed refusal", err)
			case !c.refused && err != nil:
				t.Fatalf("Demo: %v", err)
			}

			if got, err := gitx.RevParse(dir, "HEAD"); err != nil || got != head {
				t.Errorf("lease HEAD after the demo = %q, %v; want the reviewed head %q restored", got, err, head)
			}
			if status, err := gitx.Run(dir, "status", "--porcelain"); err != nil || status != "" {
				t.Errorf("lease is not pristine after the demo: %q, %v", status, err)
			}
		})
	}
}

func TestDemoDispatchRefusesALeaseNotAtTheReviewedHead(t *testing.T) {
	t.Parallel()
	dir, base, _, st := demoLeaseAt(t)
	ran := false
	backend := &demoStub{t: t, onDemo: func(session.Dispatch) error { ran = true; return nil }}
	_, err := NewReviewerGateSource(backend).(DemoSource).Demo(DemoInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir, BaseSHA: base, HeadSHA: base, MediaDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "not the reviewed head") {
		t.Fatalf("Demo = %v, want a refusal naming the head mismatch", err)
	}
	if ran {
		t.Error("a session was dispatched against a lease that is not at the reviewed head")
	}
}

// TestDemoDispatchFailures: what the demo session and its backend do wrong is
// a demoFailure, told apart from a result that breaks the contract. A failure
// leaves no result file in the store's work directory, whatever the backend
// wrote there, since the file is the backend's and the store's push would
// commit it; a result that breaks the contract is the session's own words and
// stays as written.
func TestDemoDispatchFailures(t *testing.T) {
	t.Parallel()
	const fallback = `{"outcome":"failed","summary":"no result block; denied tool calls: Write x.png","raw_tail":"tail"}`
	cases := []struct {
		name    string
		onDemo  func(d session.Dispatch) error
		want    string
		failure bool
	}{
		{"the backend fails", func(session.Dispatch) error { return errors.New("boom") }, "the demo session failed: boom", true},
		{"the backend fails after writing a file", func(d session.Dispatch) error {
			if err := os.WriteFile(d.ResultJSON, []byte(fallback), 0o644); err != nil {
				return err
			}
			return errors.New("boom")
		}, "the demo session failed: boom", true},
		{"no result is written", func(session.Dispatch) error { return nil }, "the demo session failed: it wrote no demo result", true},
		{"the backend recorded its own outcome in place of a result", func(d session.Dispatch) error {
			return os.WriteFile(d.ResultJSON, []byte(fallback), 0o644)
		}, "the demo session failed: it wrote no demo result, and the backend recorded its own outcome in its place (failed: no result block; denied tool calls: Write x.png)", true},
		{"the result is not the contract", func(d session.Dispatch) error {
			return os.WriteFile(d.ResultJSON, []byte(`{"media":[]}`), 0o644)
		}, `demo.result.json is invalid: missing "summary"`, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir, base, head, st := demoLeaseAt(t)
			backend := &demoStub{t: t, onDemo: c.onDemo}
			_, err := NewReviewerGateSource(backend).(DemoSource).Demo(DemoInput{
				Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir, BaseSHA: base, HeadSHA: head, MediaDir: t.TempDir(),
			})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Demo = %v, want an error containing %q", err, c.want)
			}
			var f *demoFailure
			if errors.As(err, &f) != c.failure {
				t.Errorf("Demo = %T %v; a failure of the session or its backend = %v, want %v", err, err, !c.failure, c.failure)
			}
			_, statErr := os.Stat(demoResultJSONPath(st, "JIG-1", 1))
			if c.failure && statErr == nil {
				t.Errorf("a failed demo left its result file in the store's work directory")
			}
			if !c.failure && statErr != nil {
				t.Errorf("the session's own result file was not kept as written: %v", statErr)
			}
		})
	}
}

// --- gateDemo: every refusal ---------------------------------------------------------

// TestGateDemoRefusals plays one demo attempt per refusal against one lease
// and one store, each in its own round of the same head: every one is
// recorded as a refused demo.yaml with its reason and nothing renamed, and a
// refused demo never counts as the head's demo, so the last attempt, a good
// one, still records. The good one also finds media_dir emptied of whatever
// the earlier attempts left, then a further round finds that demo.
func TestGateDemoRefusals(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	dir, _, head, _ := demoLeaseAt(t)
	// The lease's origin/main is its own seed; resolveFullBase needs only that
	// ref, and the store and home are the fixture's.
	stub := &demoStub{t: t}
	src := NewReviewerGateSource(stub).(DemoSource)

	var round int
	attempt := func(onDemo func(d session.Dispatch) error) DemoReport {
		t.Helper()
		round++
		mkRoundDir(t, d.Store, fx.Ticket, round)
		stub.onDemo = onDemo
		return gateDemo(d, src, DemoInput{
			Store: d.Store, Ticket: fx.Ticket, Round: round, LeaseDir: dir, Model: "rung-a", HeadSHA: head,
		}, "fixture-repo", "main")
	}
	mediaDir, err := demoMediaDir(d, fx.Ticket, head)
	if err != nil {
		t.Fatal(err)
	}
	entries := func() []string {
		t.Helper()
		des, err := os.ReadDir(mediaDir)
		if err != nil {
			t.Fatalf("read media_dir: %v", err)
		}
		var names []string
		for _, e := range des {
			names = append(names, e.Name())
		}
		return names
	}
	refused := func(name string, rep DemoReport, reason string, remaining ...string) {
		t.Helper()
		if rep.Status != DemoRefused || !strings.Contains(rep.Reason, reason) {
			t.Fatalf("%s: report = %+v, want a refusal containing %q", name, rep, reason)
		}
		rec, ok, err := readDemoRecord(d.Store, fx.Ticket, round)
		if err != nil || !ok || rec.Status != DemoRefused || rec.HeadSHA != head || rec.Reason != rep.Reason || len(rec.Media) != 0 {
			t.Fatalf("%s: demo.yaml = %+v ok=%v err=%v", name, rec, ok, err)
		}
		if got := entries(); fmt.Sprint(got) != fmt.Sprint(remaining) {
			t.Fatalf("%s: media_dir holds %v after the refusal, want %v (nothing renamed)", name, got, remaining)
		}
	}

	// A failure of the session or its backend is recorded as its code alone;
	// its text is the report's (TestGateDemoFailureOfTheSessionRecordsOnlyItsCode).
	failed := attempt(func(session.Dispatch) error { return errors.New("boom") })
	refused("the backend fails", failed, "the demo session failed: INTERNAL")
	if failed.Reason != "the demo session failed: INTERNAL" || failed.Detail != "boom" {
		t.Fatalf("the backend fails: reason %q, detail %q; want the code and the text apart", failed.Reason, failed.Detail)
	}
	refused("no result", attempt(func(session.Dispatch) error { return nil }),
		"the demo session failed: DEMO_NO_RESULT")
	refused("an invalid result", attempt(func(d session.Dispatch) error {
		return os.WriteFile(d.ResultJSON, []byte(`{"media":[{"file":"a.png"}],"summary":"s"}`), 0o644)
	}), "empty caption")

	refused("the lease changes", attempt(func(d session.Dispatch) error {
		writeMediaFile(t, d.ExtraWriteDir, "a.png", 5)
		writeReviewFile(t, dir, "feature.go", "package edited\n")
		writeDemoResult(t, d, "s", "a.png")
		return nil
	}), "changed the gate lease", "a.png")
	if got, err := gitx.RevParse(dir, "HEAD"); err != nil || got != head {
		t.Fatalf("lease HEAD = %q, %v; want %q", got, err, head)
	}

	refused("a file outside media_dir", attempt(func(d session.Dispatch) error {
		writeMediaFile(t, filepath.Dir(d.ExtraWriteDir), "outside.png", 5)
		writeDemoResult(t, d, "s", "../outside.png")
		return nil
	}), "not a plain file name directly inside media_dir")

	refused("a subdirectory", attempt(func(d session.Dispatch) error {
		if err := os.Mkdir(filepath.Join(d.ExtraWriteDir, "sub"), 0o755); err != nil {
			return err
		}
		writeMediaFile(t, filepath.Join(d.ExtraWriteDir, "sub"), "a.png", 5)
		writeDemoResult(t, d, "s", "sub/a.png")
		return nil
	}), "not a plain file name directly inside media_dir", "sub")

	refused("a directory named like a file", attempt(func(d session.Dispatch) error {
		if err := os.Mkdir(filepath.Join(d.ExtraWriteDir, "shots.png"), 0o755); err != nil {
			return err
		}
		writeDemoResult(t, d, "s", "shots.png")
		return nil
	}), "not a regular file", "shots.png")

	refused("a bad extension", attempt(func(d session.Dispatch) error {
		writeMediaFile(t, d.ExtraWriteDir, "notes.txt", 5)
		writeDemoResult(t, d, "s", "notes.txt")
		return nil
	}), "not an allowed image", "notes.txt")

	refused("an oversize image", attempt(func(d session.Dispatch) error {
		writeSparseFile(t, d.ExtraWriteDir, "big.png", 10<<20+1)
		writeDemoResult(t, d, "s", "big.png")
		return nil
	}), "10485761 bytes", "big.png")

	refused("an empty file", attempt(func(d session.Dispatch) error {
		writeMediaFile(t, d.ExtraWriteDir, "empty.png", 0)
		writeDemoResult(t, d, "s", "empty.png")
		return nil
	}), `"empty.png" is empty`, "empty.png")

	refused("too many files", attempt(func(d session.Dispatch) error {
		var names []string
		for i := 0; i < 51; i++ {
			name := fmt.Sprintf("f%02d.png", i)
			writeMediaFile(t, d.ExtraWriteDir, name, 3)
			names = append(names, name)
		}
		writeDemoResult(t, d, "s", names...)
		return nil
	}), "lists 51 files", func() []string {
		var names []string
		for i := 0; i < 51; i++ {
			names = append(names, fmt.Sprintf("f%02d.png", i))
		}
		return names
	}()...)

	// The two link cases run only where this platform and account can create
	// the link; a probe decides, so one that cannot never skips the rest.
	probe := t.TempDir()
	outside := t.TempDir()
	writeMediaFile(t, outside, "secret.png", 5)
	if makeFileLink(filepath.Join(outside, "secret.png"), filepath.Join(probe, "l")) == nil {
		refused("a link", attempt(func(d session.Dispatch) error {
			if err := makeFileLink(filepath.Join(outside, "secret.png"), filepath.Join(d.ExtraWriteDir, "link.png")); err != nil {
				return err
			}
			writeDemoResult(t, d, "s", "link.png")
			return nil
		}), "not a regular file", "link.png")
	}
	if makeDirLink(outside, filepath.Join(probe, "d")) == nil {
		rep := attempt(func(d session.Dispatch) error {
			if err := os.RemoveAll(d.ExtraWriteDir); err != nil {
				return err
			}
			if err := makeDirLink(outside, d.ExtraWriteDir); err != nil {
				return err
			}
			writeDemoResult(t, d, "s", "secret.png")
			return nil
		})
		if rep.Status != DemoRefused || !strings.Contains(rep.Reason, "media_dir is not a plain directory") {
			t.Fatalf("report = %+v, want the linked media_dir refused", rep)
		}

		// A directory above media_dir swapped for a link leaves media_dir a
		// plain directory on the way in, but not the one jig made: nothing is
		// recorded, and nothing is renamed in the tree the link reaches.
		elsewhere := t.TempDir()
		var parent string
		rep = attempt(func(d session.Dispatch) error {
			parent = filepath.Dir(d.ExtraWriteDir)
			shadow := filepath.Join(elsewhere, filepath.Base(d.ExtraWriteDir))
			if err := os.MkdirAll(shadow, 0o755); err != nil {
				return err
			}
			writeMediaFile(t, shadow, "a.png", 5)
			if err := os.Rename(parent, parent+".moved"); err != nil {
				return err
			}
			if err := makeDirLink(elsewhere, parent); err != nil {
				return err
			}
			writeDemoResult(t, d, "s", "a.png")
			return nil
		})
		if rep.Status != DemoRefused || !strings.Contains(rep.Reason, "not the directory jig made") {
			t.Fatalf("report = %+v, want the swapped parent refused", rep)
		}
		if got := fmt.Sprint(mustList(t, filepath.Join(elsewhere, filepath.Base(mediaDir)))); got != "[a.png]" {
			t.Errorf("the tree behind the swapped parent holds %s, want only the session's own a.png", got)
		}
		rec, ok, err := readDemoRecord(d.Store, fx.Ticket, round)
		if err != nil || !ok || rec.Status != DemoRefused || len(rec.Media) != 0 {
			t.Fatalf("demo.yaml after a swapped parent = %+v ok=%v err=%v", rec, ok, err)
		}
		// The swap stays. The next attempt must not clear, make or record
		// anything through it: no session is dispatched, and what the link
		// reaches is untouched.
		dispatched := len(stub.demos)
		rep = attempt(func(session.Dispatch) error {
			t.Error("a demo session was dispatched through a swapped parent")
			return nil
		})
		if rep.Status != DemoRefused || !strings.Contains(rep.Reason, "is not a plain directory") {
			t.Fatalf("report = %+v, want the persisting swapped parent refused before any dispatch", rep)
		}
		if len(stub.demos) != dispatched {
			t.Errorf("demo dispatches went from %d to %d", dispatched, len(stub.demos))
		}
		if got := fmt.Sprint(mustList(t, filepath.Join(elsewhere, filepath.Base(mediaDir)))); got != "[a.png]" {
			t.Errorf("the tree behind the swapped parent holds %s after the next attempt, want the session's a.png untouched", got)
		}
		// Put the tree back for the attempts that follow.
		if err := os.Remove(parent); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(parent+".moved", parent); err != nil {
			t.Fatal(err)
		}
	}

	// A good attempt: media_dir starts empty, the files are renamed in
	// order, and the demo is recorded.
	rep := attempt(func(d session.Dispatch) error {
		if got := fmt.Sprint(mustList(t, d.ExtraWriteDir)); got != "[]" {
			t.Errorf("media_dir held %s when the session started, want it cleared of earlier attempts' files", got)
		}
		writeMediaFile(t, d.ExtraWriteDir, "second.mp4", 7)
		writeMediaFile(t, d.ExtraWriteDir, "first.png", 6)
		// What the session leaves beside its files is no part of the demo: an
		// unlisted file (one named like jig's own too), a subdirectory, and a
		// link to somewhere else.
		writeMediaFile(t, d.ExtraWriteDir, "notes.txt", 4)
		writeMediaFile(t, d.ExtraWriteDir, "demo-7.png", 4)
		if err := os.MkdirAll(filepath.Join(d.ExtraWriteDir, "scratch"), 0o755); err != nil {
			return err
		}
		writeMediaFile(t, filepath.Join(d.ExtraWriteDir, "scratch"), "x.png", 4)
		if makeDirLink(outside, filepath.Join(d.ExtraWriteDir, "j")) != nil {
			t.Log("directory links unavailable here; the good attempt leaves none")
		}
		writeDemoResult(t, d, "two files", "first.png", "second.mp4")
		return nil
	})
	if rep.Status != DemoRecorded || len(rep.Media) != 2 || rep.Media[0].Name != "demo-1.png" || rep.Media[1].Name != "demo-2.mp4" {
		t.Fatalf("the good attempt = %+v", rep)
	}
	if got := entries(); fmt.Sprint(got) != "[demo-1.png demo-2.mp4]" {
		t.Fatalf("media_dir holds %v after the good attempt", got)
	}
	if rep.Warning != "" {
		t.Errorf("warning = %q", rep.Warning)
	}
	// Clearing media_dir for this attempt removed the link a session had left
	// in its place, and pruning it after removed the one the good attempt left
	// inside: neither removed what it pointed at.
	if _, err := os.Stat(filepath.Join(outside, "secret.png")); err != nil {
		t.Errorf("the file behind a link was removed: %v", err)
	}

	// The next clean round on this head finds that demo and runs none.
	dispatches := len(stub.demos)
	next := attempt(func(session.Dispatch) error {
		t.Error("a second demo was dispatched for a head that has one")
		return nil
	})
	if next.Status != DemoExisting || next.Round != round-1 {
		t.Fatalf("the round after a recorded demo = %+v, want existing for round %d", next, round-1)
	}
	if len(stub.demos) != dispatches {
		t.Errorf("demo dispatches went from %d to %d", dispatches, len(stub.demos))
	}
	if _, ok, err := readDemoRecord(d.Store, fx.Ticket, round); ok || err != nil {
		t.Errorf("an existing-demo round wrote a demo.yaml: ok=%v err=%v", ok, err)
	}
}

// spellings returns the ways text can spell the directory dir: as jig
// prints it, with forward slashes, and inside a JSON string, where every
// backslash doubles.
func spellings(dir string) []string {
	quoted, _ := json.Marshal(dir)
	return []string{dir, filepath.ToSlash(dir), strings.Trim(string(quoted), `"`)}
}

// filesSpelling returns every file under root, its .git directory left out,
// whose bytes spell one of dirs.
func filesSpelling(t *testing.T, root string, dirs ...string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if e.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, dir := range dirs {
			for _, sp := range spellings(dir) {
				if strings.Contains(string(data), sp) {
					found = append(found, path)
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestGateDemoLeavesNoJigHomePathInTheStore: what jig writes to the store's
// working tree for a demo, which the store commits and pushes, names no
// directory of the jig home. demo.json carries the absolute media_dir, so it
// is machine-local and lives beside the media, not in the store. The session
// here writes no path; what a session's own words hold is recorded as written
// (TestGateDemoRecordsTheSessionsOwnWordsAsWritten).
func TestGateDemoLeavesNoJigHomePathInTheStore(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	dir, _, head, _ := demoLeaseAt(t)
	stub := &demoStub{t: t}
	var handed session.Dispatch
	stub.onDemo = func(sd session.Dispatch) error {
		handed = sd
		writeMediaFile(t, sd.ExtraWriteDir, "a.png", 5)
		writeDemoResult(t, sd, "one shot", "a.png")
		return nil
	}
	mkRoundDir(t, d.Store, fx.Ticket, 1)
	rep := gateDemo(d, NewReviewerGateSource(stub).(DemoSource), DemoInput{
		Store: d.Store, Ticket: fx.Ticket, Round: 1, LeaseDir: dir, Model: "rung-a", HeadSHA: head,
	}, "fixture-repo", "main")
	if rep.Status != DemoRecorded {
		t.Fatalf("report = %+v, want a recorded demo", rep)
	}

	// The session was handed its input, and it is the media directory's own
	// sibling under the jig home.
	if got, want := handed.SliceJSON, handed.ExtraWriteDir+".demo.json"; got != want {
		t.Errorf("demo.json is at %s, want %s", got, want)
	}
	if got := readJSONMap(t, handed.SliceJSON)["media_dir"]; got != handed.ExtraWriteDir {
		t.Errorf("demo.json media_dir = %v, want %s", got, handed.ExtraWriteDir)
	}
	if got := filesSpelling(t, d.Store.Root, d.Home); len(got) != 0 {
		t.Errorf("the store's working tree names the jig home %s in %v", d.Home, got)
	}
}

// TestGateDemoRefusalNamesNoHostPath: a refusal's reason is committed to the
// store's demo.yaml and printed in the report, so it names the directories jig
// works in by role (media_dir, the jig home, the store), never by host path,
// whatever operating system error it carries, and never repeats a path the
// session listed, whatever spelling its backend gave it.
func TestGateDemoRefusalNamesNoHostPath(t *testing.T) {
	t.Parallel()

	const notPlain = `media entry 0 ("shot.png") is not a plain file name directly inside media_dir`
	cases := []struct {
		name   string
		before func(t *testing.T, d Deps)
		onDemo func(t *testing.T, sd session.Dispatch) error
		// listed, when set, replaces onDemo: the session writes shot.png in
		// media_dir and lists it as listed(media_dir).
		listed func(dir string) string
		want   string // the reason contains it
		absent string // and does not contain this
	}{
		{
			name: "the session removes media_dir",
			onDemo: func(t *testing.T, sd session.Dispatch) error {
				if err := os.RemoveAll(sd.ExtraWriteDir); err != nil {
					return err
				}
				writeDemoResult(t, sd, "s", "a.png")
				return nil
			},
			want: "media_dir cannot be read",
			// The directory is named as itself, not by the jig home it sits in.
			absent: "<jig home>",
		},
		{
			name: "the evidence directory is a file",
			before: func(t *testing.T, d Deps) {
				if err := os.WriteFile(filepath.Join(d.Home, "evidence"), []byte("not a directory"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			onDemo: func(t *testing.T, sd session.Dispatch) error {
				t.Error("a session was dispatched with nowhere to put its media")
				return nil
			},
		},
		// A session is told media_dir's absolute path, so listing a file by it
		// is a likely slip, and a herdr session on Windows was told its WSL
		// spelling. The refusal names the entry, not the string, whichever
		// spelling it is.
		{
			name:   "the session lists a file by its absolute path",
			listed: func(dir string) string { return filepath.Join(dir, "shot.png") },
			want:   notPlain,
		},
		{
			name:   "the session lists a file by its absolute path with forward slashes",
			listed: func(dir string) string { return filepath.ToSlash(filepath.Join(dir, "shot.png")) },
			want:   notPlain,
		},
		{
			name:   "the session lists a file by its WSL mount path",
			listed: func(dir string) string { return wslMountSpelling(filepath.Join(dir, "shot.png")) },
			want:   notPlain,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			dir, _, head, _ := demoLeaseAt(t)
			if c.before != nil {
				c.before(t, d)
			}
			onDemo := func(sd session.Dispatch) error { return c.onDemo(t, sd) }
			hosts := []string{d.Home, d.Store.Root}
			if c.listed != nil {
				onDemo = func(sd session.Dispatch) error {
					writeMediaFile(t, sd.ExtraWriteDir, "shot.png", 5)
					writeDemoResult(t, sd, "s", c.listed(sd.ExtraWriteDir))
					return nil
				}
				mediaDir, err := demoMediaDir(d, fx.Ticket, head)
				if err != nil {
					t.Fatal(err)
				}
				// Whatever the session listed, the directory part of it is a
				// host path too, in the spelling the session used.
				listed := c.listed(mediaDir)
				hosts = append(hosts, listed, listed[:strings.LastIndexAny(listed, `/\`)])
			}
			stub := &demoStub{t: t, onDemo: onDemo}
			mkRoundDir(t, d.Store, fx.Ticket, 1)
			rep := gateDemo(d, NewReviewerGateSource(stub).(DemoSource), DemoInput{
				Store: d.Store, Ticket: fx.Ticket, Round: 1, LeaseDir: dir, Model: "rung-a", HeadSHA: head,
			}, "fixture-repo", "main")
			if rep.Status != DemoRefused || !strings.Contains(rep.Reason, c.want) {
				t.Fatalf("report = %+v, want a refusal containing %q", rep, c.want)
			}
			if c.absent != "" && strings.Contains(rep.Reason, c.absent) {
				t.Errorf("the reason %q contains %q", rep.Reason, c.absent)
			}
			rec, ok, err := readDemoRecord(d.Store, fx.Ticket, 1)
			if err != nil || !ok || rec.Status != DemoRefused {
				t.Fatalf("demo.yaml = %+v ok=%v err=%v", rec, ok, err)
			}
			for _, reason := range []string{rep.Reason, rec.Reason} {
				for _, host := range hosts {
					for _, sp := range spellings(host) {
						if strings.Contains(reason, sp) {
							t.Errorf("the reason %q names the host path %s", reason, sp)
						}
					}
				}
			}
		})
	}
}

// TestGateDemoRecordsTheSessionsOwnWordsAsWritten: what jig itself writes for
// a demo names no host path, but the session's own words (its result file,
// its summary and its captions) are recorded as written, the way the
// reviewer's result.json summary is: jig does not filter or rewrite model
// prose, so a session that names media_dir, which it was told, has it recorded.
func TestGateDemoRecordsTheSessionsOwnWordsAsWritten(t *testing.T) {
	t.Parallel()

	for _, recorded := range []bool{true, false} {
		recorded := recorded
		name := "a refused demo"
		if recorded {
			name = "a recorded demo"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			dir, _, head, _ := demoLeaseAt(t)
			mediaDir, err := demoMediaDir(d, fx.Ticket, head)
			if err != nil {
				t.Fatal(err)
			}
			// The words are not tidy on purpose: edge whitespace, a line
			// break, runs of spaces, and more runes than a reason is capped
			// at. "As written" is every byte of them, so none of trimming,
			// collapsing or capping may touch them.
			summary := "  saved one screenshot in " + mediaDir + " and left notes in " + d.Home +
				"\n\n\tsecond   paragraph  with    runs of spaces  " + strings.Repeat("and more words ", 40) + "\n"
			caption := "\tthe page,   seen from " + mediaDir + "  \n  " + strings.Repeat("and again ", 50) + "  "
			for name, words := range map[string]string{"summary": summary, "caption": caption} {
				if n := len([]rune(words)); n <= demoReasonCap {
					t.Fatalf("the test's %s is %d runes, want more than the reason cap %d", name, n, demoReasonCap)
				}
			}
			listed := "a.png"
			if !recorded {
				listed = "missing.png" // listed and never written: the demo is refused
			}
			stub := &demoStub{t: t, onDemo: func(sd session.Dispatch) error {
				if recorded {
					writeMediaFile(t, sd.ExtraWriteDir, "a.png", 5)
				}
				data, err := json.Marshal(DemoResult{Media: []DemoMedia{{File: listed, Caption: caption}}, Summary: summary})
				if err != nil {
					return err
				}
				return os.WriteFile(sd.ResultJSON, data, 0o644)
			}}
			mkRoundDir(t, d.Store, fx.Ticket, 1)
			rep := gateDemo(d, NewReviewerGateSource(stub).(DemoSource), DemoInput{
				Store: d.Store, Ticket: fx.Ticket, Round: 1, LeaseDir: dir, Model: "rung-a", HeadSHA: head,
			}, "fixture-repo", "main")
			wantStatus := DemoRefused
			if recorded {
				wantStatus = DemoRecorded
			}
			if rep.Status != wantStatus {
				t.Fatalf("report = %+v, want a %s demo", rep, wantStatus)
			}

			// The session's result file is recorded as it wrote it, its words
			// and the paths in them intact.
			result := readJSONMap(t, demoResultJSONPath(d.Store, fx.Ticket, 1))
			if result["summary"] != summary {
				t.Errorf("the recorded result's summary = %q, want %q verbatim", result["summary"], summary)
			}
			if media, _ := result["media"].([]any); len(media) != 1 || media[0].(map[string]any)["caption"] != caption {
				t.Errorf("the recorded result's media = %v, want one file with the caption %q verbatim", result["media"], caption)
			}
			rec, ok, err := readDemoRecord(d.Store, fx.Ticket, 1)
			if err != nil || !ok || rec.Status != wantStatus {
				t.Fatalf("demo.yaml = %+v ok=%v err=%v", rec, ok, err)
			}
			if recorded {
				if rec.Summary != summary || rep.Summary != summary {
					t.Errorf("the recorded summary = %q (report %q), want %q verbatim", rec.Summary, rep.Summary, summary)
				}
				if len(rec.Media) != 1 || rec.Media[0].Caption != caption || len(rep.Media) != 1 || rep.Media[0].Caption != caption {
					t.Errorf("the recorded media = %+v (report %+v), want one file with the caption %q verbatim", rec.Media, rep.Media, caption)
				}
			}

			// Everything else in demo.yaml is jig's own, and names no host
			// path: the statuses, the head, the renamed files, their hashes
			// and sizes, and a refusal's reason.
			rec.Summary = ""
			for i := range rec.Media {
				rec.Media[i].Caption = ""
			}
			own := fmt.Sprintf("%+v", rec) + " " + rep.Reason
			for _, host := range []string{d.Home, d.Store.Root, mediaDir} {
				for _, sp := range spellings(host) {
					if strings.Contains(own, sp) {
						t.Errorf("jig's own fields in demo.yaml %q name the host path %s", own, sp)
					}
				}
			}
		})
	}
}

// storeHistory is every commit message and change the store's git holds, for
// asserting that a text reached no commit.
func storeHistory(t *testing.T, root string) string {
	t.Helper()
	out, err := gitx.Run(root, "log", "--all", "-p", "--format=%B")
	if err != nil {
		t.Fatalf("read the store's history: %v", err)
	}
	return out
}

// TestGateDemoFailureOfTheSessionRecordsOnlyItsCode: when the demo session or
// its backend fails, what jig writes into the store is jig's own words and the
// failure's code, and never the failure's text, which can hold anything a
// backend echoed: here the prompt and the paths of the dispatch in it, in the
// jig home. The report prints the text, once. Nothing of it reaches
// demo.yaml, the store's working tree or its history.
func TestGateDemoFailureOfTheSessionRecordsOnlyItsCode(t *testing.T) {
	t.Parallel()

	const marker = "WORDS-OF-THE-PROMPT-A-BACKEND-ECHOED"
	echoed := func(home string) string {
		p := filepath.Join(home, "evidence", "abc", "JIG-1", "head.demo.json")
		return "session/herdr: herdr agent prompt jig-JIG-1-gate-demo-a1 " + marker + " inputs in " + p + " and " + wslMountSpelling(p)
	}
	cases := []struct {
		name string
		fail func(text string) error
		code string
	}{
		{"an error with no code", func(text string) error { return errors.New(text) }, "INTERNAL"},
		{"an error with a code", func(text string) error { return &axi.Error{Msg: text, Code: "SESSION_TIMEOUT"} }, "SESSION_TIMEOUT"},
		{"a code under a wrapper", func(text string) error {
			return fmt.Errorf("dispatch: %w", &axi.Error{Msg: text, Code: "SCREEN_UNAVAILABLE"})
		}, "SCREEN_UNAVAILABLE"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			dir, _, head, _ := demoLeaseAt(t)
			text := echoed(d.Home)
			stub := &demoStub{t: t, onDemo: func(sd session.Dispatch) error {
				// A backend that failed after it wrote something of its own.
				if err := os.WriteFile(sd.ResultJSON, []byte(`{"outcome":"failed","summary":"`+marker+`"}`), 0o644); err != nil {
					return err
				}
				return c.fail(text)
			}}
			mkRoundDir(t, d.Store, fx.Ticket, 1)
			rep := gateDemo(d, NewReviewerGateSource(stub).(DemoSource), DemoInput{
				Store: d.Store, Ticket: fx.Ticket, Round: 1, LeaseDir: dir, Model: "rung-a", HeadSHA: head,
			}, "fixture-repo", "main")

			wantReason := "the demo session failed: " + c.code
			if rep.Status != DemoRefused || rep.Reason != wantReason || rep.Detail != c.fail(text).Error() {
				t.Fatalf("report = %+v, want a refusal with the reason %q and the whole text as its detail", rep, wantReason)
			}
			rec, ok, err := readDemoRecord(d.Store, fx.Ticket, 1)
			if err != nil || !ok || rec.Status != DemoRefused || rec.Reason != wantReason {
				t.Fatalf("demo.yaml = %+v ok=%v err=%v, want the reason %q", rec, ok, err, wantReason)
			}
			for _, leaked := range []string{marker, d.Home} {
				if got := filesSpelling(t, d.Store.Root, leaked); len(got) != 0 {
					t.Errorf("the store's working tree holds %q in %v", leaked, got)
				}
				if strings.Contains(storeHistory(t, d.Store.Root), leaked) {
					t.Errorf("the store's history holds %q", leaked)
				}
			}
			if _, err := os.Stat(demoResultJSONPath(d.Store, fx.Ticket, 1)); err == nil {
				t.Error("the backend's file was left in the store's work directory")
			}
		})
	}
}

// TestGateDemoSessionThatWroteNoResultIsAFailureWhoseFileIsNotKept: a backend
// writes a result of its own, with the slice result's shape, when a session
// wrote none. The demo session wrote no demo result: the reason is the
// failure's code, the report prints what the backend recorded, and the file,
// whose summary can name the paths a denied tool call had, is not left for the
// store to commit. A result that has the demo's shape, or breaks its contract
// as the session wrote it, is not that: it is the session's own words, and
// stays as written.
func TestGateDemoSessionThatWroteNoResultIsAFailureWhoseFileIsNotKept(t *testing.T) {
	t.Parallel()

	const marker = "DENIED-CALL-OF-THE-SESSION"
	cases := []struct {
		name       string
		result     func(mediaDir string) string
		wantStatus string
		wantReason string
		wantDetail string // contained in the report's detail
		kept       bool   // the result file stays in the store's work directory
	}{
		{
			name: "the backend's fallback",
			result: func(mediaDir string) string {
				summary, _ := json.Marshal("no result block; denied tool calls: Write " + filepath.Join(mediaDir, "shot.png") + " " + marker)
				return `{"outcome":"failed","summary":` + string(summary) + `,"artifacts":["x"]}`
			},
			wantStatus: DemoRefused,
			wantReason: "the demo session failed: DEMO_NO_RESULT",
			wantDetail: "the backend recorded its own outcome in its place (failed: no result block; denied tool calls: Write ",
		},
		{
			name:       "the backend's fallback for an agent it found blocked",
			result:     func(string) string { return `{"outcome":"failed","summary":"agent blocked at dialog"}` },
			wantStatus: DemoRefused,
			wantReason: "the demo session failed: DEMO_NO_RESULT",
			wantDetail: "(failed: agent blocked at dialog)",
		},
		{
			name:       "a demo result that says outcome in its words",
			result:     func(string) string { return `{"media":[],"summary":"the outcome is that nothing shows"}` },
			wantStatus: DemoRecorded,
		},
		{
			name:       "a result that breaks the contract",
			result:     func(string) string { return `{"media":[]}` },
			wantStatus: DemoRefused,
			wantReason: `demo.result.json is invalid: missing "summary"`,
			kept:       true,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			dir, _, head, _ := demoLeaseAt(t)
			stub := &demoStub{t: t, onDemo: func(sd session.Dispatch) error {
				return os.WriteFile(sd.ResultJSON, []byte(c.result(sd.ExtraWriteDir)), 0o644)
			}}
			mkRoundDir(t, d.Store, fx.Ticket, 1)
			rep := gateDemo(d, NewReviewerGateSource(stub).(DemoSource), DemoInput{
				Store: d.Store, Ticket: fx.Ticket, Round: 1, LeaseDir: dir, Model: "rung-a", HeadSHA: head,
			}, "fixture-repo", "main")

			if rep.Status != c.wantStatus || (c.wantReason != "" && rep.Reason != c.wantReason) {
				t.Fatalf("report = %+v, want %s with the reason %q", rep, c.wantStatus, c.wantReason)
			}
			if !strings.Contains(rep.Detail, c.wantDetail) || (c.wantDetail == "" && rep.Detail != "") {
				t.Errorf("detail = %q, want it to hold %q", rep.Detail, c.wantDetail)
			}
			_, statErr := os.Stat(demoResultJSONPath(d.Store, fx.Ticket, 1))
			keptKind := c.kept || c.wantStatus == DemoRecorded
			if keptKind != (statErr == nil) {
				t.Errorf("the result file is kept = %v, want %v", statErr == nil, keptKind)
			}
			if c.wantStatus == DemoRefused && c.wantDetail != "" {
				// What the backend recorded, and the paths in it, is the
				// report's and reaches no file or commit of the store.
				for _, leaked := range []string{marker, d.Home} {
					if got := filesSpelling(t, d.Store.Root, leaked); len(got) != 0 {
						t.Errorf("the store's working tree holds %q in %v", leaked, got)
					}
					if strings.Contains(storeHistory(t, d.Store.Root), leaked) {
						t.Errorf("the store's history holds %q", leaked)
					}
				}
			}
		})
	}
}

func mustList(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := []string{}
	for _, e := range des {
		names = append(names, e.Name())
	}
	return names
}

// --- Gate ----------------------------------------------------------------------------

// gateDemoFixture is a built fixture ticket ready for reviewer rounds.
func gateDemoFixture(t *testing.T) (*fixture.Fixture, Deps) {
	t.Helper()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	return fx, newDeps(t, fx)
}

// advanceBuildHead puts one more commit on the ticket's branch in the build
// lease, so the next gate round reviews a new head.
func advanceBuildHead(t *testing.T, fx *fixture.Fixture, name string) {
	t.Helper()
	dir := buildLeaseDir(t, fx)
	writeReviewFile(t, dir, name, "more\n")
	commitReviewLease(t, dir, "advance: "+name)
}

// TestGateCleanReviewerRoundRecordsADemoPerHead is the whole feature through
// Gate: a clean reviewer round dispatches a demo only after the round's
// store writes are pushed, holds the contract, records demo.yaml in the
// store and the media under the jig home, and runs no second demo for the
// same reviewed head - while a new head gets its own.
func TestGateCleanReviewerRoundRecordsADemoPerHead(t *testing.T) {
	t.Parallel()

	fx, d := gateDemoFixture(t)
	startSHA, err := os.ReadFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "start.fixture-repo.sha"))
	if err != nil {
		t.Fatal(err)
	}
	wantBase := strings.TrimSpace(string(startSHA))

	var captured []map[string]any
	stub := &demoStub{t: t}
	stub.onDemo = func(sd session.Dispatch) error {
		round := len(stub.demos)
		// The round's own writes are already committed and pushed.
		if _, err := gitx.Run(fx.StoreRemote, "show", "HEAD:"+fx.Ticket+"/gate/round-"+fmt.Sprint(sd.Attempt)+"/report.yaml"); err != nil {
			t.Errorf("demo %d dispatched before the round's report.yaml reached the store's remote: %v", round, err)
		}
		captured = append(captured, readJSONMap(t, sd.SliceJSON))
		writeMediaFile(t, sd.ExtraWriteDir, fmt.Sprintf("clip-%d.mp4", round), 20+round)
		writeMediaFile(t, sd.ExtraWriteDir, fmt.Sprintf("shot-%d.png", round), 10+round)
		writeDemoResult(t, sd, fmt.Sprintf("demo %d", round), fmt.Sprintf("clip-%d.mp4", round), fmt.Sprintf("shot-%d.png", round))
		return nil
	}
	src := NewReviewerGateSource(stub)

	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report.Verdict != "clean" || report.Demo == nil || report.Demo.Status != DemoRecorded {
		t.Fatalf("round 1: verdict %q, demo %+v", report.Verdict, report.Demo)
	}
	head1 := report.ReviewedSHA["fixture-repo"]
	if len(stub.demos) != 1 || len(stub.reviews) != 1 {
		t.Fatalf("dispatches: %d reviews, %d demos; want 1 and 1", len(stub.reviews), len(stub.demos))
	}
	if stub.demos[0].Model != stub.reviews[0].Model || stub.demos[0].Model == "" {
		t.Errorf("demo model %q, review model %q; want the round's one model", stub.demos[0].Model, stub.reviews[0].Model)
	}

	// The contract: full-change base (the merge base), the round's head, the
	// round's resolved intent.
	if captured[0]["base_sha"] != wantBase || captured[0]["head_sha"] != head1 {
		t.Errorf("demo.json base/head = %v / %v, want %s / %s", captured[0]["base_sha"], captured[0]["head_sha"], wantBase, head1)
	}
	if in := captured[0]["intent"].(map[string]any); in["source"] != "brief" || in["path"] != absPath(filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md")) {
		t.Errorf("demo.json intent = %v", in)
	}

	// The media: renamed in order, under the jig home, never in the store.
	id, err := d.Store.ID()
	if err != nil {
		t.Fatal(err)
	}
	mediaDir1, err := home.EvidenceDir(fx.Home, id, fx.Ticket, head1)
	if err != nil {
		t.Fatal(err)
	}
	if captured[0]["media_dir"] != absPath(mediaDir1) {
		t.Errorf("media_dir = %v, want %s", captured[0]["media_dir"], absPath(mediaDir1))
	}
	if got := fmt.Sprint(mustList(t, mediaDir1)); got != "[demo-1.mp4 demo-2.png]" {
		t.Fatalf("media dir holds %s, want [demo-1.mp4 demo-2.png]", got)
	}
	for _, m := range report.Demo.Media {
		if _, err := os.Stat(filepath.Join(mediaDir1, m.Name)); err != nil {
			t.Errorf("recorded media %s: %v", m.Name, err)
		}
	}
	err = filepath.WalkDir(fx.StoreDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() && strings.HasPrefix(e.Name(), "demo-") {
			t.Errorf("media file %s is inside the store", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// demo.yaml: in the store, and pushed.
	rec, ok, err := readDemoRecord(d.Store, fx.Ticket, 1)
	if err != nil || !ok || rec.Status != DemoRecorded || rec.HeadSHA != head1 || rec.Summary != "demo 1" || len(rec.Media) != 2 {
		t.Fatalf("round 1 demo.yaml = %+v ok=%v err=%v", rec, ok, err)
	}
	pushed, err := gitx.Run(fx.StoreRemote, "show", "HEAD:"+fx.Ticket+"/gate/round-1/demo.yaml")
	if err != nil {
		t.Fatalf("round 1 demo.yaml did not reach the store's remote: %v", err)
	}
	local, err := os.ReadFile(demoYAMLPath(d.Store, fx.Ticket, 1))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(pushed) != strings.TrimSpace(string(local)) {
		t.Errorf("the pushed demo.yaml differs from the local one:\n%s\n---\n%s", pushed, local)
	}

	// The lease is pristine at the reviewed head afterwards.
	leaseDir := filepath.Join(fx.Home, "pool", "fixture-repo", fx.Ticket+"-gate")
	if got, err := gitx.RevParse(leaseDir, "HEAD"); err != nil || got != head1 {
		t.Errorf("gate lease HEAD = %q, %v; want %q", got, err, head1)
	}
	if status, err := gitx.Run(leaseDir, "status", "--porcelain"); err != nil || status != "" {
		t.Errorf("gate lease is not pristine: %q, %v", status, err)
	}

	// Round 2, same head: nothing to review, and its demo already exists.
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Verdict != "clean" || report2.Demo == nil || report2.Demo.Status != DemoExisting || report2.Demo.Round != 1 {
		t.Fatalf("round 2: verdict %q, demo %+v; want clean with the existing round-1 demo", report2.Verdict, report2.Demo)
	}
	if len(stub.demos) != 1 {
		t.Fatalf("a second clean round on the same head dispatched a demo (%d dispatches)", len(stub.demos))
	}
	if _, ok, err := readDemoRecord(d.Store, fx.Ticket, 2); ok || err != nil {
		t.Errorf("round 2 wrote a demo.yaml: ok=%v err=%v", ok, err)
	}

	// Round 3, a new head: its own demo, in its own directory, with the base
	// still the whole change's (a delta review does not shrink the demo).
	advanceBuildHead(t, fx, "note.txt")
	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	head3 := report3.ReviewedSHA["fixture-repo"]
	if head3 == head1 {
		t.Fatalf("round 3 reviewed the same head %s", head3)
	}
	if report3.Scope != "delta" {
		t.Fatalf("round 3 scope = %q, want delta", report3.Scope)
	}
	if report3.Demo == nil || report3.Demo.Status != DemoRecorded || len(stub.demos) != 2 {
		t.Fatalf("round 3 demo = %+v after %d dispatches, want a second, recorded one", report3.Demo, len(stub.demos))
	}
	if captured[1]["base_sha"] != wantBase || captured[1]["head_sha"] != head3 {
		t.Errorf("round 3 demo.json base/head = %v / %v, want %s / %s", captured[1]["base_sha"], captured[1]["head_sha"], wantBase, head3)
	}
	mediaDir3, err := home.EvidenceDir(fx.Home, id, fx.Ticket, head3)
	if err != nil {
		t.Fatal(err)
	}
	if mediaDir3 == mediaDir1 || fmt.Sprint(mustList(t, mediaDir3)) != "[demo-1.mp4 demo-2.png]" {
		t.Errorf("round 3 media dir %s holds %v", mediaDir3, mustList(t, mediaDir3))
	}
	if got := fmt.Sprint(mustList(t, mediaDir1)); got != "[demo-1.mp4 demo-2.png]" {
		t.Errorf("round 1's media changed to %s", got)
	}
}

// TestGateDemoIsHandedTheIntentTheRoundUsed: the demo is dispatched with the
// very intent pair the round's reviewer was handed and its report shows,
// whichever source resolved it - not a pair Gate resolved before the round
// and could have gone stale, and not one the demo works out for itself.
func TestGateDemoIsHandedTheIntentTheRoundUsed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		brief  bool
		intent string
		source string
	}{
		{"a brief", true, "", IntentSourceBrief},
		{"an explicit intent", false, "make the greeting warmer", IntentSourceExplicit},
		{"nothing states it", false, "", IntentSourceNone},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx, d := gateDemoFixture(t)
			if !c.brief {
				removeBrief(t, d, fx.Ticket)
			}
			var demoIntent map[string]any
			stub := &demoStub{t: t}
			stub.onDemo = func(sd session.Dispatch) error {
				demoIntent = readJSONMap(t, sd.SliceJSON)["intent"].(map[string]any)
				writeDemoResult(t, sd, "nothing to show")
				return nil
			}
			report, err := Gate(d, NewReviewerGateSource(stub), GateOpts{Ticket: fx.Ticket, Intent: c.intent})
			if err != nil {
				t.Fatalf("Gate: %v", err)
			}
			if report.Demo == nil || report.Demo.Status != DemoRecorded || len(stub.reviews) != 1 {
				t.Fatalf("demo = %+v after %d reviews, want a recorded demo after one", report.Demo, len(stub.reviews))
			}
			if report.Intent.Source != c.source {
				t.Fatalf("the round's intent source = %q, want %q", report.Intent.Source, c.source)
			}
			reviewIntent := readJSONMap(t, stub.reviews[0].SliceJSON)["intent"].(map[string]any)
			want := map[string]any{"source": report.Intent.Source, "path": report.Intent.Path}
			if fmt.Sprint(demoIntent) != fmt.Sprint(want) || fmt.Sprint(reviewIntent) != fmt.Sprint(want) {
				t.Errorf("intent: demo.json %v, review.json %v, the round's report %v; want all three the same", demoIntent, reviewIntent, want)
			}
		})
	}
}

// TestGateADemoNeverMakesACleanRoundUncleanAndIsRetried: a demo that fails
// leaves the verdict, the journal and the round's own record exactly as a
// clean round has them, records the refusal, and is tried again by the next
// clean round on the same head.
func TestGateADemoNeverMakesACleanRoundUncleanAndIsRetried(t *testing.T) {
	t.Parallel()

	fx, d := gateDemoFixture(t)
	stub := &demoStub{t: t}
	stub.onDemo = func(sd session.Dispatch) error {
		if len(stub.demos) == 1 {
			return errors.New("the recorder crashed")
		}
		writeDemoResult(t, sd, "the change shows nothing", []string{}...)
		return nil
	}
	src := NewReviewerGateSource(stub)

	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate with a failing demo: %v", err)
	}
	if report.Verdict != "clean" || report.Demo == nil || report.Demo.Status != DemoRefused ||
		report.Demo.Reason != "the demo session failed: INTERNAL" || report.Demo.Detail != "the recorder crashed" {
		t.Fatalf("verdict %q, demo %+v; want clean with a refused demo whose reason is the failure's code and whose detail is its text", report.Verdict, report.Demo)
	}
	rep, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, 1), "report.yaml"))
	if err != nil || !strings.Contains(string(rep), "verdict: clean") {
		t.Fatalf("round 1 report.yaml = %q, %v; want the clean verdict kept", rep, err)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	sawClean := false
	for _, l := range lines {
		if l.Event == "gate-clean" && l.Attempt == 1 {
			sawClean = true
		}
	}
	if !sawClean {
		t.Error("the journal has no gate-clean line for the round whose demo failed")
	}
	rec, ok, err := readDemoRecord(d.Store, fx.Ticket, 1)
	if err != nil || !ok || rec.Status != DemoRefused || rec.Reason != "the demo session failed: INTERNAL" {
		t.Fatalf("round 1 demo.yaml = %+v ok=%v err=%v", rec, ok, err)
	}

	// Same head, next round: the refused demo does not count, so it runs
	// again - and this time records, with no media and a summary saying why.
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Verdict != "clean" || report2.Demo == nil || report2.Demo.Status != DemoRecorded || len(report2.Demo.Media) != 0 || report2.Demo.Summary != "the change shows nothing" {
		t.Fatalf("round 2: verdict %q, demo %+v", report2.Verdict, report2.Demo)
	}
	if len(stub.demos) != 2 {
		t.Fatalf("demo dispatches = %d, want the refused one retried once", len(stub.demos))
	}
	// And a demo with nothing to show is still the head's demo.
	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if report3.Demo == nil || report3.Demo.Status != DemoExisting || report3.Demo.Round != 2 || len(stub.demos) != 2 {
		t.Fatalf("round 3 demo = %+v after %d dispatches, want the round-2 demo found", report3.Demo, len(stub.demos))
	}
}

// TestGateDemoStorePushFailureIsAWarning: the demo is recorded and the
// verdict stands even when the store push that would carry demo.yaml fails.
func TestGateDemoStorePushFailureIsAWarning(t *testing.T) {
	t.Parallel()

	fx, d := gateDemoFixture(t)
	stub := &demoStub{t: t}
	stub.onDemo = func(sd session.Dispatch) error {
		// The round is already pushed; take the remote away before the demo's
		// own push.
		if err := os.RemoveAll(fx.StoreRemote); err != nil {
			t.Skipf("cannot remove the store remote on this platform: %v", err)
		}
		writeDemoResult(t, sd, "nothing to show")
		return nil
	}
	report, err := Gate(d, NewReviewerGateSource(stub), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict != "clean" || report.Demo == nil || report.Demo.Status != DemoRecorded {
		t.Fatalf("verdict %q, demo %+v; want clean with a recorded demo", report.Verdict, report.Demo)
	}
	if !strings.Contains(report.Demo.Warning, "demo.yaml") {
		t.Errorf("warning = %q, want it to say the push carrying demo.yaml failed", report.Demo.Warning)
	}
	if _, ok, err := readDemoRecord(d.Store, fx.Ticket, 1); err != nil || !ok {
		t.Errorf("demo.yaml is not in the store's working copy: ok=%v err=%v", ok, err)
	}
}

// TestGateRunsADemoOnlyAfterACleanReviewerRoundThatWantsOne: a round with
// findings runs none; --no-demo (GateOpts.NoDemo) skips a clean one and
// leaves no trace of one; and the same head's first clean round without the
// flag then runs it, even when the review itself dispatched nothing.
func TestGateRunsADemoOnlyAfterACleanReviewerRoundThatWantsOne(t *testing.T) {
	t.Parallel()

	fx, d := gateDemoFixture(t)
	round := 0
	stub := &demoStub{t: t}
	stub.onDemo = func(sd session.Dispatch) error {
		writeDemoResult(t, sd, "nothing to show")
		return nil
	}
	// The stub's reviewer is clean, so round 1 needs a reviewer of its own
	// that reports one fix.
	src := NewReviewerGateSource(session.Backend(backendFunc(func(sd session.Dispatch) error {
		if sd.Slice == session.GateDemoSlice {
			return stub.Run(sd)
		}
		round++
		if round == 1 {
			reviewData, err := os.ReadFile(sd.SliceJSON)
			if err != nil {
				t.Fatal(err)
			}
			var req ReviewRequest
			if err := json.Unmarshal(reviewData, &req); err != nil {
				t.Fatal(err)
			}
			res := ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix", Detail: "d", Action: ActionFix,
					Risk: RiskLow, RiskRationale: "r", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview, Summary: "one fix",
			}
			return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, res), 0o644)
		}
		return stub.Run(sd)
	})))

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report1.Verdict != "fix-slices" || report1.Demo != nil || len(stub.demos) != 0 {
		t.Fatalf("round 1: verdict %q, demo %+v, %d demo dispatches; want fix-slices and no demo", report1.Verdict, report1.Demo, len(stub.demos))
	}

	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true, NoDemo: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Verdict != "clean" || report2.Demo != nil || len(stub.demos) != 0 {
		t.Fatalf("round 2 (--no-demo): verdict %q, demo %+v, %d demo dispatches; want clean and no demo", report2.Verdict, report2.Demo, len(stub.demos))
	}
	for _, p := range []string{demoResultJSONPath(d.Store, fx.Ticket, 2), demoYAMLPath(d.Store, fx.Ticket, 2), filepath.Join(d.Home, "evidence")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists after a --no-demo round", p)
		}
	}

	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if report3.Verdict != "clean" || report3.Demo == nil || report3.Demo.Status != DemoRecorded || len(stub.demos) != 1 {
		t.Fatalf("round 3: verdict %q, demo %+v, %d demo dispatches; want clean and one recorded demo", report3.Verdict, report3.Demo, len(stub.demos))
	}
}

// backendFunc adapts a function to session.Backend.
type backendFunc func(d session.Dispatch) error

func (f backendFunc) Run(d session.Dispatch) error { return f(d) }

// TestGateScriptedRoundNeverRunsADemo: the scripted source has no Demo
// method, so a clean scripted round dispatches nothing and records nothing.
func TestGateScriptedRoundNeverRunsADemo(t *testing.T) {
	t.Parallel()

	fx, d := gateDemoFixture(t)
	src := NewFakeGateSource(t.TempDir()) // no rounds scripted: clean
	if _, ok := src.(DemoSource); ok {
		t.Fatal("the scripted gate source implements DemoSource")
	}
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict != "clean" || report.Demo != nil {
		t.Fatalf("verdict %q, demo %+v; want a clean scripted round with no demo", report.Verdict, report.Demo)
	}
	for _, p := range []string{demoResultJSONPath(d.Store, fx.Ticket, 1), demoYAMLPath(d.Store, fx.Ticket, 1)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists after a scripted round", p)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.Home, "evidence")); err == nil {
		t.Error("a scripted round created the evidence directory")
	}
}

// scriptedWithDemo is a scripted source that also has a Demo method: a round
// with no reviewer session behind it must still never be asked for a demo.
type scriptedWithDemo struct {
	GateSource
	demos int
}

func (s *scriptedWithDemo) Demo(DemoInput) (DemoResult, error) {
	s.demos++
	return DemoResult{}, nil
}

// TestGateNeverAsksAScriptedRoundForADemo: what decides a demo is the round -
// a reviewer round that came back clean - not only whether the source has a
// Demo method, so a clean scripted round asks for none even from a source that
// could give one.
func TestGateNeverAsksAScriptedRoundForADemo(t *testing.T) {
	t.Parallel()

	fx, d := gateDemoFixture(t)
	src := &scriptedWithDemo{GateSource: NewFakeGateSource(t.TempDir())}
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict != "clean" || report.Demo != nil || src.demos != 0 {
		t.Fatalf("verdict %q, demo %+v, %d demo requests; want a clean round that asked for none", report.Verdict, report.Demo, src.demos)
	}
}

// TestGateDemoThatCannotBeRecordedIsRefusedInTheReport: when demo.yaml has
// nowhere to go the demo is reported refused, with the reason, and nothing
// is raised.
func TestGateDemoThatCannotBeRecordedIsRefusedInTheReport(t *testing.T) {
	t.Parallel()

	dir, _, head, _ := demoLeaseAt(t)
	d := Deps{Store: newReviewStore(t), Home: t.TempDir()}
	stub := &demoStub{t: t, onDemo: func(sd session.Dispatch) error {
		writeDemoResult(t, sd, "nothing to show")
		return nil
	}}
	// No gate/round-1 directory exists, so writing demo.yaml fails.
	rep := gateDemo(d, NewReviewerGateSource(stub).(DemoSource), DemoInput{
		Store: d.Store, Ticket: "JIG-1", Round: 1, LeaseDir: dir, HeadSHA: head,
	}, "fixture-repo", "main")
	if rep.Status != DemoRefused || !strings.Contains(rep.Reason, "demo.yaml could not be recorded") {
		t.Fatalf("report = %+v, want a refusal saying demo.yaml could not be recorded", rep)
	}
	// The report is printed, so this reason, which the failed write left
	// unrecorded, names no directory of this machine either.
	for _, host := range []string{d.Home, d.Store.Root} {
		for _, sp := range spellings(host) {
			if strings.Contains(rep.Reason, sp) {
				t.Errorf("the reason %q names the host path %s", rep.Reason, sp)
			}
		}
	}
}

// TestGateDemoStopsAtAnUnreadableEarlierRecord: a demo.yaml an earlier round
// left that cannot be read is a refusal recorded for this round, and no
// session is dispatched on top of it.
func TestGateDemoStopsAtAnUnreadableEarlierRecord(t *testing.T) {
	t.Parallel()

	dir, _, head, _ := demoLeaseAt(t)
	st := newReviewStore(t)
	d := Deps{Store: st, Home: t.TempDir()}
	mkRoundDir(t, st, "JIG-1", 1)
	mkRoundDir(t, st, "JIG-1", 2)
	if err := os.WriteFile(demoYAMLPath(st, "JIG-1", 1), []byte("status: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := &demoStub{t: t, onDemo: func(session.Dispatch) error {
		t.Error("a demo was dispatched over an unreadable earlier record")
		return nil
	}}
	rep := gateDemo(d, NewReviewerGateSource(stub).(DemoSource), DemoInput{
		Store: st, Ticket: "JIG-1", Round: 2, LeaseDir: dir, HeadSHA: head,
	}, "fixture-repo", "main")
	if rep.Status != DemoRefused || !strings.Contains(rep.Reason, "gate/round-1/demo.yaml cannot be read") {
		t.Fatalf("report = %+v, want a refusal naming the unreadable record", rep)
	}
	if rec, ok, err := readDemoRecord(st, "JIG-1", 2); err != nil || !ok || rec.Status != DemoRefused {
		t.Errorf("round 2 demo.yaml = %+v ok=%v err=%v, want the refusal recorded", rec, ok, err)
	}
}
