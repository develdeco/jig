package tui

import (
	"reflect"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// ticketOf is the ticket id with lines applied.
func ticketOf(id string, envs ...events.Envelope) *ticket {
	w := newWorld()
	w.apply(envs)
	t, _ := w.ticketFor(id)
	return t
}

func TestPhaseFollowsTheJournal(t *testing.T) {
	t.Parallel()
	claim := jl(at(0, 1), "T", "a", journal.EventClaim)
	pending := jl(at(0, 5), "T", "a", journal.EventVerifyPending)
	verified := jl(at(0, 9), "T", "a", journal.EventVerified)
	ask := jl(at(1, 0), "T", "b", journal.EventQuestion, with("s", "", "", "which?", "q-001", ""))
	answer := jl(at(1, 5), "T", "b", journal.EventAnswer, with("p", "", "", "this", "q-001", "person"))
	stall := jl(at(1, 9), "T", "c", journal.EventStall)
	open := jl(at(2, 0), "T", "", journal.EventGateOpen)
	round := jl(at(2, 5), "T", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt = 1 })
	clean := jl(at(3, 0), "T", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt = 2 })
	body := jl(at(3, 5), "T", "", journal.EventPrBody)
	pr := jl(at(3, 9), "T", "", journal.EventPr, func(l *journal.Line) { l.URL = "https://x/pull/1" })
	done := jl(at(4, 0), "T", "", journal.EventPublishDone)
	fix := jl(at(2, 30), "T", "f1", journal.EventClaim)

	tests := []struct {
		name  string
		lines []events.Envelope
		want  string
		label string
	}{
		{"nothing yet", nil, StateQueued, "queued"},
		{"a claim is building", []events.Envelope{claim}, StateBuilding, "building"},
		{"a verify with no result is verifying", []events.Envelope{claim, pending}, StateVerifying, "verifying"},
		{"a result ends the verifying", []events.Envelope{claim, pending, verified}, StateBuilding, "building"},
		{"a question nobody answered parks it", []events.Envelope{claim, ask}, StateNeedsInput, "needs input"},
		{"the answer ends the parking", []events.Envelope{claim, ask, answer}, StateBuilding, "building"},
		{"a stalled slice stalls the ticket", []events.Envelope{claim, stall}, StateStalled, "stalled"},
		{"an open round is in review", []events.Envelope{claim, verified, open}, StateReviewing, "in review"},
		{"a round with findings", []events.Envelope{claim, verified, open, round}, StateFindings, "review found asks"},
		{"fix slices built after the findings are building again", []events.Envelope{claim, open, round, fix}, StateBuilding, "building"},
		{"a clean round is ready", []events.Envelope{claim, open, clean}, StateReady, "ready to publish"},
		{"a body after the clean round is publishing", []events.Envelope{open, clean, body}, StatePublishing, "publishing"},
		{"a pull request", []events.Envelope{open, clean, body, pr}, StatePublishing, "publishing"},
		{"publish done", []events.Envelope{open, clean, body, pr, done}, StatePublished, "published"},
	}
	for _, tc := range tests {
		got, label := ticketOf("T", tc.lines...).phase(time.UTC)
		if got != tc.want || label != tc.label {
			t.Errorf("%s: phase = %s %q, want %s %q", tc.name, got, label, tc.want, tc.label)
		}
	}

	// A slice stalled since its claim stalls the ticket.
	stalled := ticketOf("T", jl(at(0, 1), "T", "c", journal.EventClaim), stall)
	if got, _ := stalled.phase(time.UTC); got != StateStalled {
		t.Errorf("a stall after the claim: phase = %s, want stalled", got)
	}
}

func TestPhaseTakesTheStoresWordOnTheSlices(t *testing.T) {
	t.Parallel()
	tk := ticketOf("T", jl(at(0, 1), "T", "a", journal.EventClaim))
	for state, want := range map[string]string{"stalled": StateStalled, "parked": StateNeedsInput, "env-blocked": StateEnvBlocked, "green": StateGreen, "building": StateBuilding} {
		tk.status = &statusInfo{State: state}
		if got, _ := tk.phase(time.UTC); got != want {
			t.Errorf("status %s: phase = %s, want %s", state, got, want)
		}
	}
}

// TestPhaseDoesNotCallATicketNothingStartedBuilding: a status that says new or
// queued is taken at its word, in the words it used, however the journal reads.
func TestPhaseDoesNotCallATicketNothingStartedBuilding(t *testing.T) {
	t.Parallel()
	tk := ticketOf("T", jl(at(0, 1), "T", "", journal.EventApproval))
	for _, word := range []string{"new", "queued"} {
		tk.status = &statusInfo{State: word}
		if got, label := tk.phase(time.UTC); got != StateQueued || label != word {
			t.Errorf("status %s: phase = %s %q, want %s %q", word, got, label, StateQueued, word)
		}
	}
}

