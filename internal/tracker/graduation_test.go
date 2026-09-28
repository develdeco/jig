package tracker_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tracker"
)

func TestGraduation(t *testing.T) {
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g := tracker.Graduation{
		Chart: "Do the thing",
		Tickets: []tracker.Draft{
			{Title: "Slice A", Body: "First slice"},
			{Title: "Slice B", Body: "Depends on A", BlockedBy: []tracker.Blocker{{Ref: "#1", Kind: "merged"}}},
			{Title: "Slice C", Body: "Depends on A and B", BlockedBy: []tracker.Blocker{{Ref: "#1", Kind: "merged"}, {Ref: "#2", Kind: "stacked"}}},
		},
	}

	ids, err := tracker.Graduate(a, st, g, nil)
	if err != nil {
		t.Fatalf("Graduate: %v", err)
	}
	want := []string{"JIG-1", "JIG-2", "JIG-3"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i, id := range ids {
		if id != want[i] {
			t.Fatalf("ids[%d] = %q, want %q", i, id, want[i])
		}
		if _, err := os.Stat(st.TicketDir(id)); err != nil {
			t.Fatalf("ticket folder missing for %s: %v", id, err)
		}
	}

	// Each ticket's tracker/ticket.md must have the title and body.
	for i, id := range ids {
		ticketPath := filepath.Join(st.TicketDir(id), "tracker", "ticket.md")
		data, err := os.ReadFile(ticketPath)
		if err != nil {
			t.Fatalf("read ticket.md for %s: %v", id, err)
		}
		content := string(data)
		if !strings.Contains(content, g.Tickets[i].Title) {
			t.Fatalf("ticket.md for %s missing title %q, got %q", id, g.Tickets[i].Title, content)
		}
		if !strings.Contains(content, g.Tickets[i].Body) {
			t.Fatalf("ticket.md for %s missing body %q, got %q", id, g.Tickets[i].Body, content)
		}
	}

	// No subtasks.yaml should be created.
	for _, id := range ids {
		subtasksPath := filepath.Join(st.TicketDir(id), "tracker", "subtasks.yaml")
		if _, err := os.Stat(subtasksPath); err == nil {
			t.Fatalf("subtasks.yaml should not exist for %s", id)
		}
	}

	// Slice A has no blockers, so its ticket.yaml records a title and no
	// blocked_by.
	gotA, err := st.ReadTicket(ids[0])
	if err != nil {
		t.Fatalf("ReadTicket(%s): %v", ids[0], err)
	}
	if gotA.Title != g.Tickets[0].Title || len(gotA.BlockedBy) != 0 {
		t.Fatalf("ticket.yaml for %s = %+v, want title %q and no blockers", ids[0], gotA, g.Tickets[0].Title)
	}

	// Slice B and C get ticket.yaml with resolved ids and explicit kinds.
	depsB, err := st.ReadTicketDeps(ids[1])
	if err != nil {
		t.Fatalf("ReadTicketDeps(%s): %v", ids[1], err)
	}
	wantB := []store.TicketBlockedBy{{Ticket: ids[0], Kind: "merged"}}
	if !reflect.DeepEqual(depsB, wantB) {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", ids[1], depsB, wantB)
	}

	depsC, err := st.ReadTicketDeps(ids[2])
	if err != nil {
		t.Fatalf("ReadTicketDeps(%s): %v", ids[2], err)
	}
	wantC := []store.TicketBlockedBy{{Ticket: ids[0], Kind: "merged"}, {Ticket: ids[1], Kind: "stacked"}}
	if !reflect.DeepEqual(depsC, wantC) {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", ids[2], depsC, wantC)
	}
}

// TestGraduateCallsOnMintedBeforeNextTicket covers the write-back contract a
// caller relies on: onMinted fires for ticket i, with its minted id, before
// ticket i+1 is minted - so a caller can persist ticket i's id and, if
// onMinted itself errors (standing in for "the caller's own write failed"),
// no further ticket is minted.
func TestGraduateCallsOnMintedBeforeNextTicket(t *testing.T) {
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g := tracker.Graduation{
		Tickets: []tracker.Draft{
			{Title: "Slice A"},
			{Title: "Slice B"},
			{Title: "Slice C"},
		},
	}

	var got []struct {
		i  int
		id string
	}
	failAt := 1
	onMinted := func(i int, id string) error {
		got = append(got, struct {
			i  int
			id string
		}{i, id})
		if i == failAt {
			return fmt.Errorf("boom")
		}
		return nil
	}

	ids, err := tracker.Graduate(a, st, g, onMinted)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Graduate error = %v, want it to wrap \"boom\"", err)
	}
	if len(got) != failAt+1 {
		t.Fatalf("onMinted calls = %+v, want exactly %d (stopping at the failing one)", got, failAt+1)
	}
	for i, call := range got {
		if call.i != i {
			t.Fatalf("onMinted call %d has i=%d, want %d", i, call.i, i)
		}
		if call.id == "" {
			t.Fatalf("onMinted call %d has an empty id", i)
		}
	}
	// The ticket at failAt was minted (and its id reported to onMinted)
	// before onMinted's own error stopped the run; the one after it was
	// never minted.
	if ids[failAt] == "" {
		t.Fatalf("ids[%d] = %q, want the id minted just before the failure", failAt, ids[failAt])
	}
	if ids[failAt+1] != "" {
		t.Fatalf("ids[%d] = %q, want empty: minting must stop once onMinted errors", failAt+1, ids[failAt+1])
	}
}

