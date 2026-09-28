package intent

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRenderExcerptDropsEmptyAndFormatsRoles(t *testing.T) {
	s := &Session{Messages: []Message{
		{Role: RoleUser, Text: "  fix the rounding bug  "},
		{Role: RoleAssistant, Text: ""},
		{Role: RoleAssistant, Text: "done, edited percent.go"},
	}}
	got := RenderExcerpt(s)
	if !strings.Contains(got, "user: fix the rounding bug") {
		t.Errorf("RenderExcerpt: missing user line, got %q", got)
	}
	if !strings.Contains(got, "assistant: done, edited percent.go") {
		t.Errorf("RenderExcerpt: missing assistant line, got %q", got)
	}
	if strings.Count(got, "assistant:") != 1 {
		t.Errorf("RenderExcerpt: want the empty assistant message dropped, got %q", got)
	}
}

func TestRenderExcerptNilSession(t *testing.T) {
	if got := RenderExcerpt(nil); got != "" {
		t.Errorf("RenderExcerpt(nil) = %q, want empty", got)
	}
}

// TestRenderExcerptLabelsSubagentPromptsDistinctly checks that a RoleUser
// message folded in from a subagent's own transcript (FromSubagent) is
// rendered as "subagent-prompt:", not "user:" - it is the parent agent's
// own prompt to that subagent, not the developer's own words, and the
// summarizer must not be told to read it as the developer's own ask. A
// subagent's own RoleAssistant messages are unaffected: an implementer
// subagent's actual work is still rendered as "assistant:".
func TestRenderExcerptLabelsSubagentPromptsDistinctly(t *testing.T) {
	s := &Session{Messages: []Message{
		{Role: RoleUser, Text: "please fix the rounding bug"},
		{Role: RoleUser, Text: "implement the fix for the rounding bug", FromSubagent: true},
		{Role: RoleAssistant, Text: "done, edited percent.go", FromSubagent: true},
	}}
	got := RenderExcerpt(s)
	if !strings.Contains(got, "user: please fix the rounding bug") {
		t.Errorf("RenderExcerpt: missing the developer's own user line, got %q", got)
	}
	if !strings.Contains(got, "subagent-prompt: implement the fix for the rounding bug") {
		t.Errorf("RenderExcerpt: want the folded subagent prompt labeled subagent-prompt, got %q", got)
	}
	if strings.Contains(got, "user: implement the fix") {
		t.Errorf("RenderExcerpt: the folded subagent prompt must not be labeled user, got %q", got)
	}
	if !strings.Contains(got, "assistant: done, edited percent.go") {
		t.Errorf("RenderExcerpt: want the subagent's own assistant turn still labeled assistant, got %q", got)
	}
}

// TestRenderExcerptCapsOversizedHeadLine: a single huge opening message
// must not pass the cap uncut. MaxExcerptBytes' own doc, RenderExcerpt's
// doc comment and ADR 0012 all promise the excerpt is capped at
// MaxExcerptBytes - this checks that promise holds even when the oversized
// line is the very first one.
func TestRenderExcerptCapsOversizedHeadLine(t *testing.T) {
	huge := "THE OPENING ASK STARTS HERE: " + strings.Repeat("x", MaxExcerptBytes+40000)
	s := &Session{Messages: []Message{
		{Role: RoleUser, Text: huge},
		{Role: RoleAssistant, Text: "a short later turn"},
	}}
	got := RenderExcerpt(s)
	if len(got) > MaxExcerptBytes {
		t.Fatalf("RenderExcerpt length = %d, want at most %d (MaxExcerptBytes)", len(got), MaxExcerptBytes)
	}
	if !strings.Contains(got, "THE OPENING ASK STARTS HERE") {
		t.Error("RenderExcerpt: the truncated head line lost even its own start")
	}
}

