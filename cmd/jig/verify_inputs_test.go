package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestVerifyHeaderAndFooterNameWhatRanAndWhatWasRecorded: the header, before
// the command's output, names the command, the directory, the commit, the
// slice and where it came from, the tree and every flag with its default; the
// footer, after it, names the outcome, the seconds, whether HEAD is covered
// and what was recorded where. A run whose command never started has no
// footer.
func TestVerifyHeaderAndFooterNameWhatRanAndWhatWasRecorded(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()

	out, code := r.run("verify")
	if code != 0 {
		t.Fatalf("verify exit = %d\n%s", code, out)
	}
	header, rest, ok := strings.Cut(out, "result:")
	if !ok {
		t.Fatalf("output has no footer:\n%s", out)
	}
	for _, want := range []string{
		"verify:", "command:", "(the slice's oracle)", "dir: ", "commit: " + sha, "slice: a (the Jig-Slice trailer of HEAD)", "tree: clean",
		"flags:", "timeout: none (default)", "dry-run: off (default)", "no-record: off (default)", "json: off (default)", "slice: a (default)",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("the header lacks %q:\n%s", want, header)
		}
	}
	// The header names the worktree root as git reports it, which on Windows
	// can be the long spelling of a short temp path: the same directory, not
	// the same text.
	var dir string
	for _, line := range strings.Split(header, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "dir: "); ok {
			dir = strings.Trim(v, `"`)
			break
		}
	}
	got, err1 := os.Stat(dir)
	want, err2 := os.Stat(r.fx.RepoDir)
	if err1 != nil || err2 != nil || !os.SameFile(got, want) {
		t.Errorf("the header's dir %q is not the worktree %q (%v, %v)", dir, r.fx.RepoDir, err1, err2)
	}
	for _, want := range []string{"outcome: pass", "exit: 0", "seconds: ", "covers: yes", "head: " + sha[:12], "recorded: oracle verified", "in: the journal of JIG-1", "pushed: yes"} {
		if !strings.Contains(rest, want) {
			t.Errorf("the footer lacks %q:\n%s", want, rest)
		}
	}
	if strings.Index(out, "verify:") > strings.Index(out, "result:") {
		t.Errorf("the header does not come first:\n%s", out)
	}

	if !strings.Contains(header, "JIG_RECORD_DIR") {
		t.Errorf("the header does not say JIG_RECORD_DIR was added:\n%s", header)
	}

	// A flag that was given is not marked as the default.
	out, _ = r.run("verify", "--slice", "a", "--timeout", "90s", "--no-record")
	for _, want := range []string{"slice: a (--slice)", "timeout: 1m30s", "no-record: on", "recorded: nothing (--no-record)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "JIG_RECORD_DIR") {
		t.Errorf("a --no-record run's header names JIG_RECORD_DIR, which it does not add:\n%s", out)
	}

	// A run that cannot start its command has no footer.
	r.setOracle("b", "")
	r.commit("b", "b's work", map[string]string{"b.txt": "1"})
	out, code = r.run("verify", "--slice", "b")
	if code != 125 || strings.Contains(out, "result:") {
		t.Errorf("a run with no command: exit %d, want 125 and no footer:\n%s", code, out)
	}
}

// TestVerifyDryRunPrintsAndRunsNothing: --dry-run prints the header and the
// record that would be written, runs nothing and writes nothing.
func TestVerifyDryRunPrintsAndRunsNothing(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()

	out, code := r.run("verify", "--dry-run")
	if code != 0 {
		t.Fatalf("verify --dry-run exit = %d\n%s", code, out)
	}
	for _, want := range []string{"verify:", "commit: " + sha, "dry-run: on", "would_record:", "event: oracle", "attempt: 1", "tree_before: clean", "covers: yes", "would_change:", "Nothing ran"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "result:") {
		t.Errorf("a dry run printed a footer:\n%s", out)
	}
	if !strings.Contains(out, "JIG_RECORD_DIR") {
		t.Errorf("the header does not name the variable it adds:\n%s", out)
	}
	// Another command is foretold as covering nothing, as the run will be.
	other, code := r.run("verify", "--dry-run", "--", "echo", "x")
	if code != 0 || !strings.Contains(other, "covers: no") || !strings.Contains(other, "uncovered: another command") {
		t.Errorf("--dry-run -- echo x: exit %d, want covers: no, another command\n%s", code, other)
	}
	if lines := r.journal(); len(lines) != 0 {
		t.Errorf("a dry run wrote %+v", lines)
	}
	if ss, err := r.st.ReadSliceState(r.fx.Ticket, "a"); err != nil || ss.State == "green" {
		t.Errorf("slice a state = %+v, %v, want it untouched", ss, err)
	}
}

