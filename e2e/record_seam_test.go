package e2e

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/termrec"
)

// fakeT stands in for the *testing.T a recording is made for: it names the
// test and keeps what the recording logged.
type fakeT struct {
	name string
	mu   sync.Mutex
	logs []string
}

func (f *fakeT) Helper()      {}
func (f *fakeT) Name() string { return f.name }
func (f *fakeT) Logf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

// svgRows checks that data is a well-formed XML document with an svg root and
// returns the text of each row the animation draws.
func svgRows(t *testing.T, data []byte) []string {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(data))
	var rows []string
	var cur *strings.Builder
	root := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("the recording is not well-formed XML: %v", err)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if root == "" {
				root = tok.Name.Local
			}
			if tok.Name.Local == "text" {
				cur = &strings.Builder{}
			}
		case xml.CharData:
			if cur != nil {
				cur.Write(tok)
			}
		case xml.EndElement:
			if tok.Name.Local == "text" && cur != nil {
				rows = append(rows, cur.String())
				cur = nil
			}
		}
	}
	if root != "svg" {
		t.Fatalf("the recording's root element is %q, want svg", root)
	}
	return rows
}

// readTagFile reads the tag at path as jig does: strictly, one JSON object of
// the four fields.
func readTagFile(t *testing.T, path string) recordingTag {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the tag: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var tag recordingTag
	if err := dec.Decode(&tag); err != nil {
		t.Fatalf("tag %s: %v\n%s", filepath.Base(path), err, data)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("tag %s holds more than one JSON value", filepath.Base(path))
	}
	return tag
}

// recordedFiles is the names in dir, in order.
func recordedFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the record directory: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func writeAll(w io.Writer, s string) {
	if _, err := io.WriteString(w, s); err != nil {
		panic(err)
	}
}