// TestRenderExcerptCapsOversizedTailLine is
// TestRenderExcerptCapsOversizedHeadLine's counterpart for the other end:
// the single most-recent line, the first candidate for the tail, is cut
// too when it alone is over budget, however large.
func TestRenderExcerptCapsOversizedTailLine(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	huge := strings.Repeat("y", MaxExcerptBytes+40000) + " THE LATEST TURN ENDS HERE"
	s := &Session{Messages: []Message{
		{Role: RoleUser, Timestamp: base, Text: "an ordinary opening ask"},
		{Role: RoleAssistant, Timestamp: base.Add(time.Minute), Text: huge},
	}}
	got := RenderExcerpt(s)
	if len(got) > MaxExcerptBytes {
		t.Fatalf("RenderExcerpt length = %d, want at most %d (MaxExcerptBytes)", len(got), MaxExcerptBytes)
	}
	if !strings.Contains(got, "THE LATEST TURN ENDS HERE") {
		t.Error("RenderExcerpt: the truncated tail line lost even its own end")
	}
}

// TestRenderExcerptCapCountsTheOmissionMarker: the cap holds the omission
// marker's own bytes too. A short opening ask, one middle line that gets
// dropped and a latest line far over budget (cut to fit) put the marker
// between the kept ends - and the whole must still fit MaxExcerptBytes,
// not MaxExcerptBytes plus the marker.
func TestRenderExcerptCapCountsTheOmissionMarker(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &Session{Messages: []Message{
		{Role: RoleUser, Timestamp: base, Text: "a short opening ask"},
		{Role: RoleAssistant, Timestamp: base.Add(time.Minute), Text: "a middle turn that gets dropped"},
		{Role: RoleAssistant, Timestamp: base.Add(2 * time.Minute), Text: strings.Repeat("y", 40000) + " THE LATEST TURN ENDS HERE"},
	}}
	got := RenderExcerpt(s)
	if !strings.Contains(got, excerptOmissionMarker) {
		t.Fatalf("RenderExcerpt: want the omission marker between the kept ends, got %d bytes without it", len(got))
	}
	if len(got) > MaxExcerptBytes {
		t.Fatalf("RenderExcerpt length = %d, want at most %d (the omission marker included)", len(got), MaxExcerptBytes)
	}
	if !strings.Contains(got, "THE LATEST TURN ENDS HERE") {
		t.Error("RenderExcerpt: the truncated tail line lost even its own end")
	}
}

// TestCapExcerptLinesNeverExceedsTheBudget sweeps the budget across every
// small size, over line sets that reach each branch (a single line, an
// opening line that fills the budget, dropped middle lines with the
// omission marker, an oversized latest line, multi-byte text): whatever the
// budget, the result fits it, never splits a rune, and holds only whole
// lines, the omission marker, or a cut line that says it was cut; a set
// that already fits comes back whole.
func TestCapExcerptLinesNeverExceedsTheBudget(t *testing.T) {
	t.Parallel()
	sets := map[string][]string{
		"single":      {strings.Repeat("a", 90)},
		"two":         {"opening " + strings.Repeat("b", 60), "latest " + strings.Repeat("c", 60)},
		"many short":  {"opening ask", "one", "two", "three", "four", "five", "six", "seven", "latest"},
		"long middle": {"opening ask", strings.Repeat("m", 80), strings.Repeat("n", 80), "latest turn"},
		"huge latest": {"opening ask", "middle", strings.Repeat("z", 200)},
		"multi-byte":  {strings.Repeat("é", 40), strings.Repeat("世", 30), strings.Repeat("ü", 40)},
	}
	for name, lines := range sets {
		full := strings.Join(lines, excerptSep)
		for limit := 0; limit <= len(full)+10; limit++ {
			got := capExcerptLines(lines, limit)
			if len(got) > limit {
				t.Fatalf("%s: capExcerptLines(limit=%d) = %d bytes, want at most %d\n%q", name, limit, len(got), limit, got)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("%s: capExcerptLines(limit=%d) split a multi-byte rune: %q", name, limit, got)
			}
			if len(full) <= limit && got != full {
				t.Fatalf("%s: capExcerptLines(limit=%d) cut text that already fits", name, limit)
			}
			for _, seg := range strings.Split(got, excerptSep) {
				whole := seg == "" || seg == excerptOmissionMarker
				for _, l := range lines {
					whole = whole || seg == l
				}
				marked := strings.HasPrefix(seg, excerptLineTruncMarker) || strings.HasSuffix(seg, excerptLineTruncMarker)
				if !whole && !marked {
					t.Fatalf("%s: capExcerptLines(limit=%d) holds %q, neither a whole line, the omission marker nor a marked cut", name, limit, seg)
				}
			}
		}
	}
}