// TestGraduateRefusesUnresolvedIndexRef covers a "#k" BlockedBy ref that
// names no minted ticket (out of range, or a sibling not minted yet):
// Graduate must error rather than write the literal "#k" into the store as
// a ticket id.
func TestGraduateRefusesUnresolvedIndexRef(t *testing.T) {
	st, cfg := newTestStore(t, localCfg())
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g := tracker.Graduation{
		Tickets: []tracker.Draft{
			{Title: "Slice A", BlockedBy: []tracker.Blocker{{Ref: "#2", Kind: "merged"}}},
			{Title: "Slice B"},
		},
	}

	ids, err := tracker.Graduate(a, st, g, nil)
	if err == nil {
		t.Fatalf("Graduate: want an error for an unresolved \"#2\" ref, got ids %v", ids)
	}
	if ids[0] != "" {
		t.Fatalf("ids[0] = %q, want empty: the ticket referencing an unresolved ref must never be minted", ids[0])
	}
}

// fixedIDAdapter is an Adapter that mints the same id every time, like a
// command tracker whose ids come from elsewhere and can collide with a
// ticket the store already holds; the local tracker always mints the next
// free id and so never can.
type fixedIDAdapter struct{ id string }

func (fixedIDAdapter) Name() string                             { return "fixed" }
func (a fixedIDAdapter) Mint(tracker.Draft) (string, error)     { return a.id, nil }
func (fixedIDAdapter) Project(string, tracker.Projection) error { return nil }
func (fixedIDAdapter) Comment(string, string) error             { return nil }

// TestGraduateRefusesAMintedIDThatAlreadyHasARecord covers a minted id whose
// store folder already holds another ticket's ticket.yaml: Graduate refuses
// (store.ErrTicketRecordExists) instead of merging the new chart entry into
// it, which would overwrite the other ticket's title and give the new one
// its unrelated blockers. The existing record is unchanged, and onMinted
// never fires for a ticket whose record was refused.
func TestGraduateRefusesAMintedIDThatAlreadyHasARecord(t *testing.T) {
	st, _ := newTestStore(t, localCfg())
	seed := store.Ticket{
		Title:     "Pre-existing, unrelated",
		BlockedBy: []store.TicketBlockedBy{{Ticket: "EXT-9", Kind: "merged"}},
	}
	if err := st.CreateTicketRecord("EXT-1", seed); err != nil {
		t.Fatalf("seed EXT-1's record: %v", err)
	}

	g := tracker.Graduation{Tickets: []tracker.Draft{{Title: "New chart entry"}}}
	onMinted := false
	ids, err := tracker.Graduate(fixedIDAdapter{id: "EXT-1"}, st, g, func(int, string) error {
		onMinted = true
		return nil
	})
	if !errors.Is(err, store.ErrTicketRecordExists) {
		t.Fatalf("Graduate over an existing EXT-1 record: err = %v, want it to wrap store.ErrTicketRecordExists", err)
	}
	if onMinted {
		t.Fatal("onMinted fired for a ticket whose record was refused")
	}
	if len(ids) != 1 || ids[0] != "EXT-1" {
		t.Fatalf("ids = %v, want the minted [EXT-1], so the caller can report the orphan", ids)
	}

	got, err := st.ReadTicket("EXT-1")
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if !reflect.DeepEqual(got, seed) {
		t.Fatalf("EXT-1's record after the refused graduate = %+v, want it unchanged: %+v", got, seed)
	}
}

// TestGraduateRefusesAMintedIDJigCannotUse covers the same refusal jig ticket
// new makes, on the other path that mints into the store: a tracker whose id
// is known only once the ticket exists (github, a command) can mint one that
// names a lease (a reserved suffix) or that is not a single directory. Graduate
// refuses it after the mint, as an *axi.Error that names the tracker ticket to
// close (tracker.CheckMinted), and writes nothing under it: no store folder
// and no record, and for a path-like id no file outside the store either.
// onMinted never fires, and the minted id is still returned so the caller can
// say which ticket exists.
func TestGraduateRefusesAMintedIDJigCannotUse(t *testing.T) {
	for _, tc := range []struct{ name, id string }{
		{name: "reserved suffix", id: "EXT-7-gate"},
		{name: "path separator", id: "../escape"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := newTestStore(t, localCfg())
			g := tracker.Graduation{Tickets: []tracker.Draft{{Title: "New chart entry"}}}
			onMinted := false
			ids, err := tracker.Graduate(fixedIDAdapter{id: tc.id}, st, g, func(int, string) error {
				onMinted = true
				return nil
			})

			var ae *axi.Error
			if !errors.As(err, &ae) {
				t.Fatalf("Graduate over the unusable id %q: err = %v, want an *axi.Error", tc.id, err)
			}
			if !strings.Contains(ae.Msg, tc.id) || !strings.Contains(strings.Join(ae.Help, "\n"), "close it there") {
				t.Fatalf("Graduate over %q: Msg %q, Help %q, want the id named and the tracker ticket to close", tc.id, ae.Msg, ae.Help)
			}
			if onMinted {
				t.Fatal("onMinted fired for a ticket that was refused")
			}
			if len(ids) != 1 || ids[0] != tc.id {
				t.Fatalf("ids = %v, want the minted [%s], so the caller can report the ticket that now exists", ids, tc.id)
			}

			for _, dir := range []string{st.TicketDir(tc.id), filepath.Join(filepath.Dir(st.Root), "escape")} {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("the refused ticket %q left %s behind (stat err %v)", tc.id, dir, err)
				}
			}
		})
	}
}
