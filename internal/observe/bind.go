package observe

import (
	"os"

	"gopkg.in/yaml.v3"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
)

// A tool call is written on the ticket it belongs to, by the first of:
//
//  1. the ticket the call names: a `jig` verb's ticket word (`jig status T`),
//     as a claim call does, or a path under the store's tickets/<T>/ folder
//     (a drafter's write of the brief).
//  2. the ticket whose role the call's own agent holds: a claim until its
//     release, its ticket's publish or its agent's stop, a review round from `jig review context` to `jig review done`. A
//     harness reports the session's directory for a subagent that works in a
//     worktree of its own, so the directory says nothing of what the agent is
//     on; its role does.
//  3. the ticket of the worktree the command ran in: the directory a leading
//     `cd <dir> &&` names, else the event's directory, through the branch
//     checked out there.
//  4. none.
//
// Nothing carries a ticket forward from one call to the next: a claim binds
// only while it is held (a role, while its agent holds it).

// claimCall is a call of the event's agent that took a role (a `jig claim`, by default), read back from the
// ledger: what the claim names, and whether the agent has stopped since.
type claimCall struct {
	// word is the ticket the command names, ticket the one the line was
	// written on, slice the slice the command names; each may be empty.
	word, ticket, slice string
	// kind is the role the call took: a claim of a slice (the zero value), a
	// review round opened.
	kind string
	// ended is true when the agent stopped after the call, or, for a review
	// round, when it closed the round.
	ended bool
}

// The roles besides a claim that an agent holds on a ticket.
const (
	roleReview = "review"
)

// claimCallOf reads a tool line as the claim of a slice, false for any other
// line, and for a release.
func claimCallOf(l journal.Line) (claimCall, bool) {
	if l.Event != journal.EventTool || !madeClaim(l.Command) {
		return claimCall{}, false
	}
	return claimCall{word: claimWord(l.Command), ticket: l.Ticket, slice: claimSlice(l.Command)}, true
}

// jigTicketWord is the first word of a jig command line, after the verb, that
// is a ticket id, "" when there is none. A word is only looked up in the
// store when it is shaped like an id.
func (h *handler) jigTicketWord(cmd string) string {
	words := strings.Fields(cmd)
	for i, w := range words {
		if !isJig(w) {
			continue
		}
		for _, a := range words[i+1:] {
			switch a {
			case "&&", "||", ";", "|":
				return ""
			}
			a = strings.Trim(a, `"'`)
			if strings.HasPrefix(a, "-") || !idShaped(a) {
				continue
			}
			if t := h.ticketNamed(a); t != "" && h.st.TicketExists(t) {
				return t
			}
		}
		return ""
	}
	return ""
}

