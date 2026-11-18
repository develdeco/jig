package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/outcome"
	"gopkg.in/yaml.v3"
)

// applyScenarioPatch applies slice's attempt-1 patch from the fixture scenario
// to the clone and commits it with the slice's trailer.
func (r *verifyRig) applyScenarioPatch(slice string) string {
	r.t.Helper()
	patch := filepath.Join(r.fx.ScenarioDir, "slices", slice, "attempt-1", "patch.diff")
	gitMust(r.t, r.fx.RepoDir, "apply", patch)
	gitBuilder(r.t).Commit(r.fx.RepoDir, slice+": scenario patch"+"\n\n"+"Jig-Slice: "+slice)
	return gitMust(r.t, r.fx.RepoDir, "rev-parse", "HEAD")
}

// setOracle makes slice's oracle the literal command cmd, in the store's
// slices.yaml.
func (r *verifyRig) setOracle(slice, cmd string) {
	r.t.Helper()
	path := filepath.Join(r.st.TicketDir(r.fx.Ticket), "slices.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	body := string(data)
	quoted, err := yaml.Marshal(cmd)
	if err != nil {
		r.t.Fatal(err)
	}
	i := strings.Index(body, "  - id: "+slice+"\n")
	j := strings.Index(body[max(i, 0):], "    oracle: test\n")
	if i < 0 || j < 0 {
		r.t.Fatalf("test setup: %s has no slice %s naming the test oracle:\n%s", path, slice, body)
	}
	at := i + j
	body = body[:at] + "    oracle: " + strings.TrimSpace(string(quoted)) + "\n" + body[at+len("    oracle: test\n"):]
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// TestVerifyRunsInTheForegroundAndExitsWithTheCommandsCode: `jig verify` runs
// the real oracle in this process and in this worktree, the command's output
// reaches the stream as it is produced, and the exit code is the command's
// own: 0 for a pass, the code a failing command ends with, whatever it is.
func TestVerifyRunsInTheForegroundAndExitsWithTheCommandsCode(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()

	out, code := r.run("verify")
	if code != 0 {
		t.Fatalf("verify exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"state: pass", "slice: a", "commit: " + sha, "attempt: 1", "seconds: ", "covers: yes", "ok  \t"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	var got []string
	for _, l := range r.journal() {
		got = append(got, l.Event+":"+l.Outcome)
	}
	if want := "oracle:pass verified:"; strings.Join(got, " ") != want {
		t.Errorf("journal events = %v, want %s: nothing is pending while a command runs", got, want)
	}
	if ss, err := r.st.ReadSliceState(r.fx.Ticket, "a"); err != nil || ss.State != "green" {
		t.Errorf("slice a state = %+v, %v, want green", ss, err)
	}
	if n := len(r.spawn.Specs()); n != 0 {
		t.Errorf("verify started %d processes, want none", n)
	}

	// A command that fails with its own code: that code is the exit code, the
	// output reached the stream, and the slice builds on.
	r.setOracle("b", "echo the command printed this; exit 7")
	r.commit("b", "b's work", map[string]string{"b.txt": "1"})
	out, code = r.run("verify", "--slice", "b")
	if code != 7 {
		t.Fatalf("verify of a command that exits 7: exit = %d, want 7\n%s", code, out)
	}
	if !strings.Contains(out, "the command printed this") || !strings.Contains(out, "state: fail") {
		t.Errorf("output lacks the command's output or the failure:\n%s", out)
	}
	if ss, err := r.st.ReadSliceState(r.fx.Ticket, "b"); err != nil || ss.State != "building" {
		t.Errorf("slice b state = %+v, %v, want building", ss, err)
	}
}

// TestVerifyFailureRecordsItsSummaryAndSignature: a failing oracle exits 1,
// the report carries the summary and signature the journal line carries, and
// the slice is building.
func TestVerifyFailureRecordsItsSummaryAndSignature(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	// Slice b's patch, without a's fix under it: its test fails.
	r.applyScenarioPatch("b")

	out, code := r.run("verify")
	if code != 1 {
		t.Fatalf("verify exit = %d, want 1 for a failing oracle\n%s", code, out)
	}
	if !strings.Contains(out, "state: fail") || !strings.Contains(out, "summary: ") || !strings.Contains(out, "signature: ") || !strings.Contains(out, "FAIL") {
		t.Errorf("output lacks the failure's state, summary and signature, or the output of the test:\n%s", out)
	}
	var oracle journal.Line
	for _, l := range r.journal() {
		if l.Event == journal.EventOracle {
			oracle = l
		}
	}
	if oracle.Outcome != "fail" || oracle.Summary == "" || oracle.Signature != outcome.Signature("b", outcome.CodeBug, oracle.Summary) || oracle.Attempt != 1 {
		t.Errorf("oracle line = %+v, want a failure with its summary and signature on attempt 1", oracle)
	}
	if ss, err := r.st.ReadSliceState(r.fx.Ticket, "b"); err != nil || ss.State != "building" {
		t.Errorf("slice b state = %+v, %v, want building", ss, err)
	}
}

// TestVerifyRunsAgainEveryTime: a second run of the same commit runs the
// oracle again, as the slice's next attempt; there is no existing answer.
func TestVerifyRunsAgainEveryTime(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.fixClamp()
	for attempt, want := range []string{"attempt: 1", "attempt: 2"} {
		out, code := r.run("verify")
		if code != 0 || !strings.Contains(out, want) || strings.Contains(out, "existing") {
			t.Fatalf("run %d: exit %d, want a pass with %q and no existing answer\n%s", attempt+1, code, want, out)
		}
	}
}
