package store

import (
	"path"
	"path/filepath"
	"strings"
)

// LedgerFile is the store-root file that holds one entry per ticket a publish
// has delivered: a "## <id> - <title>" section ending in the Answers tail
// jig recall reads.
const LedgerFile = "ledger.md"

// ContractIndexFile is the file under the store's platform directory that
// holds one "- <id>: workspaces ..., oracles ..." line per delivered ticket.
const ContractIndexFile = "contract-index.md"

// DefaultPlatformDir is the platform directory of a store whose config names
// none.
const DefaultPlatformDir = "platform/"

// LedgerPath is the absolute path of the store's ledger.md.
func (s *Store) LedgerPath() string {
	return filepath.Join(s.Root, LedgerFile)
}

// ContractIndexRelPath is the slash-separated, store-relative path of the
// contract index in the platform directory platform names, "" meaning
// DefaultPlatformDir.
func ContractIndexRelPath(platform string) string {
	if platform == "" {
		platform = DefaultPlatformDir
	}
	return path.Join(platform, ContractIndexFile)
}

// ContractIndexPath is the absolute path of the store's contract index, in the
// platform directory platform names ("" for the default).
func (s *Store) ContractIndexPath(platform string) string {
	return filepath.Join(s.Root, filepath.FromSlash(ContractIndexRelPath(platform)))
}

// ReplaceLedgerEntry returns ledger with ticket's entry replaced by entry, a
// whole "## <ticket> - <title>" section ending in a newline: the new text takes
// the place of the first entry the ticket already has, so the file keeps its
// order, and any later entry of the same ticket (a republish before this one
// appended a second) is dropped with the blank line that set it off. A ticket
// with no entry gets entry at the end, a blank line after the last. Every
// other byte of ledger stays as it was.
func ReplaceLedgerEntry(ledger, ticket, entry string) string {
	return replaceEntry(ledger, ticket, entry, ledgerEntries)
}

// ReplaceContractIndexEntry is ReplaceLedgerEntry for the contract index,
// whose entry is the one "- <ticket>: ..." line, entry, ending in a newline.
func ReplaceContractIndexEntry(index, ticket, entry string) string {
	return replaceEntry(index, ticket, entry, contractIndexEntries)
}

// RenameLedgerTickets returns ledger with the ticket id in each entry heading
// that renameOf names (old id to new id) replaced by the new one. Only the id
// moves: a title, an answer, or an id mentioned in prose is another ticket's
// or the person's words and stays. An id renameOf does not name, and so a
// ledger already renamed, comes back unchanged, provided no new id is itself
// an old one.
func RenameLedgerTickets(ledger string, renameOf map[string]string) string {
	return renameEntries(ledger, renameOf, ledgerEntries)
}

// RenameContractIndexTickets is RenameLedgerTickets for the contract index's
// "- <id>: ..." lines.
func RenameContractIndexTickets(index string, renameOf map[string]string) string {
	return renameEntries(index, renameOf, contractIndexEntries)
}

// entryShape says how one file lays out its entries.
type entryShape struct {
	// head reads a line that opens an entry: the ticket id it names, and where
	// that id starts and ends in the line.
	head func(line string) (id string, start, end int, ok bool)
	// end returns the first line past the entry that opens at lines[i], not
	// counting the blank lines that set the next entry off.
	end func(lines []string, i int) int
	// blankBetween says entries are separated by a blank line.
	blankBetween bool
}

var ledgerEntries = entryShape{
	head: ledgerHead,
	end: func(lines []string, i int) int {
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "## ") {
			j++
		}
		for j > i+1 && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		return j
	},
	blankBetween: true,
}

var contractIndexEntries = entryShape{
	head: contractIndexHead,
	end:  func(_ []string, i int) int { return i + 1 },
}

// ledgerHead reads a "## <id> - <title>" heading the way recall.ParseLedger
// does: the id is what precedes the first " - ", or the whole heading when it
// has no title.
func ledgerHead(line string) (id string, start, end int, ok bool) {
	rest, found := strings.CutPrefix(line, "## ")
	if !found {
		return "", 0, 0, false
	}
	text := strings.TrimRight(rest, " \t\r\n")
	lead := len(text) - len(strings.TrimLeft(text, " \t"))
	id, _, _ = strings.Cut(text[lead:], " - ")
	id = strings.TrimRight(id, " \t")
	if id == "" {
		return "", 0, 0, false
	}
	start = len("## ") + lead
	return id, start, start + len(id), true
}

// contractIndexHead reads a "- <id>: ..." line: the id is what stands between
// the bullet and the first colon, and holds no space, so a line of prose that
// happens to open "- note: ..." is not mistaken for a ticket's unless the
// ticket is named "note".
func contractIndexHead(line string) (id string, start, end int, ok bool) {
	rest, found := strings.CutPrefix(line, "- ")
	if !found {
		return "", 0, 0, false
	}
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 || strings.ContainsAny(rest[:colon], " \t") {
		return "", 0, 0, false
	}
	return rest[:colon], len("- "), len("- ") + colon, true
}

// splitLines splits text into its lines, each keeping its line ending.
func splitLines(text string) []string {
	lines := strings.SplitAfter(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

func replaceEntry(doc, ticket, entry string, shape entryShape) string {
	lines := splitLines(doc)
	out := make([]string, 0, len(lines)+1)
	replaced := false
	for i := 0; i < len(lines); {
		id, _, _, ok := shape.head(lines[i])
		if !ok || id != ticket {
			out = append(out, lines[i])
			i++
			continue
		}
		if !replaced {
			out = append(out, entry)
			replaced = true
		} else if shape.blankBetween {
			for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
				out = out[:len(out)-1]
			}
		}
		i = shape.end(lines, i)
	}
	if !replaced {
		if n := len(out); n > 0 && !strings.HasSuffix(out[n-1], "\n") {
			out[n-1] += "\n"
		}
		if shape.blankBetween {
			out = append(out, "\n")
		}
		out = append(out, entry)
	}
	return strings.Join(out, "")
}

func renameEntries(doc string, renameOf map[string]string, shape entryShape) string {
	if len(renameOf) == 0 {
		return doc
	}
	lines := strings.SplitAfter(doc, "\n")
	for i, line := range lines {
		id, start, end, ok := shape.head(line)
		if !ok {
			continue
		}
		if to, named := renameOf[id]; named {
			lines[i] = line[:start] + to + line[end:]
		}
	}
	return strings.Join(lines, "")
}
