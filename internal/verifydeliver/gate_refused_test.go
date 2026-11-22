package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// TestGateRefusalJournaled: a gate that refuses its preconditions (the three
// codes of refusalCodes) leaves one gate-refused line with the code and a
// one-line summary, and no gate-open line, no round directory and no
// intent.md; any other refusal leaves none. The refusals are steps of one
// ticket, each on the ticket the ones before it left; the last is the rerun
// after the cause is gone, which opens round 1.
func TestGateRefusalJournaled(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()}) // brief.md present, every slice queued
	d := newDeps(t, fx)
	branch := pushedBranch(t, fx)

	refusals := func(t *testing.T) []journal.Line {
		t.Helper()
		lines, err := journal.Read(d.Store, fx.Ticket)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		var out []journal.Line
		for _, l := range lines {
			switch l.Event {
			case journal.EventGateRefused:
				out = append(out, l)
			case journal.EventGateOpen:
				t.Fatalf("a gate-open line after a refusal: %+v", lines)
			}
		}
		return out
	}
	wantRefusal := func(t *testing.T, o GateOpts, code string, total int) {
		t.Helper()
		o.Ticket = fx.Ticket
		_, err := Gate(d, alwaysCleanSource{}, o)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != code {
			t.Fatalf("err = %v, want *axi.Error %s", err, code)
		}
		got := refusals(t)
		if len(got) != total {
			t.Fatalf("gate-refused lines = %d, want %d: %+v", len(got), total, got)
		}
		last := got[len(got)-1]
		if last.Code != code || last.Summary == "" || strings.ContainsAny(last.Summary, "\r\n") || len(last.Summary) > refusalSummaryCap {
			t.Fatalf("last line = %+v, want code %s and a one-line summary of at most %d bytes", last, code, refusalSummaryCap)
		}
		for _, host := range []string{fx.RepoDir, d.Store.Root, filepath.ToSlash(fx.RepoDir), filepath.ToSlash(d.Store.Root)} {
			if strings.Contains(last.Summary, host) {
				t.Fatalf("summary %q holds the host path %q", last.Summary, host)
			}
		}
		if n, err := existingGateRounds(d.Store, fx.Ticket); err != nil || n != 0 {
			t.Fatalf("rounds = %d (err %v) after a refusal, want none", n, err)
		}
		if _, ok, err := d.Store.ReadIntent(fx.Ticket); err != nil || ok {
			t.Fatalf("ReadIntent after a refusal: ok=%v err=%v, want ok=false", ok, err)
		}
	}

	step(t, "a slice not green is GATE_NOT_GREEN", func(t *testing.T) {
		wantRefusal(t, GateOpts{Branch: branch}, "GATE_NOT_GREEN", 1)
		// The line is pushed at once, named by the code only.
		subject, err := gitx.Run(d.Store.Root, "log", "-1", "--format=%s")
		if want := fx.Ticket + ": gate refused: GATE_NOT_GREEN"; err != nil || subject != want {
			t.Fatalf("store head subject = %q (err %v), want %q", subject, err, want)
		}
	})
	step(t, "a branch nobody has is BRANCH_NOT_FOUND", func(t *testing.T) {
		wantRefusal(t, GateOpts{Branch: "no-such-branch", Early: true}, "BRANCH_NOT_FOUND", 2)
	})
	step(t, "--intent over a brief is INTENT_CONFLICT", func(t *testing.T) {
		wantRefusal(t, GateOpts{Branch: branch, Early: true, Intent: "explicit text"}, "INTENT_CONFLICT", 3)
	})
	step(t, "the same refusal again adds a second line", func(t *testing.T) {
		wantRefusal(t, GateOpts{Branch: branch, Early: true, Intent: "explicit text"}, "INTENT_CONFLICT", 4)
	})
	step(t, "a VALIDATION_ERROR refusal adds none", func(t *testing.T) {
		removeBrief(t, d, fx.Ticket)
		doc := filepath.Join(t.TempDir(), "doc.md")
		if err := os.WriteFile(doc, []byte("doc text"), 0o644); err != nil {
			t.Fatalf("write doc: %v", err)
		}
		_, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: branch, Early: true, Intent: "text", IntentDoc: doc})
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
			t.Fatalf("err = %v, want *axi.Error VALIDATION_ERROR", err)
		}
		if got := refusals(t); len(got) != 4 {
			t.Fatalf("gate-refused lines = %d, want the 4 it had", len(got))
		}
	})
	step(t, "the rerun, the cause gone, opens round 1", func(t *testing.T) {
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: branch, Early: true}); err != nil {
			t.Fatalf("Gate after the refusals: %v", err)
		}
		if n, err := existingGateRounds(d.Store, fx.Ticket); err != nil || n != 1 {
			t.Fatalf("rounds = %d (err %v), want 1", n, err)
		}
	})
}

// TestGateRefusalSummaryHoldsNoHostPath: a refusal message is one line of at
// most refusalSummaryCap bytes, cut before the first absolute path of either
// platform, so a message that would name a machine does not reach the journal.
func TestGateRefusalSummaryHoldsNoHostPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, msg, want string }{
		{"no path", "slice a is queued, not green;\n  finish it first", "slice a is queued, not green; finish it first"},
		{"unix path", "cannot read /home/dev/store/x.yaml now", "cannot read"},
		{"quoted drive path", `cannot read "C:\Users\dev\x.yaml"`, "cannot read"},
		{"forward slash drive path", "cannot read d:/work/x", "cannot read"},
		{"unc path", `cannot read \\host\share\x`, "cannot read"},
		{"colon before the path", "clone at: /srv/repo", "clone at"},
		{"a drive-like word is kept", "slice c: is queued, a:b and C: stay", "slice c: is queued, a:b and C: stay"},
		{"a slash inside a word is kept", "and/or the ./rel path stay", "and/or the ./rel path stay"},
		{"exactly the cap is kept", strings.Repeat("a", refusalSummaryCap), strings.Repeat("a", refusalSummaryCap)},
		{"one over the cap is cut to it", strings.Repeat("a", refusalSummaryCap+1), strings.Repeat("a", refusalSummaryCap)},
		{"a rune across the cap is dropped whole", strings.Repeat("a", refusalSummaryCap-1) + "é", strings.Repeat("a", refusalSummaryCap-1)},
		{"a rune that ends at the cap is kept", strings.Repeat("a", refusalSummaryCap-2) + "é", strings.Repeat("a", refusalSummaryCap-2) + "é"},
		{"the path is cut before the cap applies", strings.Repeat("a ", 20) + "/home/x " + strings.Repeat("b", 700), strings.TrimRight(strings.Repeat("a ", 20), " ")},
		{"a relative branch is kept","branch \"feature/x\" is neither in your clone nor on origin", "branch \"feature/x\" is neither in your clone nor on origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := refusalSummary(tc.msg); got != tc.want {
				t.Fatalf("refusalSummary(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
	long := strings.Repeat("é", 400)
	if got := refusalSummary(long); len(got) > refusalSummaryCap || !strings.HasPrefix(long, got) || len(got) < refusalSummaryCap-1 {
		t.Fatalf("a long message is cut to %d bytes on a rune boundary, got %d bytes", refusalSummaryCap, len(got))
	}
}