// idShaped reports whether s reads like a ticket id: letters, a dash, digits.
func idShaped(s string) bool {
	i := strings.IndexByte(s, '-')
	if i <= 0 || i == len(s)-1 {
		return false
	}
	for _, c := range s[i+1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// pathTicket is the ticket a path in the call names: one under the store's
// tickets/<T>/ folder, among the files the tool touched or the words of the
// command. A drafter writes the brief and the slices there, and nothing else
// of its work names the ticket.
func (h *handler) pathTicket() string {
	if h.st == nil || h.st.Root == "" {
		return ""
	}
	prefix := strings.ToLower(strings.TrimRight(filepath.ToSlash(h.st.Root), "/")) + "/tickets/"
	paths := append([]string(nil), h.ev.Files...)
	for _, w := range strings.Fields(h.ev.Command) {
		paths = append(paths, strings.Trim(w, `"'`))
	}
	for _, p := range paths {
		rest, ok := strings.CutPrefix(strings.ToLower(filepath.ToSlash(p)), prefix)
		if !ok {
			continue
		}
		id, _, _ := strings.Cut(rest, "/")
		if t := h.ticketNamed(strings.ToUpper(id)); t != "" && h.st.TicketExists(t) {
			return t
		}
	}
	return ""
}

// roleCallOf reads a tool line as the opening of a review round, and whether it
// is one. A session touch starts no role: a verb that ties a session to a
// ticket says nothing of what the session does next.
func roleCallOf(l journal.Line) (claimCall, bool) {
	if l.Event != journal.EventTool {
		return claimCall{}, false
	}
	switch {
	case ledger.RunsJig(l.Command, "", "review", "context"):
		return claimCall{kind: roleReview, word: reviewWord(l.Command)}, true
	}
	return claimCall{}, false
}

// reviewWord is the ticket word of a `jig review context` or `jig review
// done` command line.
func reviewWord(cmd string) string {
	words := strings.Fields(cmd)
	for i, w := range words {
		if !isJig(w) || i+3 >= len(words) || words[i+1] != "review" {
			continue
		}
		for _, a := range words[i+3:] {
			switch a {
			case "&&", "||", ";", "|":
				return ""
			}
			if a = strings.Trim(a, `"'`); !strings.HasPrefix(a, "-") && idShaped(a) {
				return a
			}
		}
	}
	return ""
}

// madeClaim reports whether the shell command line claims a slice: a
// `jig claim` that does not release.
func madeClaim(cmd string) bool {
	return ledger.RunsJig(cmd, "", "claim") && !strings.Contains(cmd, "--release")
}

// claimWord is the ticket word of a `jig claim` command line, "" when it
// names none (the claim then takes the ticket from the branch it runs on).
func claimWord(cmd string) string {
	words := strings.Fields(cmd)
	for i, w := range words {
		if !isJig(w) || i+1 >= len(words) || words[i+1] != "claim" {
			continue
		}
		for j := i + 2; j < len(words); j++ {
			a := words[j]
			switch a {
			case "&&", "||", ";", "|":
				return ""
			}
			if strings.HasPrefix(a, "-") {
				// A flag that takes its value as the next word.
				if !strings.Contains(a, "=") && flagTakesValue(a) {
					j++
				}
				continue
			}
			if a = strings.Trim(a, `"'`); idShaped(a) || locate.RefShaped(a) {
				return a
			}
		}
	}
	return ""
}

// flagTakesValue reports whether the claim flag a is followed by a value.
func flagTakesValue(a string) bool {
	switch strings.TrimLeft(a, "-") {
	case "slice", "session", "store", "project":
		return true
	}
	return false
}

// claimSlice is the slice a `jig claim` command line names, "" when none.
func claimSlice(cmd string) string {
	words := strings.Fields(cmd)
	for i := 0; i < len(words); i++ {
		flag, value, hasValue := strings.Cut(words[i], "=")
		if flag != "--slice" && flag != "-slice" {
			continue
		}
		if !hasValue && i+1 < len(words) {
			value = words[i+1]
		}
		return strings.Trim(value, `"'`)
	}
	return ""
}

func isJig(word string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.Trim(word, `"'`), `\`, "/")))
	return base == "jig" || base == "jig.exe"
}

// bindTool is the ticket and slice a tool call of the event's agent is
// written on.
func (h *handler) bindTool() (ticket, slice string) {
	ticket, slice = h.bindTicket()
	if ticket != "" && ticket == h.w.Ticket {
		// The slice the located place says, as the session's lines have always
		// carried it.
		return ticket, h.w.Slice
	}
	return ticket, slice
}

func (h *handler) bindTicket() (ticket, slice string) {
	// A jig call binds by the ticket it names. The claim's own line is not in
	// the ledger yet when the hook asks, nor is a release's or a review's.
	if ledger.RunsJig(h.ev.Command, "", "claim") {
		if t := h.ticketNamed(claimWord(h.ev.Command)); t != "" {
			return t, claimSlice(h.ev.Command)
		}
	}
	if t := h.jigTicketWord(h.ev.Command); t != "" {
		return t, h.heldSlice(t)
	}
	if t := h.pathTicket(); t != "" {
		return t, h.heldSlice(t)
	}
	if t, s := h.claimBinding(false); t != "" {
		return t, s
	}
	return h.worktreeTicket(), ""
}

// heldSlice is the slice the event's agent holds on ticket, "" when it holds
// none there: a call that names its own ticket is under its slice.
func (h *handler) heldSlice(ticket string) string {
	if t, s := h.claimBinding(false); t == ticket {
		return s
	}
	return ""
}

// ticketNamed is the ticket a word of a command names (its id, or an id it
// carried before), "" when the store knows none.
func (h *handler) ticketNamed(word string) string {
	if word == "" || h.st == nil {
		return ""
	}
	if h.st.TicketExists(word) {
		return word // its folder says so: no ticket list is read
	}
	if locate.RefShaped(word) {
		if it, err := locate.ResolveItem(h.st, h.cfg, word); err == nil && it != nil && it.Ticket != "" {
			return it.Ticket
		}
		return ""
	}
	id, err := h.st.ResolveTicket(word) // an alias, found by a walk
	if err != nil || !h.st.TicketExists(id) {
		return ""
	}
	return id
}

// claimBinding is the ticket and slice the event's agent holds: the newest
// claim it made that its ticket's journal still holds for the session. A
// subagent's stop ends its claims for the calls after it, and not for the
// stops themselves (afterStop), which place the agent. One read of a
// ticket's journal for each ticket the agent claimed, and none for an agent
// that made no claim.
func (h *handler) claimBinding(afterStop bool) (ticket, slice string) {
	session := h.session()
	if session == "" || h.st == nil {
		return "", ""
	}
	mem := h.memory()
	if afterStop {
		// A stop is placed by the agent's latest claim whether or not it has
		// ended: an ended claim stops binding calls, never where the agent's
		// spend goes.
		for _, c := range mem.claims {
			t := h.ticketNamed(c.word)
			if t == "" {
				t = c.ticket
			}
			if t != "" {
				return t, c.slice
			}
		}
		return "", ""
	}
	read := map[string]bool{}
	for _, c := range mem.claims {
		if c.ended {
			continue
		}
		t := h.ticketNamed(c.word)
		if t == "" {
			t = c.ticket
		}
		if t == "" || read[t+"\x00"+c.slice] {
			continue
		}
		switch c.kind {
		case roleReview:
			return t, ""
		}
		read[t+"\x00"+c.slice] = true
		if s, held := h.holds(t, c.slice); held {
			return t, s
		}
	}
	return "", ""
}

// ticketJournal is the journal of a ticket, read once for the event.
func (h *handler) ticketJournal(ticket string) ([]journal.Line, error) {
	if lines, ok := h.jcache[ticket]; ok {
		return lines, nil
	}
	lines, err := journal.Read(h.st, ticket)
	if err != nil {
		return nil, err
	}
	if h.jcache == nil {
		h.jcache = map[string][]journal.Line{}
	}
	h.jcache[ticket] = lines
	return lines, nil
}

// holds reports whether the session holds a claim on ticket that the event's
// agent made: the slice it names, or when it names none, any claim the ledger
// says the agent made. It returns the slice held.
func (h *handler) holds(ticket, slice string) (string, bool) {
	lines, err := h.ticketJournal(ticket)
	if err != nil {
		h.fail(err)
		return "", false
	}
	session := h.session()
	var held []*journal.Line // the session's claims not released since, oldest first
	for i, l := range lines {
		if l.Session != session && l.Event != journal.EventPr {
			continue
		}
		switch l.Event {
		case journal.EventClaim:
			held = append(held, &lines[i])
		case journal.EventRelease:
			held = deleteSlice(held, l.Slice)
		case journal.EventPr:
			// A ticket's publish ends every claim on it: where a session's
			// hand-built work ends, since the build flow never releases a
			// green slice. A verification ends nothing: the builder's calls
			// before its next claim are still its slice's.
			held = nil
		}
	}
	for i := len(held) - 1; i >= 0; i-- {
		if slice != "" && held[i].Slice == slice {
			return slice, true
		}
		if slice == "" {
			if holder, known := h.claimHolderOn(ticket, held[i]); known && holder == h.ev.Agent {
				return held[i].Slice, true
			}
		}
	}
	return "", false
}

func deleteSlice(held []*journal.Line, slice string) []*journal.Line {
	out := held[:0]
	for _, c := range held {
		if c.Slice != slice {
			out = append(out, c)
		}
	}
	return out
}

// worktreeTicket is the ticket of the worktree the command ran in: the
// directory a leading cd names, else the event's directory (the located
// place). A cd that cannot be read, or that is to a directory on no ticket,
// binds none.
func (h *handler) worktreeTicket() string {
	dir, cd := leadingCd(h.ev.Command)
	if !cd {
		return h.w.Ticket
	}
	if dir == "" || h.st == nil {
		return ""
	}
	return h.ticketAt(resolveDir(dir, h.ev.Cwd, h.d.Cwd))
}

// resolveDir is the directory a cd names, from the directory the command ran
// in (else the fallback).
func resolveDir(dir, from, fallback string) string {
	if from == "" {
		from = fallback
	}
	dir = normalDir(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(from, dir)
	}
	return dir
}

// ticketAt is the ticket the branch checked out in dir binds, "" for none.
func (h *handler) ticketAt(dir string) string {
	branch, ok := gitx.HeadBranch(dir)
	if !ok || branch == "" || h.st == nil {
		return ""
	}
	if lead := store.BranchLeadID(branch); lead != "" && h.st.TicketExists(lead) {
		return lead // its folder says so: no ticket list is read
	}
	if lead := store.BranchLeadIssue(branch); lead != "" {
		return h.trackerOwner(lead)
	}
	id, _, err := h.st.TicketForBranch(branch)
	if err != nil {
		return ""
	}
	return id
}

// trackerOwner is the ticket the one tracker record of issue number n names,
// read from its own file: TicketForBranch walks every ticket and tracker file
// to find it. A store with no GitHub tracker, a number with no record, and a
// record that names no ticket the store holds are none.
func (h *handler) trackerOwner(n string) string {
	if h.cfg.GitHub == nil {
		return ""
	}
	entry := store.TrackerEntry{Provider: store.ProviderGitHub, Scope: strings.ToLower(h.cfg.GitHub.Repo), ID: n}
	data, err := os.ReadFile(h.st.TrackerRecordPath(entry))
	if err != nil {
		return ""
	}
	var rec struct {
		Ticket string `yaml:"ticket"`
	}
	if yaml.Unmarshal(data, &rec) != nil || rec.Ticket == "" || !h.st.TicketExists(rec.Ticket) {
		return ""
	}
	return rec.Ticket
}

// leadingCd is the directory a command line that starts with `cd <dir> &&`
// moves to, and whether it does. A directory that is not one plain path (a
// variable, a substitution, a subshell, a home the shell expands) is "":
// shown on no ticket, never guessed. A shell expands ~ only at a word's start,
// so one inside a path (a Windows short name such as RUNNER~1) is plain.
func leadingCd(cmd string) (dir string, cd bool) {
	s := strings.TrimSpace(cmd)
	rest, ok := strings.CutPrefix(s, "cd ")
	if !ok {
		return "", false
	}
	i := strings.Index(rest, "&&")
	if i < 0 {
		return "", false
	}
	dir = strings.TrimSpace(rest[:i])
	if len(dir) >= 2 && (dir[0] == '"' || dir[0] == '\'') && dir[len(dir)-1] == dir[0] {
		dir = dir[1 : len(dir)-1]
	}
	if dir == "" || strings.HasPrefix(dir, "~") || strings.ContainsAny(dir, "$`;|()<>*?\"'") {
		return "", true
	}
	return dir, true
}

// normalDir reads a path the way a git bash shell on Windows spells it
// (/c/work) as the drive path it names.
func normalDir(dir string) string {
	if runtime.GOOS != "windows" {
		return dir
	}
	if len(dir) >= 3 && dir[0] == '/' && dir[2] == '/' && isLetter(dir[1]) {
		return strings.ToUpper(dir[1:2]) + ":" + dir[2:]
	}
	return dir
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
