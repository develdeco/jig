package tui

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// The block under the steps lists the sub-steps of the step under the cursor,
// one line each, and opens the one that matters now: its line shows how far it
// has come (a track of its stages) and how long it has run, and under it are
// fields that say what it is (its goal, who holds it, what checks it, its
// commit; the brief's sections and slices; the pull request) and one live
// line, its holder's latest move and how long ago it was made. Every move is
// in the ticket's one activity log (log.go), tagged with its sub-step.

const (
	// goalRows is the most rows a slice's goal takes, and briefRows the most a
	// brief's sections take.
	goalRows  = 3
	briefRows = 3
	// fieldW is the width of a field's label.
	fieldW = 9
	// openMin is the fewest rows an open sub-step takes: its line, a field
	// and its live line.
	openMin = 3
)

// focusIndex is the sub-step the block opens: a person's turn or a failure
// first, else the work under way. -1 when none is, as in a step that is done
// or not reached, whose sub-steps all stay one line.
func focusIndex(subs []phase.Sub) int {
	best, bestW := -1, 2
	for i, s := range subs {
		if w := subWeight(s); w < bestW {
			best, bestW = i, w
		}
	}
	return best
}

// subTrack is what happened on one sub-step: its own lines, the session that
// holds it (the latest of them to name one), when it began and, once it is
// over, when it ended.
type subTrack struct {
	own        []events.Envelope
	holder     string
	start, end time.Time
	// borrowed is set when the holder is the ticket's latest session, not one
	// the sub-step's own lines name (openTrack).
	borrowed bool
}

// trackOf reads the sub-step key of step from the ticket's lines.
func (t *ticket) trackOf(step int, key string) subTrack {
	var tr subTrack
	for _, l := range t.stepLines(step) {
		if t.subKey(l) != key {
			continue
		}
		tr.own = append(tr.own, l)
		if l.Session != "" {
			tr.holder = l.Session
		}
		if at, ok := l.At(); ok {
			if tr.start.IsZero() {
				tr.start = at
			}
			tr.end = at
		}
	}
	return tr
}

// openTrack is trackOf for a sub-step under way. One that has written no line
// of its own yet (a pull request being opened once its body is written, a
// brief being drafted) is held by the session that wrote the ticket's latest
// line naming one, since the ticket's latest line: what that session has done
// since are its moves. A ticket whose journal names no session yet is held by
// the session the machine ledger ties to it (the one that minted it, for a
// brief), since its first line there.
func (t *ticket) openTrack(step int, key string) subTrack {
	tr := t.trackOf(step, key)
	if len(tr.own) > 0 {
		return tr
	}
	for _, l := range t.seen {
		if at, ok := l.At(); ok && at.After(tr.start) {
			tr.start = at
		}
	}
	for i := len(t.lines) - 1; i >= 0; i-- {
		l := t.lines[i]
		if at, ok := l.At(); ok && at.After(tr.start) {
			tr.start = at
		}
		if l.Session != "" {
			tr.holder, tr.borrowed = l.Session, true
			return tr
		}
	}
	for _, l := range t.ledger {
		if l.Session == "" {
			continue
		}
		tr.holder, tr.borrowed = l.Session, true
		if at, ok := l.At(); ok && tr.start.IsZero() {
			tr.start = at
		}
	}
	return tr
}

// moves are the track's lines and, between its first line and its end (now,
// while the sub-step is open), the calls its holder made, oldest first. A
// line that took the sub-step is left out: the sub-step's fields say who holds
// it, and its line since when.
func (t *ticket) moves(tr subTrack, open bool, now time.Time) []events.Envelope {
	var out []events.Envelope
	for _, l := range tr.own {
		if l.Event != journal.EventClaim && l.Event != journal.EventGateReviewer {
			out = append(out, l)
		}
	}
	if tr.holder == "" || tr.start.IsZero() {
		return out
	}
	end := tr.end
	if open {
		end = now
	}
	for _, l := range t.ledger {
		if !isMove(l) || l.Session != tr.holder {
			continue
		}
		at, ok := l.At()
		if !ok || !at.After(tr.start) || at.After(end) {
			continue
		}
		out = insertSorted(out, l)
	}
	return out
}

// isMove is whether a machine ledger line is a move of a session: a call that
// ran a command.
func isMove(l events.Envelope) bool {
	return l.Src == events.SrcLedger && l.Event == journal.EventTool && l.Command != ""
}

