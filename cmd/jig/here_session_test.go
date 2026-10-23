package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
)

// hereSessionSection is the section this file registers, as `jig here` finds
// it among the registered ones.
func hereSessionSection(t *testing.T) hereSection {
	t.Helper()
	for _, s := range hereSections() {
		if s.Name == "session" {
			return s
		}
	}
	t.Fatal(`no "session" section is registered with jig here`)
	return hereSection{}
}

// hereSessionCtx is what jig here hands a section for a clone of a project
// found through the machine map, on a jig home of the test's own.
func hereSessionCtx(t *testing.T) (hereCtx, string) {
	t.Helper()
	e, root := sessionEnv(t)
	return hereCtx{
		e: e,
		Where: locate.Where{
			How: locate.HowMapPath, JigHome: root, Cwd: "/work/app/sub", Project: "demo", RepoDir: "/work/app", Ticket: "JIG-1", Slice: "a",
		},
		JigHome: root,
	}, root
}

// TestHereMintsSessionSection: a directory that resolves, with no session
// given, gets a minted one - printed as a session block and recorded as a
// session-start line that carries what here found.
func TestHereMintsSessionSection(t *testing.T) {
	t.Parallel()
	c, root := hereSessionCtx(t)

	blocks, err := hereSessionSection(t).Blocks(c)
	if err != nil {
		t.Fatalf("Blocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %q, want one session block", blocks)
	}
	m := regexp.MustCompile(`(?s)^session:\n  id: (s-[0-9a-f]{12})\n  how: minted\n  env: JIG_SESSION=(s-[0-9a-f]{12})$`).FindStringSubmatch(blocks[0])
	if m == nil || m[1] != m[2] {
		t.Fatalf("block = %q, want session: with a minted id, how: minted and the same id in env:", blocks[0])
	}

	lines, err := ledger.Read(root)
	if err != nil || len(lines) != 1 {
		t.Fatalf("ledger = %+v, %v, want the one session-start line", lines, err)
	}
	if l := lines[0]; l.Event != journal.EventSessionStart || l.Session != m[1] || l.Cwd != "/work/app/sub" || l.Ticket != "JIG-1" || l.Slice != "a" || l.TS != ledger.Stamp(sessionTestStart) {
		t.Errorf("line = %+v, want a session-start for %s at the cwd, ticket and slice here found, stamped by the env's clock", l, m[1])
	}
	got := sessionsIn(t, root)[m[1]]
	if got.Clone != "/work/app" || !got.Live() || got.Started != ledger.Stamp(sessionTestStart) {
		t.Errorf("session = %+v, want it live with the clone /work/app", got)
	}
}

// TestHereSessionJSONSection: the JSON form mints the same way and says what
// it minted.
func TestHereSessionJSONSection(t *testing.T) {
	t.Parallel()
	c, root := hereSessionCtx(t)

	v, err := hereSessionSection(t).JSON(c)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ ID, How string }
	if err := json.Unmarshal(raw, &got); err != nil || got.How != "minted" || !strings.HasPrefix(got.ID, "s-") {
		t.Fatalf("JSON = %s (%v), want {id, how: minted}", raw, err)
	}
	if _, ok := sessionsIn(t, root)[got.ID]; !ok {
		t.Errorf("the session %s the JSON names is not in the ledger", got.ID)
	}
}

// TestHereSessionSectionLeavesAGivenSessionAlone: a session from --session or
// JIG_SESSION is not replaced and not written.
func TestHereSessionSectionLeavesAGivenSessionAlone(t *testing.T) {
	t.Parallel()
	c, root := hereSessionCtx(t)
	c.Where.Session, c.Where.SessionHow = "given", locate.HowFlag

	sec := hereSessionSection(t)
	if blocks, err := sec.Blocks(c); err != nil || len(blocks) != 0 {
		t.Errorf("Blocks = %q, %v, want nothing", blocks, err)
	}
	if v, err := sec.JSON(c); err != nil || v != nil {
		t.Errorf("JSON = %v, %v, want nothing", v, err)
	}
	if _, err := os.Stat(home.LedgerPath(root)); err == nil {
		t.Errorf("the ledger was written for a session that was given")
	}
}

// TestHereSessionSectionIsSilentOutsideProjects: a directory in no project
// (no How, or none) mints nothing.
func TestHereSessionSectionIsSilentOutsideProjects(t *testing.T) {
	t.Parallel()
	c, root := hereSessionCtx(t)

	sec := hereSessionSection(t)
	for _, how := range []string{"", locate.HowNone} {
		c.Where.How = how
		if blocks, err := sec.Blocks(c); err != nil || len(blocks) != 0 {
			t.Errorf("How %q: Blocks = %q, %v, want nothing", how, blocks, err)
		}
		if v, err := sec.JSON(c); err != nil || v != nil {
			t.Errorf("How %q: JSON = %v, %v, want nothing", how, v, err)
		}
	}
	if _, err := os.Stat(home.LedgerPath(root)); err == nil {
		t.Errorf("the ledger was written for a directory in no project")
	}
}

// TestHereSessionSectionNeedsAJigHome: with no jig home to record in the
// section fails, rather than write a ledger under the working directory.
func TestHereSessionSectionNeedsAJigHome(t *testing.T) {
	t.Parallel()
	c, _ := hereSessionCtx(t)
	c.JigHome = ""

	if blocks, err := hereSessionSection(t).Blocks(c); err == nil || len(blocks) != 0 {
		t.Errorf("Blocks = %q, %v, want an error", blocks, err)
	}
}
