package recall

import (
	"strings"
)

// Answer is one bullet of a ledger entry's Answers section: the question's id
// and the rest of the bullet, which is "<question> -> <answer>" as the publish
// that wrote it formatted it. The text is kept whole: neither side can be told
// from the arrow when the question or the answer holds one itself.
type Answer struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// LedgerEntry is one ticket's entry in the store's ledger.md: the heading
// "## <ticket> - <title>" and the answers the person gave while it was built.
type LedgerEntry struct {
	Ticket  string   `json:"ticket"`
	Title   string   `json:"title,omitempty"`
	Answers []Answer `json:"answers"`
}

// answersMarker is the line that opens an entry's Answers section.
const answersMarker = "**Answers:**"

// ParseLedger reads ledger.md into its entries, oldest first (the file is only
// appended to). Text before the first "## " heading is skipped. In an entry,
// the lines after the Answers marker are read as bullets: "- q-001: ..." opens
// an answer, and a line that is neither a bullet nor blank continues the one
// before it. The section is "none" for an entry with no answers, which gives
// the entry no Answers.
func ParseLedger(text string) []LedgerEntry {
	var entries []LedgerEntry
	var cur *LedgerEntry
	inAnswers := false
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			entries = append(entries, LedgerEntry{Answers: []Answer{}})
			cur = &entries[len(entries)-1]
			cur.Ticket, cur.Title = splitHeading(rest)
			inAnswers = false
			continue
		}
		if cur == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == answersMarker:
			inAnswers = true
		case !inAnswers || trimmed == "":
		case strings.HasPrefix(trimmed, "- "):
			id, text, _ := strings.Cut(strings.TrimPrefix(trimmed, "- "), ": ")
			cur.Answers = append(cur.Answers, Answer{ID: strings.TrimSpace(id), Text: strings.TrimSpace(text)})
		case trimmed == "none":
		case len(cur.Answers) > 0:
			last := &cur.Answers[len(cur.Answers)-1]
			last.Text = strings.TrimSpace(last.Text + " " + trimmed)
		}
	}
	return entries
}

// splitHeading splits an entry heading, "<ticket> - <title>", at its first
// " - ". A heading with no title is a ticket alone.
func splitHeading(h string) (ticket, title string) {
	ticket, title, _ = strings.Cut(strings.TrimSpace(h), " - ")
	return strings.TrimSpace(ticket), strings.TrimSpace(title)
}
