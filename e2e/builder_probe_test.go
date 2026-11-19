package e2e

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

// TestBuilderProbeReachesTheNextRound: a builder that claimed a slice records a
// surviving probe with no round open, `jig verify` shows it before review, and
// the round `jig gate` opens lists it in `probed` with its slice, leaving its
// hunk on the work list for the reviewer to probe again.
func TestBuilderProbeReachesTheNextRound(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-builder")
	base := h.git("rev-parse", "HEAD")
	if res := h.Jig("claim", fx.Ticket, "--slice", "a"); res.Code != 0 {
		t.Fatalf("jig claim exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	b := buildTicketOn(t, fx, env, true)

	hunks, err := gitx.DiffHunks(fx.RepoDir, base, "HEAD")
	if err != nil || len(hunks) == 0 {
		t.Fatalf("hunks of the change = %+v, %v; want some", hunks, err)
	}
	probed := hunks[0]

	// The builder probes a term of its change and the mutant survives.
	res := b.builder.Jig("probe", fx.Ticket, "--seam", "the bound of a percentage", "--file", probed.Path, "--line", strconv.Itoa(probed.From),
		"--mutation", "raise the bound", "--command", "go test ./alpha", "--outcome", "survived")
	if res.Code != 0 || !strings.Contains(res.Stdout, "slice: a") || !strings.Contains(res.Stdout, "recorded: builder") {
		t.Fatalf("jig probe with no round open: exit = %d, want it recorded for slice a\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}

	// `jig verify` shows it before any reviewer runs, and still covers.
	verify := b.builder.Verify(fx.Ticket, "a")
	for _, want := range []string{"covers: yes", "probes:\n  slice: a\n  current: 1\n  stale: 0\n  survived: 1\n", "survived,current,raise the bound"} {
		if verify.Code != 0 || !strings.Contains(verify.Stdout, want) {
			t.Fatalf("jig verify: exit = %d, lacks %q\nstdout:\n%s\nstderr:\n%s", verify.Code, want, verify.Stdout, verify.Stderr)
		}
	}

	// The round reads it with the slice that made it, and its hunk stays unprobed.
	b.openRound(t)
	rev := newHarness(t, fx, env, "s-reviewer")
	ctx := rev.Jig("review", "context", fx.Ticket, "--json")
	if ctx.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	var req struct {
		Probed []struct {
			Round   int    `json:"round"`
			Slice   string `json:"slice"`
			File    string `json:"file"`
			Line    int    `json:"line"`
			Outcome string `json:"outcome"`
			State   string `json:"state"`
		} `json:"probed"`
		Hunks []struct {
			File   string `json:"file"`
			From   int    `json:"from"`
			Probes int    `json:"probes"`
		} `json:"hunks"`
	}
	if err := json.Unmarshal([]byte(ctx.Stdout), &req); err != nil {
		t.Fatalf("parse review context --json: %v\n%s", err, ctx.Stdout)
	}
	if len(req.Probed) != 1 || req.Probed[0].Slice != "a" || req.Probed[0].Round != 0 || req.Probed[0].State != "current" || req.Probed[0].Outcome != "survived" ||
		req.Probed[0].File != probed.Path || req.Probed[0].Line != probed.From {
		t.Errorf("probed = %+v, want the builder's current survivor of slice a", req.Probed)
	}
	for _, h := range req.Hunks {
		if h.File == probed.Path && h.From == probed.From && h.Probes != 0 {
			t.Errorf("hunk %+v counts a probe, want the survivor to leave it for the reviewer to probe again", h)
		}
	}
	text := rev.Jig("review", "context", fx.Ticket)
	if !strings.Contains(text.Stdout, "builder_probes: 1 (slice a)") {
		t.Errorf("the round's view does not name the builder's slice\n%s", text.Stdout)
	}
}
