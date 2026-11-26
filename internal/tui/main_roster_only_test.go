package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
)

// TestMainListsTheRosterOnly: an id the stream names that the roster does not
// list (one an old journal mentions) is no row of main and no ticket of its
// header's count, before and after a later read.
func TestMainListsTheRosterOnly(t *testing.T) {
	t.Parallel()
	m := New(quietConfig())
	m.w.apply(append(jig1(), jl(at(3, 0), "OLD-9", "", "note")))
	m.w.apply([]events.Envelope{jl(at(3, 1), "T-22", "a", "claim")})
	_, _ = m.Update(rosterMsg{rows: []rosterRow{listed("JIG-1"), listed("JIG-2")}, seq: 1})
	frame := m.View()
	if strings.Contains(frame, "T-22") || strings.Contains(frame, "OLD-9") || !strings.Contains(frame, "1 need you · 1 running") {
		t.Errorf("main lists what the roster does not, or counts it:\n%s", frame)
	}
	_, _ = m.Update(rosterMsg{rows: []rosterRow{listed("JIG-1"), listed("JIG-2"), listed("JIG-3")}, seq: 2})
	if frame := m.View(); strings.Contains(frame, "T-22") || !strings.Contains(frame, "1 need you · 2 running") {
		t.Errorf("a later roster brings the stream's ids back:\n%s", frame)
	}
}

// TestMainRefreshesMergesOnceAtLaunch: the first roster, when a row it lists
// waits on a merge, is followed by the background refresh of the kept states
// and then a roster again, so a launch does not show a state two minutes old;
// the second roster starts no refresh, and a list with no such row starts none.
func TestMainRefreshesMergesOnceAtLaunch(t *testing.T) {
	t.Parallel()
	g := newGate()
	cfg := quietConfig()
	cfg.Run = g.run
	cfg.Scope = []string{"--store", "/s"}
	m := New(cfg)
	waits := listed("T-1")
	waits.Digest.Phases = []digestPhase{
		{Name: "brief", State: "done", Text: "approved"}, {Name: "build", State: "done", Text: "done"},
		{Name: "review", State: "done", Text: "clean"}, {Name: "publish", State: "done", Text: "published"},
		{Name: "land", State: "current", Text: "waits for a merge"},
	}
	waits.Digest.Current = "land"
	_, cmd := m.Update(rosterMsg{rows: []rosterRow{waits}, seq: 1})
	start(cmd)
	if got := g.next(t); got != "next --refresh-merges --store /s" {
		t.Fatalf("after the first roster %q began, want the merge refresh", got)
	}
	g.release()
	// The refresh ends: the roster is read again, and that one starts no refresh.
	_, cmd = m.Update(mergeRefreshedMsg{})
	start(cmd)
	if got := g.next(t); got != "next --json --roster --store /s" {
		t.Fatalf("after the refresh %q began, want the roster", got)
	}
	g.release()
	_, cmd = m.Update(rosterMsg{rows: []rosterRow{waits}, seq: 2})
	start(cmd)
	g.none(t, "after the second roster")

	quiet := New(cfg)
	_, cmd = quiet.Update(rosterMsg{rows: []rosterRow{listed("T-1")}, seq: 1})
	start(cmd)
	g.none(t, "a list with no row that waits on a merge")
}

// TestAStreamLineAfterTheRosterAddsNoRow: the stream's replay can arrive after
// the first roster; a line for an id the roster does not list makes no row and
// is not counted, so the header never reads more tickets than the store holds.
func TestAStreamLineAfterTheRosterAddsNoRow(t *testing.T) {
	t.Parallel()
	m := New(quietConfig())
	_, _ = m.Update(rosterMsg{rows: []rosterRow{listed("JIG-1"), listed("JIG-2")}, seq: 1})
	m.w.apply([]events.Envelope{jl(at(3, 1), "T-23", "a", "claim"), jl(at(3, 2), "JIG-1", "a", "claim")})
	if frame := m.View(); strings.Contains(frame, "T-23") || !strings.Contains(frame, "2 running") {
		t.Errorf("a stream line after the roster made a row or was counted:\n%s", frame)
	}
}
