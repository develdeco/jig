package verify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// killedRun runs a passing oracle, then a failing one that printed out and
// was killed by the wedge guard, whose error embeds a command of cmdLen bytes.
func killedRun(t *testing.T, r *rig, pass, out string, cmdLen int) Result {
	t.Helper()
	r.oracle.out = pass
	sha := r.commit("b", "b: one", map[string]string{"x.txt": "1"})
	if res, err := Run(r.deps, r.request("b", sha)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("passing Run = %+v, %v", res, err)
	}
	r.oracle.out = out
	r.oracle.err = timedOut(strings.Repeat("go test -run X ./... && ", cmdLen/24))
	sha = r.commit("b", "b: two", map[string]string{"x.txt": "2"})
	res, err := Run(r.deps, r.request("b", sha))
	if err != nil || res.State != journal.VerifyFail {
		t.Fatalf("killed Run = %+v, %v", res, err)
	}
	return res
}

// TestKilledRunWithALongCommandKeepsItsBounds is the repro: a run the wedge
// guard killed, its error embedding a long oracle command and many new lines
// after a baseline, has a one-line summary within its bound that names a
// failing line, and a signature that keeps the timeout.
func TestKilledRunWithALongCommandKeepsItsBounds(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	newOut := okLines(3, "0.5") + strings.Repeat("=== RUN   TestSlowThing/case_x  waiting for the server to come up\n", 12) + "--- FAIL: TestSlowThing\n"
	res := killedRun(t, r, okLines(60, "0.3"), newOut, 700)
	if strings.Contains(res.Summary, "\n") || len(res.Summary) > summaryBytes {
		t.Errorf("Summary is %d bytes (%q), want one line within %d", len(res.Summary), res.Summary, summaryBytes)
	}
	if !strings.Contains(res.Summary, "=== RUN") {
		t.Errorf("Summary = %q, want a failing line", res.Summary)
	}
	if !strings.Contains(res.Signature, "did not finish within") {
		t.Errorf("Signature = %q, want the timeout", res.Signature)
	}
	plain := passThenFail(t, newRig(t, fixture.Opts{}), okLines(60, "0.3"), newOut)
	if plain.Signature == res.Signature {
		t.Errorf("a kill and a plain failure share the signature %q", res.Signature)
	}
}

// timedOut is the error the wedge guard gives a run of cmd it killed at 30m.
func timedOut(cmd string) error {
	return &envrun.TimedOutError{Cmd: cmd, Limit: 30 * time.Minute}
}

// longKill is the error of a killed run whose oracle command is n bytes.
func longKill(n int) error { return timedOut(strings.Repeat("x", n)) }

// manyNewLines is twelve distinct failing lines of about 70 bytes each.
func manyNewLines() string {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		b.WriteString("=== RUN   TestSlowThing/case_" + string(rune('a'+i)) + "  waiting for the server to come up\n")
	}
	return b.String()
}

// TestSeveralNewLinesMakeOneLineSummary: round 3 note 1, on the plain and the
// killed branch.
func TestSeveralNewLinesMakeOneLineSummary(t *testing.T) {
	t.Parallel()
	base := baselineOf("ok\n")
	for name, err := range map[string]error{"plain": errors.New("exit status 1"), "killed": longKill(10)} {
		_, s := failureOutput("ok\n"+manyNewLines(), err, base)
		if strings.ContainsAny(s, "\n\r") || !strings.HasPrefix(s, "=== RUN TestSlowThing/case_a waiting") || !strings.Contains(s, "case_b") {
			t.Errorf("%s: summary = %q, want the new lines on one line", name, s)
		}
	}
}

// TestKilledTailHoldsItsNewLinesTo4000Bytes: round 3 note 2.
func TestKilledTailHoldsItsNewLinesTo4000Bytes(t *testing.T) {
	t.Parallel()
	base := baselineOf("ok\n")
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("failure line " + strings.Repeat("x", 40) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + "\n")
	}
	kill := longKill(10)
	tail, _ := failureOutput(b.String(), kill, base)
	head := tail[:strings.Index(tail, "\n"+kill.Error()+"\n"+markerOldTail)]
	if len(head) > tailBytes || len(head) < tailBytes-60 {
		t.Errorf("new lines in the killed tail are %d bytes, want just under %d", len(head), tailBytes)
	}
}