// TestRecordRunRecordsTheCall pins the recording of one jig run at its seam:
// the SVG shows the command and its output (stdout and stderr) and the exit
// code, the temp paths of the run are left out, the caller's buffers get
// exactly what the process wrote, and the tag names the scenario, flow, step
// and caption.
func TestRecordRunRecordsTheCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := jigEnv{home: t.TempDir(), userHome: t.TempDir()}
	scenario := filepath.Join(t.TempDir(), "scenario")
	cwd := filepath.Join(t.TempDir(), "store")
	other := filepath.Join(os.TempDir(), "elsewhere")
	args := []string{"run", "T-1", "--title", "Add retry", "--backend", "fake", "--scenario", scenario}
	ft := &fakeT{name: "TestScenario/sub case"}
	rs := newRecordings(dir)

	var stdout, stderr bytes.Buffer
	wantOut := fmt.Sprintf("jig home %s\nscenario %s\nstore %s\nslash %s\njson %s\nelsewhere %s\ndone\n",
		env.home, scenario, cwd, filepath.ToSlash(env.home), strings.ReplaceAll(env.home, `\`, `\\`), other)
	code := rs.run(ft, recordCall{env: env, cwd: cwd, args: args}, &stdout, &stderr, func(out, errs io.Writer) int {
		writeAll(out, wantOut)
		writeAll(errs, "warning: slow\n")
		return 2
	})

	if code != 2 {
		t.Errorf("exit code = %d, want the process's 2", code)
	}
	if stdout.String() != wantOut || stderr.String() != "warning: slow\n" {
		t.Errorf("the caller's buffers = %q and %q, want the process's output unchanged", stdout.String(), stderr.String())
	}
	if len(ft.logs) != 0 {
		t.Errorf("a recording that worked logged %q", ft.logs)
	}
	wantFiles := []string{"TestScenario-sub-case-01.svg", "TestScenario-sub-case-01.svg.json"}
	if got := recordedFiles(t, dir); !slices.Equal(got, wantFiles) {
		t.Fatalf("record directory = %q, want %q", got, wantFiles)
	}

	svg, err := os.ReadFile(filepath.Join(dir, wantFiles[0]))
	if err != nil {
		t.Fatal(err)
	}
	rows := svgRows(t, svg)
	for _, want := range []string{
		"$ jig run T-1 --title 'Add retry' --backend fake --scenario <scenario>",
		"jig home <jig-home>",
		"scenario <scenario>",
		"store <store>",
		"slash <jig-home>",
		"json <jig-home>",
		"elsewhere <tmp>" + string(filepath.Separator) + "elsewhere",
		"done",
		"warning: slow",
		"[exit 2]",
	} {
		if !slices.Contains(rows, want) {
			t.Errorf("the recording shows no row %q; its rows:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	for _, leaked := range []string{env.home, env.userHome, scenario, cwd, os.TempDir(), filepath.ToSlash(env.home)} {
		if strings.Contains(string(svg), leaked) {
			t.Errorf("the recording carries the host path %q", leaked)
		}
	}

	tag := readTagFile(t, filepath.Join(dir, wantFiles[1]))
	want := recordingTag{
		Scenario: "TestScenario/sub case",
		Flow:     "TestScenario/sub case",
		Step:     1,
		Caption:  "jig run T-1 --title 'Add retry' --backend fake",
	}
	if tag != want {
		t.Errorf("tag = %+v, want %+v", tag, want)
	}
}

// TestRecordRunNumbersStepsPerTest: a test's runs are its steps, 1 on, in
// order, and each test counts its own; a run that exits zero shows no exit.
func TestRecordRunNumbersStepsPerTest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := jigEnv{home: t.TempDir()}
	rs := newRecordings(dir)
	a := &fakeT{name: "TestA"}
	b := &fakeT{name: "TestB/sub"}
	for _, step := range []struct {
		ft   *fakeT
		args []string
	}{
		{a, []string{"status", "T-1"}},
		{b, []string{"validate", "T-1"}},
		{a, []string{"run", "T-1"}},
	} {
		rs.run(step.ft, recordCall{env: env, cwd: env.home, args: step.args}, io.Discard, io.Discard, func(out, errs io.Writer) int {
			writeAll(out, "ok\n")
			return 0
		})
	}

	want := []string{"TestA-01.svg", "TestA-01.svg.json", "TestA-02.svg", "TestA-02.svg.json", "TestB-sub-01.svg", "TestB-sub-01.svg.json"}
	if got := recordedFiles(t, dir); !slices.Equal(got, want) {
		t.Fatalf("record directory = %q, want %q", got, want)
	}
	for file, caption := range map[string]string{
		"TestA-01.svg.json":     "jig status T-1",
		"TestA-02.svg.json":     "jig run T-1",
		"TestB-sub-01.svg.json": "jig validate T-1",
	} {
		tag := readTagFile(t, filepath.Join(dir, file))
		if tag.Caption != caption || tag.Step < 1 || tag.Flow != tag.Scenario {
			t.Errorf("%s = %+v, want caption %q and a step from 1", file, tag, caption)
		}
	}
	svg, err := os.ReadFile(filepath.Join(dir, "TestA-02.svg"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range svgRows(t, svg) {
		if strings.Contains(row, "[exit") {
			t.Errorf("a run that exited zero shows %q", row)
		}
	}
}

// TestRecordRunWithoutADirectoryRecordsNothing: with no record directory the
// process gets the caller's own writers, so there is no recorder and no tee,
// and nothing is counted.
func TestRecordRunWithoutADirectoryRecordsNothing(t *testing.T) {
	t.Parallel()
	rs := newRecordings("")
	ft := &fakeT{name: "TestNothing"}
	var stdout, stderr bytes.Buffer
	var gotOut, gotErr io.Writer
	code := rs.run(ft, recordCall{env: jigEnv{home: t.TempDir()}, cwd: t.TempDir(), args: []string{"status"}}, &stdout, &stderr, func(out, errs io.Writer) int {
		gotOut, gotErr = out, errs
		writeAll(out, "out\n")
		writeAll(errs, "err\n")
		return 3
	})
	if code != 3 || stdout.String() != "out\n" || stderr.String() != "err\n" {
		t.Errorf("run = %d, %q, %q, want 3 and the process's output", code, stdout.String(), stderr.String())
	}
	if gotOut != io.Writer(&stdout) || gotErr != io.Writer(&stderr) {
		t.Errorf("the process got writers other than the caller's buffers")
	}
	if rs.written != 0 || len(rs.steps) != 0 || len(ft.logs) != 0 {
		t.Errorf("a sink with no directory kept state: written %d, steps %v, logs %q", rs.written, rs.steps, ft.logs)
	}
}

// TestRecordRunStopsAtTheCap: the directory holds at most 50 recordings,
// which jig accepts, and the run that finds it full says so once; every run
// still runs and still reports to its caller.
func TestRecordRunStopsAtTheCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := jigEnv{home: t.TempDir()}
	rs := newRecordings(dir)
	ft := &fakeT{name: "TestMany"}
	for i := 0; i < maxRecordings+4; i++ {
		var stdout bytes.Buffer
		code := rs.run(ft, recordCall{env: env, cwd: env.home, args: []string{"status", fmt.Sprint(i)}}, &stdout, io.Discard, func(out, errs io.Writer) int {
			writeAll(out, "ok\n")
			return 0
		})
		if code != 0 || stdout.String() != "ok\n" {
			t.Fatalf("run %d = %d, %q, want the process's own result past the cap too", i, code, stdout.String())
		}
	}

	var svgs int
	for _, name := range recordedFiles(t, dir) {
		if strings.HasSuffix(name, ".json") {
			tag := readTagFile(t, filepath.Join(dir, name))
			if tag.Step < 0 || len(tag.Scenario) > 200 || len(tag.Flow) > 200 || len(tag.Caption) > 1000 {
				t.Errorf("tag %s = %+v is outside what jig accepts", name, tag)
			}
			continue
		}
		if strings.ContainsAny(name, `/\:`) || !strings.HasSuffix(name, ".svg") {
			t.Errorf("recording %q is not a plain svg file name", name)
		}
		svgs++
	}
	if svgs != maxRecordings {
		t.Errorf("the directory holds %d recordings, want exactly %d", svgs, maxRecordings)
	}
	if len(ft.logs) != 1 || !strings.Contains(ft.logs[0], "50") {
		t.Errorf("logs = %q, want the one line saying the directory is full", ft.logs)
	}
}

// TestRecordRunNeverFailsARun: a recording that cannot be written is logged
// and dropped, the run's result is untouched, and the place it held is given
// back.
func TestRecordRunNeverFailsARun(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing")
	rs := newRecordings(missing)
	ft := &fakeT{name: "TestUnwritable"}
	var stdout bytes.Buffer
	code := rs.run(ft, recordCall{env: jigEnv{home: t.TempDir()}, cwd: t.TempDir(), args: []string{"status"}}, &stdout, io.Discard, func(out, errs io.Writer) int {
		writeAll(out, "ok\n")
		return 1
	})
	if code != 1 || stdout.String() != "ok\n" {
		t.Errorf("run = %d, %q, want the process's own result", code, stdout.String())
	}
	if len(ft.logs) != 1 {
		t.Errorf("logs = %q, want the one line saying why the recording was dropped", ft.logs)
	}
	if rs.written != 0 {
		t.Errorf("a dropped recording still holds %d places", rs.written)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("recording made %s: %v", missing, err)
	}
}

// TestRecordRunWritesNothingForARunThatEndedTheTest: a process that ends the
// test (runJig's Fatal when jig cannot start) leaves no recording and gives
// its place back.
func TestRecordRunWritesNothingForARunThatEndedTheTest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rs := newRecordings(dir)
	done := make(chan struct{})
	go func() {
		defer close(done)
		rs.run(&fakeT{name: "TestFatal"}, recordCall{env: jigEnv{home: t.TempDir()}, cwd: t.TempDir()}, io.Discard, io.Discard, func(out, errs io.Writer) int {
			runtime.Goexit() // what t.Fatal does
			return 0
		})
	}()
	<-done
	if got := recordedFiles(t, dir); len(got) != 0 {
		t.Errorf("record directory = %q, want it empty", got)
	}
	if rs.written != 0 {
		t.Errorf("the run that ended the test still holds %d places", rs.written)
	}
}

func TestRecordingName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		test string
		step int
		want string
	}{
		{"TestEndToEndTwice/iterations/iteration_1", 3, "TestEndToEndTwice-iterations-iteration_1-03.svg"},
		{"TestInit/a b//c#01", 12, "TestInit-a-b-c-01-12.svg"},
		{"Testé世", 1, "Test-01.svg"},
		{"///", 1, "test-01.svg"},
	} {
		if got := recordingName(c.test, c.step); got != c.want {
			t.Errorf("recordingName(%q, %d) = %q, want %q", c.test, c.step, got, c.want)
		}
	}

	// A long name is cut, and what is cut still tells two tests apart.
	long := strings.Repeat("a", 300)
	a, b := recordingName(long+"1", 1), recordingName(long+"2", 1)
	if a == b {
		t.Errorf("two long names share the file name %q", a)
	}
	for _, name := range []string{a, b} {
		stem, ok := strings.CutSuffix(name, ".svg")
		if len(name) > maxNameBytes+len("-")+8+len("-01.svg") || !ok || strings.Trim(stem, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			t.Errorf("long name became %q, want a short plain file name", name)
		}
	}
}

func TestRecordingCaptionAndTagBounds(t *testing.T) {
	t.Parallel()
	abs := filepath.Join(t.TempDir(), "x")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "T-1"}, "jig status T-1"},
		{[]string{"run", "T-1", "--backend", "fake", "--scenario", abs}, "jig run T-1 --backend fake"},
		{[]string{"init", "--store", abs, "--clone", "fixture-repo=" + abs}, "jig init"},
		{[]string{"ticket", "new", "--title", "Fix the thing"}, "jig ticket new --title 'Fix the thing'"},
	} {
		if got := caption(c.args); got != c.want {
			t.Errorf("caption(%q) = %q, want %q", c.args, got, c.want)
		}
	}

	// A test name over the bound is cut at a whole rune.
	dir := t.TempDir()
	env := jigEnv{home: t.TempDir()}
	ft := &fakeT{name: "Test" + strings.Repeat("é", 150)}
	newRecordings(dir).run(ft, recordCall{env: env, cwd: env.home, args: []string{"status"}}, io.Discard, io.Discard, func(out, errs io.Writer) int { return 0 })
	var tagFile string
	for _, name := range recordedFiles(t, dir) {
		if strings.HasSuffix(name, ".json") {
			tagFile = name
		}
	}
	if tagFile == "" {
		t.Fatalf("no tag written: %q", recordedFiles(t, dir))
	}
	tag := readTagFile(t, filepath.Join(dir, tagFile))
	if len(tag.Scenario) > maxTagText || len(tag.Flow) > maxTagText || !utf8.ValidString(tag.Scenario) || !strings.HasSuffix(tag.Scenario, "...") {
		t.Errorf("scenario %q (%d bytes) is not cut to %d bytes at a whole rune", tag.Scenario, len(tag.Scenario), maxTagText)
	}
}

// TestFitHeight: a recording is cut to the rows its output needs and still
// ends on the same screen, blank lines at its end included; an output taller
// than the terminal keeps the whole terminal.
func TestFitHeight(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		output string
		want   int
	}{
		{"short output takes the minimum", "a\nb\n", recordMinHeight},
		{"output and the cursor's row", "1\n2\n3\n4\n5\n6\n", 7},
		{"blank lines at the end are rows too", "1\n2\n3\n4\n5\n\n\n", 8},
		{"taller than the terminal", strings.Repeat("x\n", recordMaxHeight+50), recordMaxHeight},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec, err := termrec.NewRecorder(recordWidth, recordMaxHeight)
			if err != nil {
				t.Fatal(err)
			}
			writeAll(rec.Stream(), c.output)
			full := rec.Cast()
			fitted := fitHeight(full)
			if fitted.Height != c.want {
				t.Errorf("height = %d, want %d", fitted.Height, c.want)
			}
			wantText, err := full.FinalText()
			if err != nil {
				t.Fatal(err)
			}
			if gotText, err := fitted.FinalText(); err != nil || gotText != wantText {
				t.Errorf("the fitted recording ends on %q (%v), want %q", gotText, err, wantText)
			}
		})
	}
}
