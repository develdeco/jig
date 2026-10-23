package main

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestSliceMovesFollowWhatTheSessionDoes pins the state each action leaves a
// slice in, from every state it can be in (decision 2 of the digester): a
// claim makes it building and a release queued, both clearing what stalled
// it, and neither touching green or a slice parked on a question; a question
// parks a slice that is queued or being worked; an answer sends a parked slice
// back to building when a session holds it and queued when none does, unless
// another question is still open.
func TestSliceMovesFollowWhatTheSessionDoes(t *testing.T) {
	t.Parallel()
	stalled := store.SliceState{State: "stalled", Attempts: 2, Reason: "stall", Signature: "sig", StallSummary: "why"}
	parked := store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}
	for _, tc := range []struct {
		name string
		got  store.SliceState
		want store.SliceState
	}{
		{"claim a queued slice", claimedState(store.SliceState{State: "queued"}), store.SliceState{State: "building"}},
		{"claim a stalled slice clears the stall", claimedState(stalled), store.SliceState{State: "building", Attempts: 2}},
		{"claim an env-blocked slice", claimedState(store.SliceState{State: "env-blocked", Reason: "blocked-by-env"}), store.SliceState{State: "building"}},
		{"claim leaves green", claimedState(store.SliceState{State: "green", Attempts: 1}), store.SliceState{State: "green", Attempts: 1}},
		{"claim leaves a parked slice parked", claimedState(parked), parked},

		{"release a building slice", releasedState(store.SliceState{State: "building", Attempts: 1}), store.SliceState{State: "queued", Attempts: 1}},
		{"release a stalled slice", releasedState(stalled), store.SliceState{State: "queued", Attempts: 2}},
		{"release leaves green", releasedState(store.SliceState{State: "green"}), store.SliceState{State: "green"}},
		{"release leaves a parked slice parked", releasedState(parked), parked},

		{"ask while queued", askedState(store.SliceState{State: "queued"}, "q-001"), store.SliceState{State: "needs-input", Question: "q-001"}},
		{"ask while building", askedState(store.SliceState{State: "building", Attempts: 1}, "q-001"), store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}},
		{"a second question parks on the newer", askedState(parked, "q-002"), store.SliceState{State: "needs-input", Attempts: 1, Question: "q-002"}},
		{"ask about a green slice", askedState(store.SliceState{State: "green"}, "q-001"), store.SliceState{State: "green"}},
		{"ask about a stalled slice keeps the stall", askedState(stalled, "q-001"), stalled},

		{"answer, held: building", answeredState(parked, true, ""), store.SliceState{State: "building", Attempts: 1}},
		{"answer, not held: queued", answeredState(parked, false, ""), store.SliceState{State: "queued", Attempts: 1}},
		{"answer with another question open", answeredState(parked, true, "q-002"), store.SliceState{State: "needs-input", Attempts: 1, Question: "q-002"}},
		{"answer to a slice not parked", answeredState(store.SliceState{State: "green", Attempts: 1}, true, ""), store.SliceState{State: "green", Attempts: 1}},
		{"answer clears what parked it", answeredState(store.SliceState{State: "needs-input", Question: "q-001", Reason: "flawed-brief"}, false, ""), store.SliceState{State: "queued"}},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, tc.got, tc.want)
		}
	}
}

// TestClaimHolderFollowsClaimAndRelease: the holder of a slice is the session
// of its latest claim line, until a release line of the slice; other slices
// lines do not count.
func TestClaimHolderFollowsClaimAndRelease(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		{Slice: "a", Event: journal.EventClaim, Session: "s-1"},
		{Slice: "b", Event: journal.EventClaim, Session: "s-2"},
		{Slice: "a", Event: journal.EventClaim, Session: "s-3"},
		{Slice: "b", Event: journal.EventRelease, Session: "s-2"},
		{Slice: "a", Event: journal.EventNote, Session: "s-9"},
	}
	if who, held := claimHolder(lines, "a"); !held || who != "s-3" {
		t.Errorf("holder of a = %q, %v, want s-3", who, held)
	}
	if who, held := claimHolder(lines, "b"); held {
		t.Errorf("holder of b = %q, %v, want none after the release", who, held)
	}
	if _, held := claimHolder(lines, "c"); held {
		t.Error("c was never claimed")
	}
	if who, held := claimHolder([]journal.Line{{Slice: "a", Event: journal.EventClaim}}, "a"); !held || who != "" {
		t.Errorf("a claim with no session = %q, %v, want held by the empty session", who, held)
	}
}

// TestSplitArgs pins how the verbs that take free text tell a leading ticket
// or question id from the words that follow.
func TestSplitArgs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in           []string
		ticket, text string
	}{
		{[]string{"JIG-1", "which", "tone?"}, "JIG-1", "which tone?"},
		{[]string{"which", "tone?"}, "", "which tone?"},
		{[]string{"JIG-1"}, "JIG-1", ""},
		{[]string{"Which tone is JIG-1?"}, "", "Which tone is JIG-1?"},
		{nil, "", ""},
	} {
		if ticket, text := splitTicket(tc.in); ticket != tc.ticket || text != tc.text {
			t.Errorf("splitTicket(%q) = %q, %q, want %q, %q", tc.in, ticket, text, tc.ticket, tc.text)
		}
	}
	for _, tc := range []struct {
		in                []string
		ticket, qid, text string
		wantErr           bool
	}{
		{[]string{"JIG-1", "q-002", "Casual,", "please."}, "JIG-1", "q-002", "Casual, please.", false},
		{[]string{"q-002", "Casual"}, "", "q-002", "Casual", false},
		{[]string{"JIG-1", "q-002"}, "", "", "", true},
		{[]string{"JIG-1", "Casual"}, "", "", "", true},
		{[]string{"JIG-1"}, "", "", "", true},
		{nil, "", "", "", true},
	} {
		ticket, qid, text, err := splitAnswerArgs(tc.in)
		if (err != nil) != tc.wantErr || ticket != tc.ticket || qid != tc.qid || text != tc.text {
			t.Errorf("splitAnswerArgs(%q) = %q, %q, %q, %v, want %q, %q, %q, error %v", tc.in, ticket, qid, text, err, tc.ticket, tc.qid, tc.text, tc.wantErr)
		}
	}
}

// TestFlattenKeepsAtMostMaxBytes: whitespace collapses to single spaces and a
// cut never splits a character.
func TestFlattenKeepsAtMostMaxBytes(t *testing.T) {
	t.Parallel()
	if got := flatten("  a\n\tb   c ", 100); got != "a b c" {
		t.Errorf("flatten = %q, want %q", got, "a b c")
	}
	got := flatten("aeiou\u00e9\u00e9\u00e9", 7)
	if len(got) > 7 || got != "aeiou\u00e9" {
		t.Errorf("flatten cut = %q (%d bytes), want %q", got, len(got), "aeiou\u00e9")
	}
}
