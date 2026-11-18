package verify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
)

// timedOut is the error the wedge guard gives a run of cmd it killed at 30m.
func timedOut(cmd string) error {
	return &envrun.TimedOutError{Cmd: cmd, Limit: 30 * time.Minute}
}

// longKill is the error of a killed run whose oracle command is n bytes.
func longKill(n int) error { return timedOut(strings.Repeat("x", n)) }

// TestKilledSummaryWithALongCommandKeepsAFailingLine: however long the
// command its error embeds, a killed run's summary is one line within its
// bound that ends with the guard's reason and keeps the failing line.
func TestKilledSummaryWithALongCommandKeepsAFailingLine(t *testing.T) {
	t.Parallel()
	for _, n := range []int{50, summaryBytes - 40, summaryBytes, 5 * summaryBytes} {
		_, s := failureOutput("ok\nboom: pkg9 broke\n", longKill(n))
		if len(s) > summaryBytes || strings.Contains(s, "\n") || !strings.HasSuffix(s, "did not finish within 30m0s") {
			t.Errorf("command of %d bytes: summary = %q (%d bytes)", n, s, len(s))
		}
		if n <= summaryBytes-60 && !strings.Contains(s, "boom: pkg9 broke") {
			t.Errorf("command of %d bytes: summary = %q, want the failing line", n, s)
		}
	}
}

// TestKilledSignatureKeepsTheTimeout: the signature carries the timeout
// without its limit, two kills match whatever their commands, and a kill does
// not match a plain failure.
func TestKilledSignatureKeepsTheTimeout(t *testing.T) {
	t.Parallel()
	sig := func(out string, err error) string {
		_, s := failureOutput(out, err)
		return failureSignature("s", s, err)
	}
	out := "ok\n" + strings.Repeat("=== RUN   TestSlowThing  waiting for the server to come up\n", 12)
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

// TestKilledSummaryHoldsItsBound: a long output and a long command give one
// line within the bound that ends with the reason, holds no part of the
// command and keeps the output's last line.
func TestKilledSummaryHoldsItsBound(t *testing.T) {
	t.Parallel()
	_, s := failureOutput(longOutput(), longKill(300))
	if len(s) > summaryBytes || strings.Contains(s, "\n") || !strings.HasSuffix(s, " did not finish within 30m0s") {
		t.Errorf("summary = %q (%d bytes), want one line within %d ending with the reason", s, len(s), summaryBytes)
	}
	if strings.Contains(s, "xxx") || !strings.Contains(s, "FAIL pkg9 last line") || len(s) < summaryBytes-30 {
		t.Errorf("summary = %q, want the last line, most of the room used and no part of the command", s)
	}
}

// phraseKill is the error of a killed run whose oracle command holds the
// guard's own phrase, with padding after it.
func phraseKill(pad int) error {
	return timedOut("grep -v 'did not finish within' " + strings.Repeat("x", pad))
}

// TestKilledReasonIsTheGuardsOwnWords: a command that holds
// "did not finish within" does not make the reason the rest of the command:
// the summary stays within its bound, keeps the failing line and does not
// panic.
func TestKilledReasonIsTheGuardsOwnWords(t *testing.T) {
	t.Parallel()
	if got := killedReason(phraseKill(760)); got != "did not finish within 30m0s" {
		t.Errorf("killedReason = %q", got)
	}
	for _, pad := range []int{10, 760} {
		_, s := failureOutput("ok\nboom: pkg9 broke\n", phraseKill(pad))
		if len(s) > summaryBytes || strings.Contains(s, "\n") || !strings.HasSuffix(s, "did not finish within 30m0s") || strings.Contains(s, "xxx") {
			t.Errorf("pad %d: summary = %q (%d bytes)", pad, s, len(s))
		}
		if !strings.Contains(s, "boom: pkg9 broke") {
			t.Errorf("pad %d: summary = %q, want the failing line", pad, s)
		}
	}
}

// TestKilledSummaryRoomIsExact: the end of the output is cut to exactly the
// room the reason leaves, one byte for the space before it: here the output
// is laid so that a room one byte larger would keep the line "Z".
func TestKilledSummaryRoomIsExact(t *testing.T) {
	t.Parallel()
	reason := "did not finish within 30m0s"
	room := summaryBytes - len(reason) - 1
	tail := strings.Repeat("y", room-2)
	_, s := failureOutput("pad\nZ\n"+tail, longKill(300))
	if want := tail + " " + reason; s != want {
		t.Errorf("summary = %q (%d bytes), want %q", s, len(s), want)
	}
	// Retained text that fills the room exactly keeps every byte: a room one
	// byte smaller would drop one from the failing line.
	full := strings.Repeat("y", room)
	_, s = failureOutput("pad\n"+full, longKill(300))
	if want := full + " " + reason; s != want || len(s) != summaryBytes {
		t.Errorf("full room: summary = %q (%d bytes), want %d bytes %q", s, len(s), summaryBytes, want)
	}
}

// plainPhraseFail is the error of a plain failure (exit status 1) of a command
// that holds the guard's phrase, with padding after it.
func plainPhraseFail(pad int) error {
	return errors.New(`shell "grep -v 'did not finish within' ` + strings.Repeat("x", pad) + `": exit status 1`)
}

// TestPlainFailureWhoseCommandHoldsTheGuardsPhraseIsNotAKill: a command that
// holds "did not finish within" does not make its plain failure a kill: the
// summary keeps its bound and its failing line, ends with no timeout, and the
// signature has none.
func TestPlainFailureWhoseCommandHoldsTheGuardsPhraseIsNotAKill(t *testing.T) {
	t.Parallel()
	for _, pad := range []int{10, 760} {
		err := plainPhraseFail(pad)
		if got := killedReason(err); got != "" {
			t.Errorf("pad %d: killedReason = %q, want none", pad, got)
		}
		_, s := failureOutput("ok\nboom: pkg9 broke\n", err)
		if len(s) > summaryBytes || strings.Contains(s, "\n") || !strings.Contains(s, "boom: pkg9 broke") {
			t.Errorf("pad %d: summary = %q (%d bytes), want one line within the bound with the failing line", pad, s, len(s))
		}
		if sig := failureSignature("s", s, err); strings.Contains(sig, "did not finish") {
			t.Errorf("pad %d: signature %q holds a timeout", pad, sig)
		}
		if got := failureTail("boom: pkg9 broke\n", err); got != "boom: pkg9 broke" {
			t.Errorf("pad %d: failureTail = %q, want only the output", pad, got)
		}
	}
}

func TestKilledSummaryJoinsItsLinesAndReasonWithOneSpace(t *testing.T) {
	t.Parallel()
	_, s := failureOutput("ok\nboom: pkg9 broke\n", longKill(50))
	if want := "ok boom: pkg9 broke did not finish within 30m0s"; s != want {
		t.Errorf("summary = %q, want %q", s, want)
	}
	_, s = failureOutput("", longKill(50))
	if want := "did not finish within 30m0s"; s != want {
		t.Errorf("no output: summary = %q, want %q", s, want)
	}
}
