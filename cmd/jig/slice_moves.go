package main

import (
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// The state a slice is in follows what its session does (decision 2 of the
// digester): a claim makes it building, a release queued again, a question
// parks it as needs-input and the answer sends it back. These are the moves,
// as functions of the state, so each is one rule in one place.

// claimHolder returns the session of the claim line of slice that no release
// line has undone since, and whether there is one. The session is "" for a
// claim made without one.
func claimHolder(lines []journal.Line, slice string) (session string, held bool) {
	for _, l := range lines {
		if l.Slice != slice {
			continue
		}
		switch l.Event {
		case journal.EventClaim:
			session, held = l.Session, true
		case journal.EventRelease:
			session, held = "", false
		}
	}
	return session, held
}

// parkedOrDone reports whether the state is one a claim or a release leaves
// alone: green is built, and needs-input waits on a question whoever holds the
// slice.
func parkedOrDone(state string) bool {
	return state == "green" || state == "needs-input"
}

// claimedState is ss after a claim: building, with the reason it was stalled or
// env-blocked cleared. A green or needs-input slice stays as it is.
func claimedState(ss store.SliceState) store.SliceState {
	if parkedOrDone(ss.State) {
		return ss
	}
	return moved(ss, "building")
}

// releasedState is ss after a release: queued again unless it is green; a
// slice parked on a question stays parked.
func releasedState(ss store.SliceState) store.SliceState {
	if parkedOrDone(ss.State) {
		return ss
	}
	return moved(ss, "queued")
}

// askedState is ss after a question about the slice was asked as qid: parked
// as needs-input on it when the slice was being worked or waiting its turn.
// Any other state (green, stalled, env-blocked) already says what the slice
// waits for, and keeps it.
func askedState(ss store.SliceState, qid string) store.SliceState {
	switch ss.State {
	case "queued", "building", "needs-input", "":
		ss.State = "needs-input"
		ss.Question = qid
	}
	return ss
}

// answeredState is ss after a question was answered. A slice parked on
// questions goes back to work once none is left open: building if a session
// holds it (claimed), else queued; while another question is open (stillOpen,
// its id) it stays parked on that one. A slice that is not parked is left as
// it is.
func answeredState(ss store.SliceState, claimed bool, stillOpen string) store.SliceState {
	if ss.State != "needs-input" {
		return ss
	}
	if stillOpen != "" {
		ss.Question = stillOpen
		return ss
	}
	ss.Question = ""
	if claimed {
		return moved(ss, "building")
	}
	return moved(ss, "queued")
}

// moved is ss in state, with the reason, signature and stall summary of the
// state it left cleared, as every route out of a stall clears them.
func moved(ss store.SliceState, state string) store.SliceState {
	ss.State = state
	ss.Reason = ""
	ss.Signature = ""
	ss.StallSummary = ""
	return ss
}