// TestKilledSummaryWithALongCommandKeepsAFailingLine: round 3 note 3, with and
// without a baseline, however long the command.
func TestKilledSummaryWithALongCommandKeepsAFailingLine(t *testing.T) {
	t.Parallel()
	base := baselineOf("ok\n")
	for _, n := range []int{50, summaryBytes - 40, summaryBytes, 5 * summaryBytes} {
		for _, b := range []map[string]bool{base, nil} {
			_, s := failureOutput("ok\nboom: pkg9 broke\n", longKill(n), b)
			if len(s) > summaryBytes || strings.Contains(s, "\n") || !strings.HasSuffix(s, "did not finish within 30m0s") {
				t.Errorf("command of %d bytes: summary = %q (%d bytes)", n, s, len(s))
			}
			if n <= summaryBytes-60 && !strings.Contains(s, "boom: pkg9 broke") {
				t.Errorf("command of %d bytes: summary = %q, want the failing line", n, s)
			}
		}
	}
}

// TestKilledSignatureKeepsTheTimeout: round 3 note 4. However many new lines
// precede it the signature carries the timeout without its limit, two kills
// match and a kill does not match a plain failure.
func TestKilledSignatureKeepsTheTimeout(t *testing.T) {
	t.Parallel()
	base := baselineOf("ok\n")
	sig := func(out string, err error) string {
		_, s := failureOutput(out, err, base)
		return failureSignature("s", s, err)
	}
	out := "ok\n" + manyNewLines()
	k1, k2 := sig(out, longKill(10)), sig(out, longKill(700))
	if !strings.HasSuffix(k1, "did not finish within ms") || k1 != k2 {
		t.Errorf("signatures %q and %q, want one ending with the timeout", k1, k2)
	}
	if plain := sig(out, errors.New("exit status 1")); plain == k1 || strings.Contains(plain, "did not finish") {
		t.Errorf("plain %q, killed %q", plain, k1)
	}
}

// longOutput is ninety distinct short lines with no runs of whitespace, then a
// failing one: flattened they are about 800 bytes, well past the summary bound,
// and a short line keeps the part a cut drops small, so a room or a cut larger
// than the right one puts the summary over its bound.
func longOutput() string {
	var b strings.Builder
	for i := 0; i < 90; i++ {
		b.WriteString("FAIL:" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "q\n")
	}
	return b.String() + "FAIL pkg9 last line\n"
}

// TestKilledSummaryHoldsItsBoundWithNoBaselineOrNoNewLine: round 1 r1-f1. A
// long output and a long command, with no baseline and with a baseline that
// has no new line, give one line within the bound that ends with the reason,
// holds no part of the command and keeps the output's last line.
func TestKilledSummaryHoldsItsBoundWithNoBaselineOrNoNewLine(t *testing.T) {
	t.Parallel()
	out := longOutput()
	for name, b := range map[string]map[string]bool{"no baseline": nil, "no new line": baselineOf(out)} {
		_, s := failureOutput(out, longKill(300), b)
		if len(s) > summaryBytes || strings.Contains(s, "\n") || !strings.HasSuffix(s, " did not finish within 30m0s") {
			t.Errorf("%s: summary = %q (%d bytes), want one line within %d ending with the reason", name, s, len(s), summaryBytes)
		}
		if strings.Contains(s, "xxx") || !strings.Contains(s, "FAIL pkg9 last line") || len(s) < summaryBytes-30 {
			t.Errorf("%s: summary = %q, want the last line, most of the room used and no part of the command", name, s)
		}
	}
}

// phraseKill is the error of a killed run whose oracle command holds the
// guard's own phrase, with padding after it.
func phraseKill(pad int) error {
	return timedOut("grep -v 'did not finish within' " + strings.Repeat("x", pad))
}

// TestKilledReasonIsTheGuardsOwnWords: round 1 r1-f2. A command that holds
// "did not finish within" does not make the reason the rest of the command:
// the summary stays within its bound, keeps the failing line and does not
// panic, with or without a baseline.
func TestKilledReasonIsTheGuardsOwnWords(t *testing.T) {
	t.Parallel()
	if got := killedReason(phraseKill(760)); got != "did not finish within 30m0s" {
		t.Errorf("killedReason = %q", got)
	}
	base := baselineOf("ok\n")
	for _, pad := range []int{10, 760} {
		for name, b := range map[string]map[string]bool{"baseline": base, "no baseline": nil} {
			_, s := failureOutput("ok\nboom: pkg9 broke\n", phraseKill(pad), b)
			if len(s) > summaryBytes || strings.Contains(s, "\n") || !strings.HasSuffix(s, "did not finish within 30m0s") || strings.Contains(s, "xxx") {
				t.Errorf("%s, pad %d: summary = %q (%d bytes)", name, pad, s, len(s))
			}
			if !strings.Contains(s, "boom: pkg9 broke") {
				t.Errorf("%s, pad %d: summary = %q, want the failing line", name, pad, s)
			}
		}
	}
}