// TestVerifyNoRecordWritesNothing: --no-record runs the command and prints the
// result, and writes no line, no state and no push.
func TestVerifyNoRecordWritesNothing(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.fixClamp()
	before, err := r.st.ReadSliceState(r.fx.Ticket, "a")
	if err != nil {
		t.Fatal(err)
	}

	out, code := r.run("verify", "--no-record")
	if code != 0 || !strings.Contains(out, "outcome: pass") || !strings.Contains(out, "recorded: nothing (--no-record)") {
		t.Fatalf("verify --no-record: exit %d, want a pass that records nothing\n%s", code, out)
	}
	if lines := r.journal(); len(lines) != 0 {
		t.Errorf("--no-record wrote %+v", lines)
	}
	if after, err := r.st.ReadSliceState(r.fx.Ticket, "a"); err != nil || after != before {
		t.Errorf("slice a state = %+v, %v, want %+v", after, err, before)
	}
}

// TestVerifyJSONPrintsOneObject: --json prints the closing report as one JSON
// object on stdout and the command's own output, and the header, on stderr.
func TestVerifyJSONPrintsOneObject(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()
	r.setOracle("b", "echo the command printed this; exit 3")
	var stderr bytes.Buffer
	r.e.stderr = &stderr

	out, code := r.run("verify", "--json")
	if code != 0 {
		t.Fatalf("verify --json exit = %d\n%s", code, out)
	}
	var got verifyJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	if got.Outcome != "pass" || got.Exit != 0 || !got.Covered || got.Commit != sha || got.Slice != "a" || got.TreeBefore != "clean" ||
		len(got.Recorded) != 2 || got.Recorded[0] != "oracle" || got.Recorded[1] != "verified" {
		t.Errorf("report = %+v, want a covering pass of slice a at %s recording oracle and verified", got, sha)
	}
	if !strings.Contains(stderr.String(), "verify:") || strings.Contains(out, "verify:") {
		t.Errorf("the header is not on stderr alone:\nstdout:\n%s\nstderr:\n%s", out, stderr.String())
	}

	// A command that fails: its output is on stderr, its code is the exit.
	r.commit("b", "b's work", map[string]string{"b.txt": "1"})
	stderr.Reset()
	out, code = r.run("verify", "--json", "--slice", "b")
	if code != 3 || !strings.Contains(stderr.String(), "the command printed this") || strings.Contains(out, "\nthe command printed this") {
		t.Errorf("a failing command: exit %d, want 3 with its output on stderr\nstdout:\n%s\nstderr:\n%s", code, out, stderr.String())
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Outcome != "fail" || got.Exit != 3 {
		t.Errorf("report = %+v, %v, want a failure with exit 3", got, err)
	}
}

// TestVerifyOtherCommandIsRecordedAndGreensNothing: a command given after `--`
// runs in the place of the slice's oracle, is recorded as another command, and
// covers nothing: the slice is not marked green and no verified line is
// written, whatever the command did.
func TestVerifyOtherCommandIsRecordedAndGreensNothing(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()
	before, err := r.st.ReadSliceState(r.fx.Ticket, "a")
	if err != nil {
		t.Fatal(err)
	}

	out, code := r.run("verify", "--", "echo", "it ran", "&&", "exit", "0")
	if code != 0 || !strings.Contains(out, "it ran") {
		t.Fatalf("verify -- cmd: exit %d, want the command's output and its code\n%s", code, out)
	}
	if !strings.Contains(out, "covers: no") || !strings.Contains(out, "uncovered: another command") || !strings.Contains(out, "given after --") {
		t.Errorf("output does not say the command is another one that covers nothing:\n%s", out)
	}
	lines := r.journal()
	if len(lines) != 1 || lines[0].Event != journal.EventOracle || lines[0].Uncovered != journal.UncoveredOtherCmd || lines[0].Commit != sha {
		t.Fatalf("journal = %+v, want one oracle line that covers nothing: another command", lines)
	}
	if !strings.Contains(lines[0].Command, "echo") || !strings.Contains(lines[0].Flags, "--") || lines[0].Dir == "" {
		t.Errorf("oracle line = %+v, want the command and the flags as given", lines[0])
	}
	if after, err := r.st.ReadSliceState(r.fx.Ticket, "a"); err != nil || after != before {
		t.Errorf("slice a state = %+v, %v, want %+v", after, err, before)
	}
}

// TestVerifyTimeoutBoundsTheRun: no bound is put on the command unless --timeout
// says; with it, a command that outlives it is ended and its exit is the
// timeout's.
func TestVerifyTimeoutBoundsTheRun(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.fixClamp()
	r.setOracle("b", "sleep 30")
	r.commit("b", "b's work", map[string]string{"b.txt": "1"})

	out, code := r.run("verify", "--slice", "b", "--timeout", "1s")
	if code != 124 || !strings.Contains(out, "outcome: fail") || !strings.Contains(out, "did not finish within 1s") {
		t.Fatalf("verify --timeout 1s of a command that sleeps: exit %d, want 124 and a timeout\n%s", code, out)
	}
	lines := r.journal()
	if len(lines) == 0 || lines[len(lines)-1].Cause != journal.CauseTimeout {
		t.Errorf("journal = %+v, want an oracle line whose cause is the timeout", lines)
	}
}