func TestLinesStayInTheOrderTheyWereWritten(t *testing.T) {
	t.Parallel()
	// A line from another machine arrives late, by a merge, with an earlier stamp.
	tk := ticketOf("T",
		jl(at(0, 10), "T", "a", journal.EventClaim),
		jl(at(0, 30), "T", "a", journal.EventVerified),
		jl(at(0, 20), "T", "a", journal.EventOracle),
		jl("not a time", "T", "a", journal.EventNote),
	)
	var got []string
	for _, l := range tk.lines {
		got = append(got, l.Event)
	}
	if want := []string{"note", "claim", "oracle", "verified"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events in order = %v, want %v (a line with no readable time first)", got, want)
	}
}

func TestQuestionsAreOpenUntilAnswered(t *testing.T) {
	t.Parallel()
	tk := ticketOf("T",
		jl(at(0, 1), "T", "c", journal.EventQuestion, with("s", "", "", "first?", "q-001", "agent")),
		jl(at(0, 2), "T", "d", journal.EventQuestion, with("s", "", "", "second?", "q-002", "agent")),
		jl(at(0, 3), "T", "c", journal.EventAnswer, with("p", "", "", "yes", "q-001", "person")),
	)
	qs := tk.questions(time.UTC)
	if len(qs) != 2 || qs[0].Open || qs[0].Answer != "yes" || qs[0].Provenance != "person" || !qs[1].Open || qs[1].Text != "second?" {
		t.Fatalf("questions = %+v", qs)
	}
	if open := tk.openQuestions(time.UTC, ""); len(open) != 1 || open[0].ID != "q-002" {
		t.Errorf("open = %+v, want q-002", open)
	}
	if open := tk.openQuestions(time.UTC, "c"); len(open) != 0 {
		t.Errorf("open on slice c = %+v, want none", open)
	}

	// The store's word settles a question the journal has not caught up with,
	// and names one the journal has no line for.
	tk.status = &statusInfo{Questions: []statusQuestion{{"q-002", "d", "answered"}, {"q-003", "e", "open"}}}
	open := tk.openQuestions(time.UTC, "")
	if len(open) != 1 || open[0].ID != "q-003" || open[0].Slice != "e" {
		t.Errorf("open after a status = %+v, want only q-003", open)
	}
}

func TestRoundsNumberThemAndShowTheOpenOne(t *testing.T) {
	t.Parallel()
	tk := ticketOf("T",
		jl(at(1, 0), "T", "", journal.EventGateOpen),
		jl(at(1, 5), "T", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "rev" }),
		jl(at(2, 0), "T", "", journal.EventGateOpen),
	)
	rounds := tk.rounds(time.UTC)
	if len(rounds) != 2 || rounds[0].N != 1 || rounds[0].State != StateFindings || rounds[0].Reviewer != "rev" || rounds[1].N != 2 || rounds[1].State != StateReviewing {
		t.Fatalf("rounds = %+v, want round 1 with findings, round 2 open", rounds)
	}

	// The same round recorded twice (a clean round writes its line twice in
	// some paths) is one round.
	tk = ticketOf("T",
		jl(at(1, 0), "T", "", journal.EventGateOpen),
		jl(at(1, 5), "T", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt = 1 }),
		jl(at(1, 6), "T", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt = 1 }),
	)
	if rounds := tk.rounds(time.UTC); len(rounds) != 1 || rounds[0].State != StateReady {
		t.Errorf("rounds = %+v, want one clean round", rounds)
	}
}

func TestAsksCarryTheLatestDecision(t *testing.T) {
	t.Parallel()
	tk := ticketOf("T",
		jl(at(1, 0), "T", "", journal.EventTriage, with("a", "", "dismiss", "", "r1-f1", "agent")),
		jl(at(1, 5), "T", "", journal.EventTriage, with("p", "", "keep", "", "r1-f1", "person")),
	)
	if tk.asks() != nil {
		t.Fatalf("asks are known before a status is read")
	}
	tk.status = parseStatus(statusJIG2())
	asks := tk.asks()
	if len(asks) != 2 || asks[0].Decision != "keep" || asks[0].DecidedBy != "person" || asks[1].Decision != "" {
		t.Errorf("asks = %+v, want r1-f1 kept by a person and r1-f2 undecided", asks)
	}
}

func TestPublishCollectsTheJournalsAccount(t *testing.T) {
	t.Parallel()
	tk := ticketOf("JIG-2", jig2()...)
	got := tk.publish()
	want := publishInfo{
		BodyFor: sha('c'), Branch: "jig/JIG-2", ApprovedFor: sha('c'), ApprovedBy: "person",
		PR: "https://example.test/acme/demo/pull/7", PROutcome: "opened", Done: true,
	}
	if got != want {
		t.Errorf("publish = %+v, want %+v", got, want)
	}
}