// TestKilledSummaryRoomIsExact: round 2 r1-f1. The end of the output is cut to
// exactly the room the reason leaves, one byte for the space before it: here
// the output is laid so that a room one byte larger would keep the line "Z".
func TestKilledSummaryRoomIsExact(t *testing.T) {
	t.Parallel()
	reason := "did not finish within 30m0s"
	room := summaryBytes - len(reason) - 1
	tail := strings.Repeat("y", room-2)
	out := "pad\nZ\n" + tail
	for name, b := range map[string]map[string]bool{"no baseline": nil, "no new line": baselineOf(out)} {
		_, s := failureOutput(out, longKill(300), b)
		if want := tail + " " + reason; s != want {
			t.Errorf("%s: summary = %q (%d bytes), want %q", name, s, len(s), want)
		}
	}
	// Retained text that fills the room exactly keeps every byte: a room one
	// byte smaller would drop one from the failing line.
	full := strings.Repeat("y", room)
	fullOut := "pad\n" + full
	for name, c := range map[string]struct {
		out  string
		base map[string]bool
	}{
		"no baseline": {fullOut, nil},
		"no new line": {fullOut, baselineOf(fullOut)},
		"new line":    {"ok\n" + full, baselineOf("ok\n")},
	} {
		_, s := failureOutput(c.out, longKill(300), c.base)
		if want := full + " " + reason; s != want || len(s) != summaryBytes {
			t.Errorf("%s, full room: summary = %q (%d bytes), want %d bytes %q", name, s, len(s), summaryBytes, want)
		}
	}
}

// plainPhraseFail is the error of a plain failure (exit status 1) of a command
// that holds the guard's phrase, with padding after it.
func plainPhraseFail(pad int) error {
	return errors.New(`shell "grep -v 'did not finish within' ` + strings.Repeat("x", pad) + `": exit status 1`)
}

// TestPlainFailureWhoseCommandHoldsTheGuardsPhraseIsNotAKill: round 2 r1-f2.
// A command that holds "did not finish within" does not make its plain failure
// a kill: the summary keeps its bound and its failing line, ends with no
// timeout, and the signature has none.
func TestPlainFailureWhoseCommandHoldsTheGuardsPhraseIsNotAKill(t *testing.T) {
	t.Parallel()
	base := baselineOf("ok\n")
	for _, pad := range []int{10, 760} {
		err := plainPhraseFail(pad)
		if got := killedReason(err); got != "" {
			t.Errorf("pad %d: killedReason = %q, want none", pad, got)
		}
		for name, b := range map[string]map[string]bool{"baseline": base, "no baseline": nil} {
			_, s := failureOutput("ok\nboom: pkg9 broke\n", err, b)
			if len(s) > summaryBytes || strings.Contains(s, "\n") || !strings.Contains(s, "boom: pkg9 broke") {
				t.Errorf("%s, pad %d: summary = %q (%d bytes), want one line within the bound with the failing line", name, pad, s, len(s))
			}
			if sig := failureSignature("s", s, err); strings.Contains(sig, "did not finish") {
				t.Errorf("%s, pad %d: signature %q holds a timeout", name, pad, sig)
			}
		}
		if got := failureTail("boom: pkg9 broke\n", err); got != "boom: pkg9 broke" {
			t.Errorf("pad %d: failureTail = %q, want only the output", pad, got)
		}
	}
}

// TestKilledSummaryJoinsItsLinesAndReasonWithOneSpace: round 1 r1-f3.
func TestKilledSummaryJoinsItsLinesAndReasonWithOneSpace(t *testing.T) {
	t.Parallel()
	_, s := failureOutput("ok\nboom: pkg9 broke\n", longKill(50), baselineOf("ok\n"))
	if want := "boom: pkg9 broke did not finish within 30m0s"; s != want {
		t.Errorf("summary = %q, want %q", s, want)
	}
	_, s = failureOutput("", longKill(50), nil)
	if want := "did not finish within 30m0s"; s != want {
		t.Errorf("no output: summary = %q, want %q", s, want)
	}
}