// shownCommand is a command as a move shows it: without the `cd <dir> &&` a
// harness puts before it to run it in a folder, nor the `2>&1` that sends its
// errors where its output goes, which say nothing of what ran.
func shownCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	for strings.HasPrefix(cmd, "cd ") {
		_, rest, ok := strings.Cut(cmd, "&&")
		if !ok {
			break
		}
		cmd = strings.TrimSpace(rest)
	}
	var words []string
	for _, w := range strings.Fields(cmd) {
		if w != "2>&1" {
			words = append(words, w)
		}
	}
	return strings.Join(words, " ")
}

// homePattern matches home in the spellings a command may give it: as the
// system writes it, with forward slashes, and as Git Bash names a drive
// (/c/Users/...), in any case, and only as a whole folder name (followed by a
// separator, a quote, a space or the end). Nil for no home.
func homePattern(home string) *regexp.Regexp {
	home = strings.TrimRight(home, `\/`)
	if home == "" {
		return nil
	}
	slash := strings.ReplaceAll(home, `\`, "/")
	forms := []string{regexp.QuoteMeta(home), regexp.QuoteMeta(slash)}
	if len(slash) > 2 && slash[1] == ':' {
		forms = append(forms, regexp.QuoteMeta("/"+slash[:1]+slash[2:]))
	}
	return regexp.MustCompile(`(?i)(` + strings.Join(forms, "|") + `)([\\/"'\s]|$)`)
}

// tildeHome is cmd with the home folder named ~.
func (m *Model) tildeHome(cmd string) string {
	if m.home == nil {
		return cmd
	}
	return m.home.ReplaceAllString(cmd, "~${2}")
}

// moveText is a move in words: what a journal line says, or what a session did
// in the words it gave the call (the command itself when it gave none), a
// failing command marked with its exit status.
func (m *Model) moveText(t *ticket, l events.Envelope) string {
	if l.Src != events.SrcLedger {
		return m.st.icon(eventState(l)) + " " + m.say(t, l)
	}
	text := "$ `" + m.tildeHome(shownCommand(l.Command)) + "`"
	if d := strings.TrimSpace(l.Description); d != "" {
		text = m.tildeHome(d)
	}
	if l.Exit != nil && *l.Exit != 0 {
		return m.st.state(StateStalled, "✗ "+text+" · exit "+strconv.Itoa(*l.Exit))
	}
	return m.st.dim("·") + " " + text
}

// subRole is the role a sub-step's holder plays in it, for naming the session.
func subRole(key string) string {
	switch kind, _, _ := strings.Cut(key, ":"); kind {
	case "slice":
		return roleBuilder
	case "round":
		return roleReviewer
	case "drafted", "slices", "approved":
		return rolePlanner
	}
	return ""
}

// holderWho names the session that holds a sub-step of role: as whoWithAs
// does, and by the role itself when the session has played none on the ticket
// yet (the planner of a brief nobody has approved).
func (m *Model) holderWho(t *ticket, session, role string) string {
	if role != "" && t.firstRole(session) == "" {
		if d := m.sessionDesc(session); d != "" {
			return role + " (" + d + ")"
		}
		return role
	}
	return m.whoWithAs(t, session, role)
}

// heldBy is who holds the sub-step: "you" when a person took it at a terminal,
// else its holder named by the role the sub-step gives it (a round's reviewer
// as the reviewer it is); "" when no line names one.
func (m *Model) heldBy(t *ticket, s phase.Sub, tr subTrack) string {
	for _, l := range tr.own {
		if l.Session == "" && l.Provenance == "person" {
			return "you"
		}
	}
	if tr.holder == "" {
		return ""
	}
	if kind, id, _ := strings.Cut(s.Key, ":"); kind == "round" {
		n, _ := strconv.Atoi(id)
		return firstOf(m.reviewerWho(t, tr.holder, n, "", nil), m.holderWho(t, tr.holder, roleReviewer))
	}
	return m.holderWho(t, tr.holder, subRole(s.Key))
}

// field is one field of an open sub-step: a label and the rows that say it,
// wrapped to the block.
type field struct {
	label string
	rows  []string
}

// fieldText is how many cells a field leaves its text: the block's inside, less
// the indent and the label.
func (m *Model) fieldText() int { return max(10, m.width-7-4-fieldW) }

// subFields are what an open sub-step shows above its live line, each when it
// is known: a slice's goal, holder, oracle and commit; a round's reviewer; the
// brief's planner, sections, slices and the approval it waits for; a publish's
// session, branch and pull request.
func (m *Model) subFields(t *ticket, s phase.Sub, tr subTrack) []field {
	var fs []field
	add := func(label string, rows ...string) {
		if len(rows) > 0 && rows[0] != "" {
			fs = append(fs, field{label, rows})
		}
	}
	tw := m.fieldText()
	who := m.heldBy(t, s, tr)
	kind, id, _ := strings.Cut(s.Key, ":")
	switch kind {
	case "slice":
		add("goal", wrap(goalText(t.goal(id)), tw, goalRows)...)
		add("held by", dotted(who))
		add("checked", t.checkedBy(id))
		add("commit", t.lastCommit(id))
	case "round":
		add("reviewer", dotted(who))
	case "drafted", "slices", "approved":
		add("planner", strings.TrimPrefix(strings.TrimPrefix(dotted(who), rolePlanner), " · "))
		add("sections", wrap(strings.Join(t.headings(), " · "), tw, briefRows)...)
		var ids []string
		for _, sl := range t.planned() {
			ids = append(ids, sl.ID)
		}
		add("slices", strings.Join(ids, ", "))
		if kind != "approved" {
			add("approval", "after the slices are written")
		}
	case "body", "approval", "pr", "changelog", "done":
		add("by", dotted(who))
		if pub := t.publishInfo(); pub != nil {
			add("branch", strings.Join(nonEmpty(pub.Branch, headWords(pub.Head)), " · "))
			add("pull req", pub.PR)
		}
	default:
		add("held by", dotted(who))
	}
	return fs
}

// dotted is a session named as a field says it, its harness and model after
// dots rather than in brackets: "builder · Claude Code · Sonnet 5".
func dotted(who string) string {
	name, desc, ok := strings.Cut(who, " (")
	if !ok {
		return who
	}
	return name + " · " + strings.TrimSuffix(desc, ")")
}

// nonEmpty are the strings of ss that are not empty.
func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// headWords is a head commit as a field says it.
func headWords(sha string) string {
	if sha == "" {
		return ""
	}
	return "head " + short(sha)
}

// publishInfo is what the status says of the publish, nil before it began.
func (t *ticket) publishInfo() *statusPublish {
	if t.status == nil {
		return nil
	}
	return t.status.Publish
}

// checkedBy is what checks slice id: the command of its latest oracle run, else
// the oracle slices.yaml names, with the attempt when it is not the first.
func (t *ticket) checkedBy(id string) string {
	oracle := ""
	for _, l := range t.lines {
		if l.Event == journal.EventOracle && l.Slice == id && l.Command != "" {
			oracle = l.Command
		}
	}
	attempts := 0
	if t.status != nil {
		for _, s := range t.status.Slices {
			if s.ID == id {
				oracle, attempts = firstOf(oracle, s.Oracle), s.Attempts
			}
		}
	}
	if oracle != "" && attempts > 1 {
		oracle += " · attempt " + strconv.Itoa(attempts)
	}
	return oracle
}

// lastCommit is the latest commit slice id put up for verification, with its
// subject.
func (t *ticket) lastCommit(id string) string {
	sha := ""
	for _, l := range t.lines {
		if l.Event == journal.EventVerifyPending && l.Slice == id && l.Commit != "" {
			sha = l.Commit
		}
	}
	return commitWords(t, sha)
}

// stagesOf are the stages the track of an open sub-step shows and how many of
// them it has reached: a slice is claimed, committed, verifying and verified; a
// round opened, taken, reviewing and closed; any other sub-step is a stage of
// its step's sub-steps, which are the track.
func (t *ticket) stagesOf(step int, s phase.Sub, subs []phase.Sub) ([]string, int) {
	kind, _, _ := strings.Cut(s.Key, ":")
	tr := t.trackOf(step, s.Key)
	has := func(ev string) bool {
		for _, l := range tr.own {
			if l.Event == ev {
				return true
			}
		}
		return false
	}
	switch kind {
	case "slice":
		reached := 0
		switch {
		case s.State == phase.SubDone:
			reached = 4
		case has(journal.EventVerifyPending):
			reached = 3
		case len(tr.own) > 0:
			reached = 1
		}
		return []string{"claimed", "committed", "verifying", "verified"}, reached
	case "round":
		reached := 0
		switch {
		case s.State == phase.SubDone:
			reached = 4
		case has(journal.EventGateReviewer):
			reached = 3
		case has(journal.EventGateOpen):
			reached = 1
		}
		return []string{"opened", "taken", "reviewing", "closed"}, reached
	}
	var names []string
	reached := 0
	for _, x := range subs {
		names = append(names, stageName(x))
		if x.State == phase.SubDone || x.Key == s.Key {
			reached = len(names)
		}
	}
	return names, reached
}

// stageName is a sub-step as a stage of its step's track: its key's word.
func stageName(s phase.Sub) string {
	kind, id, _ := strings.Cut(s.Key, ":")
	switch kind {
	case "pr":
		return strings.TrimSpace("PR " + id)
	case "done":
		return "published"
	case "round":
		return "round " + id
	case "slice":
		return id
	}
	return kind
}

// trackLine draws stages: the ones reached in state's color, joined by a heavy
// line, and the rest drawn back.
func (m *Model) trackLine(state string, stages []string, reached int) string {
	var b strings.Builder
	for i, name := range stages {
		if i > 0 {
			if i < reached {
				b.WriteString(m.st.state(state, " ━━ "))
			} else {
				b.WriteString(m.st.dim(" ── "))
			}
		}
		if i < reached {
			b.WriteString(m.st.state(state, name))
		} else {
			b.WriteString(m.st.dim(name))
		}
	}
	return b.String()
}

// openParts are what the open sub-step s shows: its track, its fields and its
// latest move (nil when it has made none).
func (m *Model) openParts(t *ticket, step int, s phase.Sub) (subTrack, []field, *events.Envelope) {
	tr := t.openTrack(step, s.Key)
	moves := t.moves(tr, true, m.now())
	var last *events.Envelope
	if len(moves) > 0 {
		last = &moves[len(moves)-1]
	}
	return tr, m.subFields(t, s, tr), last
}

// openWant is how many rows the open sub-step s would take: its line, its
// fields and its live line.
func (m *Model) openWant(t *ticket, step int, s phase.Sub) int {
	_, fs, _ := m.openParts(t, step, s)
	return 2 + fieldRows(fs)
}

// fieldRows is how many rows fields take.
func fieldRows(fs []field) int {
	n := 0
	for _, f := range fs {
		n += len(f.rows)
	}
	return n
}

// trimFields is fields in at most n rows: the last field gives up rows first,
// its last kept row ending in an ellipsis, and goes when it has one row left.
func trimFields(fs []field, n int) []field {
	for len(fs) > 0 && fieldRows(fs) > n {
		last := &fs[len(fs)-1]
		if len(last.rows) <= 1 {
			fs = fs[:len(fs)-1]
			continue
		}
		last.rows = last.rows[:len(last.rows)-1]
		last.rows[len(last.rows)-1] = strings.TrimRight(last.rows[len(last.rows)-1], " …") + " …"
	}
	return fs
}

// openLines are the open sub-step s in rows lines, the most it may take: its
// line (label, track, how long it has run), its fields, and its live line, the
// latest move with how long ago it was made. Fields give way when rows are few;
// the line and the live line stay.
func (m *Model) openLines(t *ticket, step int, s phase.Sub, subs []phase.Sub, labelW, rows int) []string {
	tr, fs, last := m.openParts(t, step, s)
	now := m.now()
	state := subState(step, s)
	w := max(20, m.width-7)

	right := ""
	if !tr.start.IsZero() && now.After(tr.start) {
		right = m.st.state(state, span(now.Sub(tr.start)))
	}
	head := m.st.icon(state) + " " + m.st.bold(pad(s.Label, labelW)) + "  "
	stages, reached := t.stagesOf(step, s, subs)
	track := m.trackLine(state, stages, reached)
	if width(head)+width(track)+width(right)+2 > w {
		track = ""
	}
	gap := max(1, w-width(head)-width(track)-width(right))
	out := []string{head + track + spaces(gap) + right}

	row := func(label, text string) string {
		return "    " + m.st.dim(pad(label, fieldW)) + text
	}
	for _, f := range trimFields(fs, max(0, rows-2)) {
		for i, r := range f.rows {
			label := ""
			if i == 0 {
				label = f.label
			}
			out = append(out, row(label, r))
		}
	}
	live := m.st.dim("nothing yet")
	if last != nil {
		if at, ok := last.At(); ok {
			live = m.st.state(state, pad(span(max(0, now.Sub(at))), 7)) + " " + m.moveText(t, *last)
		}
	}
	return append(out, row("now", live))
}

// subSummary is what a folded sub-step that is done adds to its line: how long
// it took from its first line to its last, when that is known.
func (t *ticket) subSummary(step int, s phase.Sub) string {
	if s.State != phase.SubDone {
		return ""
	}
	tr := t.trackOf(step, s.Key)
	if tr.start.IsZero() || !tr.end.After(tr.start) {
		return ""
	}
	return "took " + span(tr.end.Sub(tr.start))
}