func TestSlicesWithoutAStatusAreGuessedFromTheirLines(t *testing.T) {
	t.Parallel()
	tk := ticketOf("JIG-1", jig1()...)
	got := map[string]string{}
	for _, s := range tk.slices(time.UTC) {
		got[s.ID] = s.State
	}
	want := map[string]string{"a": StateGreen, "b": StateBuilding, "c": StateNeedsInput}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("guessed states = %v, want %v", got, want)
	}
	tk.status = parseStatus(statusJIG1())
	if rows := tk.slices(time.UTC); len(rows) != 4 || rows[3].ID != "d" || rows[3].State != StateQueued {
		t.Errorf("with a status the store's four slices are listed: %+v", rows)
	}
}

func TestVerifyingLaysOverTheStoresState(t *testing.T) {
	t.Parallel()
	tk := ticketOf("T",
		jl(at(0, 1), "T", "b", journal.EventClaim),
		jl(at(0, 5), "T", "b", journal.EventVerifyPending),
	)
	tk.status = &statusInfo{Slices: []statusSlice{{ID: "b", State: "building"}, {ID: "c", State: "stalled"}}}
	rows := tk.slices(time.UTC)
	if rows[0].State != StateVerifying || rows[1].State != StateStalled {
		t.Errorf("states = %s, %s, want verifying over building and stalled left alone", rows[0].State, rows[1].State)
	}
}

func TestALedgerLineOfASessionBelongsToItsTicket(t *testing.T) {
	t.Parallel()
	w := newWorld()
	touched, fresh := w.apply([]events.Envelope{
		ll(at(0, 1), "T", journal.EventVerifySpawned, func(l *journal.Line) { l.Session = "s-1" }),
		ll(at(0, 2), "", journal.EventTool, func(l *journal.Line) { l.Session, l.Tool = "s-1", "Bash" }),
		ll(at(0, 3), "", journal.EventTool, func(l *journal.Line) { l.Session, l.Tool = "s-other", "Bash" }),
	})
	if !fresh || !touched["T"] || len(touched) != 1 {
		t.Errorf("touched = %v fresh = %v", touched, fresh)
	}
	if got := len(w.tickets["T"].ledger); got != 2 {
		t.Errorf("ticket T has %d ledger lines, want its spawn line and its session's tool line", got)
	}
	if got := len(w.feed); got != 3 {
		t.Errorf("the feed holds %d lines, want all 3", got)
	}
}

func TestRosterOrdersTheTicketsAndKeepsTheOnesTheStreamAdded(t *testing.T) {
	t.Parallel()
	w := newWorld()
	w.apply([]events.Envelope{jl(at(0, 1), "JIG-9", "a", journal.EventClaim)})
	w.setRoster([]rosterRow{{"JIG-2", "Two", "ready"}, {"JIG-1", "One", "blocked"}})
	if want := []string{"JIG-2", "JIG-1", "JIG-9"}; !reflect.DeepEqual(w.order, want) {
		t.Errorf("order = %v, want the roster's, then the stream's", w.order)
	}
	if tk := w.tickets["JIG-1"]; tk.title != "One" || tk.rosterState != "blocked" {
		t.Errorf("JIG-1 = %+v", tk)
	}
}

func TestDescribeAndEventStateCoverEveryJournalEvent(t *testing.T) {
	t.Parallel()
	for _, ev := range []string{
		journal.EventClaim, journal.EventRelease, journal.EventVerifyPending, journal.EventOracle, journal.EventVerified,
		journal.EventVerifyError, journal.EventQuestion, journal.EventAnswer, journal.EventNote, journal.EventStall,
		journal.EventGateOpen, journal.EventGateRound, journal.EventGateClean, journal.EventTriage, journal.EventPrBody,
		journal.EventPublishApproved, journal.EventPr, journal.EventPublishDone,
	} {
		l := jl(at(0, 1), "T", "a", ev, with("s", sha('d'), "pass", "text", "q-001", "person"))
		if d := describe(l); d == "" || d == ev && ev != journal.EventNote {
			t.Errorf("describe(%s) = %q: a line should say more than its name", ev, d)
		}
		if eventState(l) == "" && ev != journal.EventRelease && ev != journal.EventNote {
			t.Errorf("event %s has no state to be drawn in", ev)
		}
	}
	if got := describe(jl(at(0, 1), "T", "a", journal.EventOracle, with("", sha('e'), "fail", "boom", "", ""))); got != "oracle fail boom" {
		t.Errorf("a failed oracle = %q", got)
	}
	if got := describe(jl(at(0, 1), "T", "", "something-new", with("", "", "odd", "", "", ""))); got != "something-new odd" {
		t.Errorf("an event the screens do not know = %q, want its name and outcome", got)
	}
}
