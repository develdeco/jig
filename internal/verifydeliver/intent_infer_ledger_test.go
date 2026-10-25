package verifydeliver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// This file is the ledger half of the intent excerpt (intentExcerpt): the
// sessions the machine ledger holds for the ticket are read from the
// transcript paths their hooks recorded, and the search of every local
// transcript (intent_infer_test.go) is what is left when that finds none.

// ticketSessionWords is what the ticket's own session says, so that an excerpt
// that holds it was made from that session and no other.
const ticketSessionWords = "TICKET-SESSION-WORDS-7d41e2"

// writeStatingTranscript writes a minimal Claude Code transcript at path whose
// user turn says text and whose assistant turn mentions every file in
// mentioned. Unlike writeSyntheticClaudeSession it puts the file where the
// caller says, so a test can leave it where no search would look.
func writeStatingTranscript(t *testing.T, path, cwd, text string, mentioned ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	cwdJSON, _ := json.Marshal(cwd)
	userJSON, _ := json.Marshal(text)
	assistantJSON, _ := json.Marshal("I edited " + strings.Join(mentioned, ", ") + ".")
	lines := []string{
		`{"type":"user","cwd":` + string(cwdJSON) + `,"timestamp":"2026-01-01T00:10:00.000Z","message":{"role":"user","content":` + string(userJSON) + `}}`,
		`{"type":"assistant","cwd":` + string(cwdJSON) + `,"timestamp":"2026-01-01T00:20:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":` + string(assistantJSON) + `}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordSession writes the session-start line a hook writes for r.
func recordSession(t *testing.T, jigHome string, r ledger.Registration) {
	t.Helper()
	if r.Harness == "" {
		r.Harness = "claude"
	}
	if err := ledger.StartSession(jigHome, time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC), r); err != nil {
		t.Fatalf("record session %s: %v", r.ID, err)
	}
}

// claimedBy writes the journal line that a claim writes for the session.
func claimedBy(t *testing.T, st *store.Store, ticket, session string) {
	t.Helper()
	if err := journal.Append(st, ticket, journal.Line{Event: journal.EventClaim, Slice: "a", Session: session}); err != nil {
		t.Fatalf("append the claim of %s: %v", session, err)
	}
}

// ledgerRound is a round input over a gate-lease stand-in for JIG-1 that has
// no stated intent, an operator home holding the unrelated session of
// writeSyntheticClaudeSession (which the search would pick: it mentions the
// diff's files), and a jig home. diffFiles are the files the round changed.
type ledgerRound struct {
	in        RoundInput
	head      string
	diffFiles []string
	userHome  string
	jigHome   string
}

func newLedgerRound(t *testing.T) ledgerRound {
	t.Helper()
	userHome, jigHome := newIntentInferHomes(t)
	dir, head := newIntentInferLease(t)
	diffFiles := []string{"alpha/alpha.go", "alpha/percent.go"}
	writeSyntheticClaudeSession(t, userHome, "session-unrelated", dir, diffFiles, intentInferSessionMTime)
	return ledgerRound{
		in: RoundInput{
			Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir, RepoName: "fixture-repo", Target: "main",
			OperatorClone: dir, Home: jigHome, UserHome: userHome, Intent: Intent{Source: IntentSourceNone},
		},
		head: head, diffFiles: diffFiles, userHome: userHome, jigHome: jigHome,
	}
}

// excerpt runs the lookup and fails the test when it found nothing.
func (r ledgerRound) excerpt(t *testing.T) (path, text string) {
	t.Helper()
	path, note := intentExcerpt(r.in, r.head, r.diffFiles)
	if path == "" {
		t.Fatalf("intentExcerpt found nothing: %s", note)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the excerpt: %v", err)
	}
	return path, string(data)
}

// wantExcerptOf fails the test unless path is the excerpt of session and text
// holds words.
func (r ledgerRound) wantExcerptOf(t *testing.T, path, text, session, words string) {
	t.Helper()
	if want := filepath.Join(home.IntentExcerptDir(r.jigHome), r.in.Ticket, session+".md"); path != want {
		t.Errorf("excerpt path = %q, want %q", path, want)
	}
	if !strings.Contains(text, words) {
		t.Errorf("the excerpt does not hold %q:\n%s", words, text)
	}
}

// TestIntentExcerptComesFromTheTicketsSessionTheLedgerNames: two sessions are
// in the ledger, both with a transcript. One worked the ticket (it claimed a
// slice) and its transcript, which no search would find, states the intent;
// the other is on another ticket, and its transcript, which the search would
// pick, mentions the very files the round changed. The excerpt is the
// ticket's session's.
func TestIntentExcerptComesFromTheTicketsSessionTheLedgerNames(t *testing.T) {
	t.Parallel()
	r := newLedgerRound(t)
	ticketTranscript := filepath.Join(t.TempDir(), "elsewhere", "session-ticket.jsonl")
	writeStatingTranscript(t, ticketTranscript, r.in.LeaseDir, "Please clamp percentages. "+ticketSessionWords)
	claimedBy(t, r.in.Store, "JIG-1", "session-ticket")
	recordSession(t, r.jigHome, ledger.Registration{ID: "session-ticket", Ticket: "JIG-1", Transcript: ticketTranscript})
	recordSession(t, r.jigHome, ledger.Registration{ID: "session-unrelated", Ticket: "JIG-9", Transcript: filepath.Join(r.userHome, ".claude", "projects", "proj1", "session-unrelated.jsonl")})
	if err := ledger.Append(r.jigHome, journal.Line{Event: journal.EventTool, Session: "session-unrelated", Ticket: "JIG-9", Tool: "Edit", Files: r.diffFiles}); err != nil {
		t.Fatal(err)
	}

	path, text := r.excerpt(t)
	r.wantExcerptOf(t, path, text, "session-ticket", ticketSessionWords)
	if strings.Contains(text, intentInferExcerptMarker) {
		t.Errorf("the excerpt holds the unrelated session's words:\n%s", text)
	}
}

// TestIntentExcerptReadsOnlyTheTicketsSessions: of the sessions with a
// transcript, only those the records tie to the ticket are read, by what the
// ticket's journal and the ledger's file-changing tool lines say. A session
// that only read around the ticket (a reviewer, a search) or worked another
// is no source of the author's intent.
func TestIntentExcerptReadsOnlyTheTicketsSessions(t *testing.T) {
	t.Parallel()
	r := newLedgerRound(t)
	transcripts := t.TempDir()
	for id, words := range map[string]string{
		"session-claimer": "claimer-words", "session-editor": "editor-words", "session-reader": "reader-words",
	} {
		path := filepath.Join(transcripts, id+".jsonl")
		// Every transcript mentions the diff's files, so that only the ledger's
		// records can tell the reader from the builders.
		writeStatingTranscript(t, path, r.in.LeaseDir, words, r.diffFiles...)
		recordSession(t, r.jigHome, ledger.Registration{ID: id, Ticket: "JIG-1", Transcript: path})
	}
	claimedBy(t, r.in.Store, "JIG-1", "session-claimer")
	// The editor changed a file on the ticket's branch with no claim of its own.
	for _, l := range []journal.Line{
		{Event: journal.EventTool, Session: "session-editor", Branch: "jig/JIG-1", Tool: "Edit", Files: []string{"alpha/alpha.go"}},
		{Event: journal.EventTool, Session: "session-reader", Ticket: "JIG-1", Tool: "Bash", Command: "jig review context JIG-1"},
	} {
		if err := ledger.Append(r.jigHome, l); err != nil {
			t.Fatal(err)
		}
	}

	// With the branch the editor is a builder too; without it the editor's line
	// names no ticket, and the claimer is the only builder the records name.
	// The reader is never one.
	r.in.Branch = "jig/JIG-1"
	if ids := ticketSessionIDs(r.in); strings.Join(ids, ",") != "session-claimer,session-editor" {
		t.Errorf("the ticket's sessions with its branch = %v, want the claimer then the editor", ids)
	}
	r.in.Branch = ""
	if ids := ticketSessionIDs(r.in); strings.Join(ids, ",") != "session-claimer" {
		t.Errorf("the ticket's sessions without its branch = %v, want the claimer alone", ids)
	}
	path, text := r.excerpt(t)
	r.wantExcerptOf(t, path, text, "session-claimer", "claimer-words")
	if strings.Contains(text, "reader-words") {
		t.Errorf("the excerpt holds the words of a session that only read:\n%s", text)
	}
}

// TestIntentExcerptTakesTheBestMatchOfTheTicketsSessions: when more than one
// session worked the ticket, the one whose transcript mentions the most of the
// round's files is excerpted, whatever order the records name them in.
func TestIntentExcerptTakesTheBestMatchOfTheTicketsSessions(t *testing.T) {
	t.Parallel()
	r := newLedgerRound(t)
	transcripts := t.TempDir()
	for id, mentioned := range map[string][]string{
		"session-first": nil, "session-second": r.diffFiles[:1], "session-third": r.diffFiles,
	} {
		path := filepath.Join(transcripts, id+".jsonl")
		writeStatingTranscript(t, path, r.in.LeaseDir, id+"-words", mentioned...)
		recordSession(t, r.jigHome, ledger.Registration{ID: id, Ticket: "JIG-1", Transcript: path})
	}
	for _, id := range []string{"session-first", "session-second", "session-third"} {
		claimedBy(t, r.in.Store, "JIG-1", id)
	}

	path, text := r.excerpt(t)
	r.wantExcerptOf(t, path, text, "session-third", "session-third-words")
}

// TestIntentExcerptTakesTheTicketsDriver: the session the claims log names as
// the ticket's driver is a candidate though no journal line or tool line of
// its own names the ticket.
func TestIntentExcerptTakesTheTicketsDriver(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimDriver(fx.Ticket, "session-driver", false); err != nil {
		t.Fatalf("ClaimDriver: %v", err)
	}
	r := newLedgerRound(t)
	r.in.Store, r.in.Ticket = st, fx.Ticket
	driverTranscript := filepath.Join(t.TempDir(), "session-driver.jsonl")
	writeStatingTranscript(t, driverTranscript, r.in.LeaseDir, ticketSessionWords)
	recordSession(t, r.jigHome, ledger.Registration{ID: "session-driver", Transcript: driverTranscript})

	if ids := ticketSessionIDs(r.in); len(ids) != 1 || ids[0] != "session-driver" {
		t.Fatalf("the ticket's sessions = %v, want the driver", ids)
	}
	path, text := r.excerpt(t)
	r.wantExcerptOf(t, path, text, "session-driver", ticketSessionWords)
}

// TestIntentExcerptFallsBackToTheSearchWhenTheLedgerHasNothingToRead: with no
// ledger, or a ledger whose session for the ticket recorded no transcript, or
// whose transcript is gone, is not an absolute path or is of a harness jig
// reads no transcripts of, the lookup is today's search of every local
// transcript, and finds what it found before.
func TestIntentExcerptFallsBackToTheSearchWhenTheLedgerHasNothingToRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// reg is the ticket session's start line, given the transcript path
		// that exists; nil writes no ledger at all.
		reg func(present string) *ledger.Registration
	}{
		{"no ledger", func(string) *ledger.Registration { return nil }},
		{"no transcript recorded", func(string) *ledger.Registration {
			return &ledger.Registration{ID: "session-ticket", Ticket: "JIG-1"}
		}},
		{"the transcript is gone", func(present string) *ledger.Registration {
			return &ledger.Registration{ID: "session-ticket", Ticket: "JIG-1", Transcript: present + ".gone"}
		}},
		{"a relative path", func(string) *ledger.Registration {
			return &ledger.Registration{ID: "session-ticket", Ticket: "JIG-1", Transcript: "session-ticket.jsonl"}
		}},
		{"a harness jig does not read", func(present string) *ledger.Registration {
			return &ledger.Registration{ID: "session-ticket", Ticket: "JIG-1", Harness: "codex", Transcript: present}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLedgerRound(t)
			present := filepath.Join(t.TempDir(), "session-ticket.jsonl")
			writeStatingTranscript(t, present, r.in.LeaseDir, ticketSessionWords)
			claimedBy(t, r.in.Store, "JIG-1", "session-ticket")
			if reg := tc.reg(present); reg != nil {
				recordSession(t, r.jigHome, *reg)
			}

			path, text := r.excerpt(t)
			r.wantExcerptOf(t, path, text, "session-unrelated", intentInferExcerptMarker)
		})
	}
}

// TestIntentExcerptSaysWhyThereIsNoneWhenNeitherFindsOne: the ledger names a
// session of the ticket whose transcript is gone, and the search finds no
// session either: the reason is the search's, as it was before the ledger was
// asked.
func TestIntentExcerptSaysWhyThereIsNoneWhenNeitherFindsOne(t *testing.T) {
	t.Parallel()
	userHome, jigHome := newIntentInferHomes(t) // no transcripts to search
	dir, head := newIntentInferLease(t)
	in := RoundInput{
		Store: newReviewStore(t), Ticket: "JIG-1", Round: 1, LeaseDir: dir, RepoName: "fixture-repo", Target: "main",
		OperatorClone: dir, Home: jigHome, UserHome: userHome,
	}
	claimedBy(t, in.Store, "JIG-1", "session-ticket")
	recordSession(t, jigHome, ledger.Registration{ID: "session-ticket", Ticket: "JIG-1", Transcript: filepath.Join(t.TempDir(), "gone.jsonl")})

	path, note := intentExcerpt(in, head, []string{"alpha/alpha.go"})
	assertNoExcerpt(t, path, note, "no local agent sessions found for this repo and window")
}