// TestRenderExcerptCapsToMostRecent checks that an oversized transcript is
// capped by dropping the oldest lines first, keeping the most recent
// (later) content intact and under the byte cap.
func TestRenderExcerptCapsToMostRecent(t *testing.T) {
	var msgs []Message
	for i := 0; i < 2000; i++ {
		msgs = append(msgs, Message{Role: RoleUser, Text: "line filler content to pad the transcript out"})
	}
	msgs = append(msgs, Message{Role: RoleAssistant, Text: "THE FINAL MESSAGE"})
	s := &Session{Messages: msgs}

	got := RenderExcerpt(s)
	if len(got) > MaxExcerptBytes {
		t.Fatalf("RenderExcerpt length = %d, want at most %d (the omission notice included)", len(got), MaxExcerptBytes)
	}
	if !strings.Contains(got, "THE FINAL MESSAGE") {
		t.Fatal("RenderExcerpt: capped output dropped the most recent message, want it kept")
	}
	if !strings.Contains(got, "omitted") {
		t.Error("RenderExcerpt: capped output has no omission notice")
	}
}

// TestRenderExcerptKeepsOriginalAskAcrossALongSubagentFold: a folded-in
// implementer subagent's own turns are not simply appended after every one
// of the parent's own messages - they are merged in by Timestamp - and
// even once the excerpt is capped, the session's own opening ask survives,
// not only whichever content happens to fall in the kept tail. One opening
// user ask, 40 parent turns and 30 subagent turns.
func TestRenderExcerptKeepsOriginalAskAcrossALongSubagentFold(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var msgs []Message
	msgs = append(msgs, Message{
		Role: RoleUser, Timestamp: base,
		Text: "THE ORIGINAL ASK: please fix the rounding bug in the percent helper.",
	})
	// 40 parent turns, timestamped after the opening ask.
	for i := 0; i < 40; i++ {
		msgs = append(msgs, Message{
			Role: RoleAssistant, Timestamp: base.Add(time.Duration(i+1) * time.Minute),
			Text: fmt.Sprintf("parent turn %d: %s", i, strings.Repeat("filler content to pad the transcript out. ", 40)),
		})
	}
	// 30 subagent turns, interleaved in time (earlier than most parent
	// turns) but appended after them in Messages, the way the reader's own
	// fold does it (parent messages first, subagent messages appended).
	for i := 0; i < 30; i++ {
		msgs = append(msgs, Message{
			Role: RoleAssistant, Timestamp: base.Add(time.Duration(i+1) * time.Second),
			Text: fmt.Sprintf("subagent turn %d: %s", i, strings.Repeat("filler content to pad the transcript out. ", 40)),
		})
	}
	s := &Session{Messages: msgs}

	got := RenderExcerpt(s)
	if len(got) > MaxExcerptBytes {
		t.Fatalf("RenderExcerpt length = %d, want at most %d (the omission notice included)", len(got), MaxExcerptBytes)
	}
	if !strings.Contains(got, "THE ORIGINAL ASK") {
		t.Fatal("RenderExcerpt: capped output dropped the original ask, want it kept")
	}
	if !strings.Contains(got, "parent turn 39") {
		t.Error("RenderExcerpt: capped output dropped the most recent parent turn, want it kept")
	}
}
