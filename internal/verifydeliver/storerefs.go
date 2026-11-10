package verifydeliver

// A store reference is a line a change adds that names something only the
// store holds: a ticket id or alias, a finding id of the ticket's own earlier
// rounds, or the store's own path. The code then points at a record a reader of
// the repo cannot open. The gate notes each one with its file and line and
// never refuses over it: it is a fact of the diff, computed from the lines the
// whole change adds, and it clears itself when the line is gone.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// StoreRefPath is the id recorded for a hit on the store's own path: the path
// itself stays out of the record, since the store is shared by machines that
// each spell it differently.
const StoreRefPath = "<store path>"

// StoreRef is one added line that names something of the store: the repo and
// the file (repo-relative, slash form) and line it was added at, and what it
// names.
type StoreRef struct {
	Repo string `yaml:"repo" json:"repo"`
	File string `yaml:"file" json:"file"`
	Line int    `yaml:"line" json:"line"`
	ID   string `yaml:"id" json:"id"`
}

// storeRefRepo is one repo of a round and the range its whole change spans:
// the base a full round uses, whatever this round's scope, and the head.
type storeRefRepo struct {
	Name, Dir, Base, Head string
}

// storeRefMatcher finds the store's own names in a line of text.
type storeRefMatcher struct {
	ids   []string
	paths []string
}

// newStoreRefMatcher reads what the store holds for ticket's round: every
// ticket id and alias, the ids of the findings the ticket's earlier rounds
// reported, and the store's own root path. Another ticket's finding ids are not
// held: they are not names a reader of this change could mistake for the
// ticket's own.
func newStoreRefMatcher(st *store.Store, ticket string, round int) (storeRefMatcher, error) {
	seen := map[string]bool{}
	var m storeRefMatcher
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			m.ids = append(m.ids, id)
		}
	}
	tickets, err := st.Tickets()
	if err != nil {
		return m, fmt.Errorf("verifydeliver: store references: list tickets: %w", err)
	}
	for _, id := range tickets {
		add(id)
		// A record that cannot be read costs its own aliases and nothing else.
		if rec, err := st.ReadTicket(id); err == nil {
			for _, alias := range rec.Aliases {
				add(alias)
			}
		}
	}
	for r := 1; r < round; r++ {
		ff, ok, err := readFindingsYAML(st, ticket, r)
		if err != nil {
			return m, fmt.Errorf("verifydeliver: store references: read round %d findings.yaml: %w", r, err)
		}
		if !ok {
			continue
		}
		for _, f := range ff.Findings {
			add(f.ID)
		}
		for _, id := range ff.Cleared {
			add(id)
		}
	}
	sort.Strings(m.ids)
	m.paths = storePathSpellings(st.Root)
	return m, nil
}

// storePathSpellings is the store's root in the slash form a line is matched
// in, as given, absolute and with its links resolved. A filesystem root is a
// prefix of every path and so names nothing.
func storePathSpellings(root string) []string {
	if root == "" {
		return nil
	}
	var out []string
	add := func(p string) {
		p = slashed(p)
		if p == "" || p == "/" || len(p) < 4 {
			return
		}
		for _, have := range out {
			if have == p {
				return
			}
		}
		out = append(out, p)
	}
	abs := absPath(root)
	add(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		add(resolved)
	}
	return out
}

// slashed spells a path or a line in the slash form: a Go string literal's
// doubled backslash and a single one both become a slash.
func slashed(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\\`, "/"), `\`, "/")
}

// isIDChar is a character an id runs on in: a letter, a digit, a hyphen or an
// underscore. An id flanked by one is part of a longer id or a word.
func isIDChar(b byte) bool {
	return b == '-' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}

// find returns the store's names text holds, in the order they appear: an id
// as a whole token, case-sensitively, and the store's path as it is spelled.
func (m storeRefMatcher) find(text string) []string {
	type hit struct {
		at int
		id string
	}
	var hits []hit
	for _, id := range m.ids {
		for from := 0; ; {
			i := strings.Index(text[from:], id)
			if i < 0 {
				break
			}
			at := from + i
			end := at + len(id)
			from = at + 1
			if at > 0 && isIDChar(text[at-1]) || end < len(text) && isIDChar(text[end]) {
				continue
			}
			hits = append(hits, hit{at, id})
			break
		}
	}
	if len(m.paths) > 0 {
		line := slashed(text)
		for _, p := range m.paths {
			if at := strings.Index(line, p); at >= 0 {
				hits = append(hits, hit{at, StoreRefPath})
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var out []string
	for _, h := range hits {
		out = append(out, h.id)
	}
	return out
}

// storeRefsOf is every store reference in the lines repos' whole changes add,
// in diff order, repo by repo. It reads the store only when some repo adds a
// line at all.
func storeRefsOf(st *store.Store, ticket string, round int, repos []storeRefRepo) ([]StoreRef, error) {
	type repoLines struct {
		name  string
		lines []gitx.AddedLine
	}
	var all []repoLines
	for _, r := range repos {
		lines, err := gitx.DiffAdded(r.Dir, r.Base, r.Head)
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: store references: %s: %w", r.Name, err)
		}
		if len(lines) > 0 {
			all = append(all, repoLines{r.Name, lines})
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	m, err := newStoreRefMatcher(st, ticket, round)
	if err != nil {
		return nil, err
	}
	var refs []StoreRef
	for _, rl := range all {
		for _, l := range rl.lines {
			for _, id := range m.find(l.Text) {
				refs = append(refs, StoreRef{Repo: rl.name, File: l.Path, Line: l.Line, ID: id})
			}
		}
	}
	return refs, nil
}

// StoreRefsLimit is how many store references a table lists before it sums the
// rest up: report.yaml keeps every one.
const StoreRefsLimit = 20

// StoreRefsRows is refs as the rows a table lists (repo, file:line, id): at
// most StoreRefsLimit, then, when more remain, a last row StoreRefsMore says in
// the repo column.
func StoreRefsRows(refs []StoreRef) (rows [][]string) {
	for i, r := range refs {
		if i == StoreRefsLimit {
			break
		}
		rows = append(rows, []string{r.Repo, fmt.Sprintf("%s:%d", r.File, r.Line), r.ID})
	}
	if len(refs) > StoreRefsLimit {
		rows = append(rows, []string{StoreRefsMore(refs[StoreRefsLimit:]), "", ""})
	}
	return rows
}

// StoreRefsMore sums up the references a table leaves out: how many, and in how
// many files (counted by repo and path).
func StoreRefsMore(rest []StoreRef) string {
	files := map[[2]string]bool{}
	for _, r := range rest {
		files[[2]string{r.Repo, r.File}] = true
	}
	return fmt.Sprintf("and %d more in %d files", len(rest), len(files))
}

// storeRefsNotes is the review notes' section on the store references of the
// round the pull request ships: how many lines the change adds that name
// something only the store holds, and where, for whoever merges to weigh. It
// names the places and not what they cite, since the notes are public.
func storeRefsNotes(refs []StoreRef) string {
	// A line that names two things is one line to look at.
	var places []string
	seen := map[string]bool{}
	for _, r := range refs {
		place := fmt.Sprintf("%s:%d", r.File, r.Line)
		if !seen[place] {
			seen[place] = true
			places = append(places, place)
		}
	}
	var b strings.Builder
	noun, verb := "lines", "name"
	if len(places) == 1 {
		noun, verb = "line", "names"
	}
	fmt.Fprintf(&b, "## Store references\n\nThe change adds %d %s that %s something only the jig store holds:\n\n", len(places), noun, verb)
	for _, p := range places {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	b.WriteString("\n")
	return b.String()
}
