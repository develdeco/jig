package tui

import (
	"strconv"
	"strings"
	"unicode"
)

// A search narrows a list to the rows whose own words start with every term
// typed. This file is the rule and nothing else: it takes the words of rows
// and returns the rows kept, so any screen's list can use it once it says what
// a row's words are. The words are the ones the row's line already shows or
// stands on; a search reads nothing a list has not got.

// rowFacts are what a row says of itself, as the screens do: the words of a
// row come from these and from nothing else.
type rowFacts struct {
	id    string
	chart bool
	title string
	// kind is the ticket's kind as the digest names it (issue, task,
	// multi-task); a chart's row is the word chart whatever it says here.
	kind string
	// parent is the issue or multi-task a task is in, issues every issue whose
	// chart lists the row, charts the charts that list it, and plan the chart
	// that plans an issue.
	parent string
	issues []string
	charts []string
	plan   string
	// group is the name of the group the row stands in, end the word of its end
	// state (published, waits to land, landed, closed) when it is at one.
	group string
	end   string
	// prs are the numbers of the row's pull requests.
	prs []int
}

// rowWords are the words of a row, lower case: its id whole and in its parts,
// the words of its title, its kind, the issue and the charts around it, the
// words of its state, and its pull requests' numbers.
func rowWords(f rowFacts) []string {
	var out []string
	out = append(out, wordsOf(f.id)...)
	out = append(out, wordsOf(f.title)...)
	if f.chart {
		out = append(out, "chart")
	} else {
		out = append(out, wordsOf(f.kind)...)
	}
	out = append(out, wordsOf(f.parent)...)
	for _, id := range f.issues {
		out = append(out, wordsOf(id)...)
	}
	for _, name := range f.charts {
		out = append(out, wordsOf(name)...)
	}
	out = append(out, wordsOf(f.plan)...)
	out = append(out, wordsOf(f.group)...)
	out = append(out, wordsOf(f.end)...)
	for _, n := range f.prs {
		out = append(out, strconv.Itoa(n))
	}
	return out
}

// wordsOf are the words of s, lower case: each run of letters and digits, and
// each blank-separated token whole, so `rel-11` is found as typed and by its
// parts, `rel` and `11`.
func wordsOf(s string) []string {
	var out []string
	for _, tok := range strings.Fields(strings.ToLower(s)) {
		parts := strings.FieldsFunc(tok, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if len(parts) != 1 || parts[0] != tok {
			out = append(out, tok)
		}
		out = append(out, parts...)
	}
	return out
}

// searchTerms are the terms of a query: its blank-separated words, lower case,
// a leading # dropped (so #287 is the number 287).
func searchTerms(query string) []string {
	var terms []string
	for _, t := range strings.Fields(strings.ToLower(query)) {
		if t = strings.TrimLeft(t, "#"); t != "" {
			terms = append(terms, t)
		}
	}
	return terms
}

// matches says every term starts one of the words, in any order.
func matches(terms, words []string) bool {
	for _, t := range terms {
		found := false
		for _, w := range words {
			if strings.HasPrefix(w, t) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// searchRun is one query applied to a list of rows.
type searchRun struct {
	// hit says a row matches by its own words.
	hit func(queueRow) bool
	// self are the rows drawn that match by themselves, and tickets the tickets
	// and charts that do, each once however many rows draw it.
	self    map[rowIdentity]bool
	tickets map[string]bool
}

func newSearchRun(hit func(queueRow) bool) *searchRun {
	return &searchRun{hit: hit, self: map[rowIdentity]bool{}, tickets: map[string]bool{}}
}

// keep is the rows that match or have a row under them that does, each with its
// matching rows only: a hit on a task keeps its issue, a hit on an issue keeps
// no task that does not match too.
func (s *searchRun) keep(rows []queueRow) []queueRow {
	var out []queueRow
	for _, r := range rows {
		kids := s.keep(r.kids)
		own := s.hit(r)
		if !own && len(kids) == 0 {
			continue
		}
		if own {
			s.self[r.ref().identity()] = true
			s.tickets[kindKey(r)] = true
		}
		r.kids = kids
		out = append(out, r)
	}
	return out
}

// litMask says which of the runes of text are the letters a term matched: the
// first letters of a word (a blank-separated token whole, or a run of letters
// and digits inside it) that a term starts, as many as the term has. Only a word
// that is one of the row's own (own) is lit: a word the row's cell says but the
// row does not hold lights nothing, so what is lit is why the row is kept.
func litMask(text []rune, terms []string, own map[string]bool) []bool {
	mask := make([]bool, len(text))
	if len(terms) == 0 {
		return mask
	}
	lower := make([]rune, len(text))
	for i, r := range text {
		lower[i] = unicode.ToLower(r)
	}
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	// light marks the first n runes of the word that runs from i while in holds.
	light := func(i int, in func(rune) bool, bareTerms bool) {
		end := i
		for end < len(text) && in(text[end]) {
			end++
		}
		word := string(lower[i:end])
		if !own[word] {
			return
		}
		for _, term := range terms {
			if bareTerms && strings.IndexFunc(term, func(r rune) bool { return !isWord(r) }) >= 0 {
				continue
			}
			if strings.HasPrefix(word, term) {
				for j := i; j < i+len([]rune(term)); j++ {
					mask[j] = true
				}
			}
		}
	}
	for i := range text {
		if !unicode.IsSpace(text[i]) && (i == 0 || unicode.IsSpace(text[i-1])) {
			light(i, func(r rune) bool { return !unicode.IsSpace(r) }, false)
		}
		if isWord(text[i]) && (i == 0 || !isWord(text[i-1])) {
			light(i, isWord, true)
		}
	}
	return mask
}

// kindKey tells a ticket's id from a chart's name.
func kindKey(r queueRow) string {
	if r.chart {
		return "chart " + r.id
	}
	return "ticket " + r.id
}
