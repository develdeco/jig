package tui

import (
	"context"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/develdeco/jig/internal/events"
)

// Source is the event stream the screens are built on: the ticket journals
// and the machine ledger as `jig events` streams them. Follow emits the lines
// there are, oldest first, then each line as it is written, until ctx ends
// (it then returns nil) or emit returns an error (which it returns).
// events.Follow with a Scope is the one the binary uses.
type Source interface {
	Follow(ctx context.Context, emit func(events.Envelope) error) error
}

// Config is what a model is built from.
type Config struct {
	// Source is the event stream. Nil shows only what the verbs say.
	Source Source
	// Run runs the verbs the screens read and act through. Nil reads and
	// acts on nothing.
	Run Runner
	// Context ends the stream when it is done. Nil is never.
	Context context.Context
	// Ticket, when set, opens that ticket's screen first, with the project's
	// list one step back.
	Ticket string
	// Scope is the flags that keep every verb on the store the screens were
	// opened on (`--store <path>`); they are added to each command.
	Scope []string
	// Project is the project's name, for the header.
	Project string
	// Renderer paints the screens. Nil draws without color.
	Renderer *lipgloss.Renderer
	// Zone is the time zone the screens show times in. Nil is the local one.
	Zone *time.Location
	// Width and Height are the screen's size until the terminal reports one;
	// zero is 100 by 30.
	Width, Height int
}

const (
	defaultWidth  = 100
	defaultHeight = 30
	// batchMax bounds how many lines of the stream one update takes.
	batchMax = 512
)

// screenKind is which of the five screens is showing.
type screenKind int

const (
	screenMain   screenKind = iota // the project: its tickets
	screenTicket                   // one ticket: build, review, publish, what needs a person
	screenBuild                    // a ticket's slices
	screenReview                   // a ticket's gate rounds, its asks and its publish
	screenSlice                    // one slice: its questions and its timeline
)

func (s screenKind) String() string {
	switch s {
	case screenMain:
		return "main"
	case screenTicket:
		return "ticket"
	case screenBuild:
		return "build"
	case screenReview:
		return "review"
	case screenSlice:
		return "slice"
	}
	return "?"
}

// navEntry is a screen on the way in: which one, about what, and where the
// cursor is on it.
type navEntry struct {
	screen screenKind
	ticket string
	slice  string
	sel    int
}

// textInput is a line a person is typing: an answer.
type textInput struct {
	prompt string
	about  []string
	value  []rune
	ticket string
	qid    string
}

// confirmation is an action that waits for a yes.
type confirmation struct {
	prompt string
	run    func() tea.Cmd
}

// Model is the TUI: five screens over the event stream and the verbs. It is a
// bubbletea model, and nothing else in the package needs the terminal: its
// View is plain text a test compares.
type Model struct {
	cfg  Config
	st   styles
	run  Runner
	zone *time.Location

	w             *world
	width, height int
	nav           []navEntry

	input  *textInput
	ask    *confirmation
	last   *outcome
	notice string
	tail   bool

	ctx        context.Context
	cancel     context.CancelFunc
	stream     chan events.Envelope
	streamErr  chan error
	rosterBusy bool
	quitting   bool
}

// New builds the model for cfg.
func New(cfg Config) *Model {
	m := &Model{cfg: cfg, w: newWorld(), width: cfg.Width, height: cfg.Height}
	if m.width <= 0 {
		m.width = defaultWidth
	}
	if m.height <= 0 {
		m.height = defaultHeight
	}
	m.zone = cfg.Zone
	if m.zone == nil {
		m.zone = time.Local
	}
	r := cfg.Renderer
	if r == nil {
		r = lipgloss.NewRenderer(io.Discard)
	}
	m.st = styles{r: r}
	if cfg.Run != nil {
		m.run = serial(cfg.Run)
	}
	m.nav = []navEntry{{screen: screenMain}}
	if cfg.Ticket != "" {
		m.w.ticketFor(cfg.Ticket)
		m.nav = append(m.nav, navEntry{screen: screenTicket, ticket: cfg.Ticket})
	}
	return m
}

// Init starts reading the stream, the ticket list and, when a ticket opens
// first, its status.
func (m *Model) Init() tea.Cmd {
	parent := m.cfg.Context
	if parent == nil {
		parent = context.Background()
	}
	m.ctx, m.cancel = context.WithCancel(parent)
	cmds := []tea.Cmd{m.startStream(), m.loadRoster()}
	if id := m.watching(); id != "" {
		cmds = append(cmds, m.refreshStatus(id))
	}
	return tea.Batch(cmds...)
}

// Stop ends the stream. The program that runs the model calls it when it is
// done; quitting from a key does it too.
func (m *Model) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// Screen reports which screen is showing, for tests and callers that care.
func (m *Model) Screen() string { return m.top().screen.String() }

func (m *Model) top() *navEntry { return &m.nav[len(m.nav)-1] }

// watching is the ticket whose status the screen needs, "" on the main screen.
func (m *Model) watching() string {
	if t := m.top(); t.screen != screenMain {
		return t.ticket
	}
	return ""
}

// The messages the stream reports with.
type (
	eventsMsg    []events.Envelope
	streamEndMsg struct{ err error }
)

func (m *Model) startStream() tea.Cmd {
	if m.cfg.Source == nil {
		return nil
	}
	ch := make(chan events.Envelope, 256)
	errc := make(chan error, 1)
	m.stream, m.streamErr = ch, errc
	ctx, src := m.ctx, m.cfg.Source
	go func() {
		err := src.Follow(ctx, func(e events.Envelope) error {
			select {
			case ch <- e:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		errc <- err
		close(ch)
	}()
	return m.waitEvents()
}

// waitEvents blocks for the next lines of the stream and takes what else is
// already there with them, so a long history is a few updates and not one
// per line.
func (m *Model) waitEvents() tea.Cmd {
	ch, errc := m.stream, m.streamErr
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		first, ok := <-ch
		if !ok {
			return streamEndMsg{err: <-errc}
		}
		batch := []events.Envelope{first}
		for len(batch) < batchMax {
			select {
			case e, ok := <-ch:
				if !ok {
					return eventsMsg(batch)
				}
				batch = append(batch, e)
			default:
				return eventsMsg(batch)
			}
		}
		return eventsMsg(batch)
	}
}

// Update takes a message in.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return m, m.keys(msg)
	case eventsMsg:
		return m, tea.Batch(m.onEvents(msg), m.waitEvents())
	case streamEndMsg:
		if msg.err != nil && !m.quitting {
			m.notice = "the event stream ended: " + msg.err.Error()
		}
	case rosterMsg:
		m.rosterBusy = false
		if msg.err != nil {
			m.notice = "tickets: " + msg.err.Error()
		} else {
			m.w.setRoster(msg.rows)
		}
	case statusMsg:
		return m, m.onStatus(msg)
	case ranMsg:
		return m, m.onRan(msg)
	}
	return m, nil
}

func (m *Model) onEvents(envs []events.Envelope) tea.Cmd {
	touched, fresh := m.w.apply(envs)
	for id := range touched {
		m.w.tickets[id].dirty = true
	}
	var cmds []tea.Cmd
	if fresh {
		cmds = append(cmds, m.loadRoster())
	}
	if id := m.watching(); id != "" && touched[id] {
		cmds = append(cmds, m.refreshStatus(id))
	}
	return tea.Batch(cmds...)
}

func (m *Model) onStatus(msg statusMsg) tea.Cmd {
	t := m.w.tickets[msg.ticket]
	if t == nil {
		return nil
	}
	t.loading = false
	switch {
	case msg.info.Err != nil:
		m.notice = "status " + msg.ticket + ": " + msg.info.Err.Msg
	default:
		t.status = msg.info
	}
	if t.dirty && m.watching() == msg.ticket {
		return m.refreshStatus(msg.ticket)
	}
	return nil
}

func (m *Model) onRan(msg ranMsg) tea.Cmd {
	m.last = &msg.result
	if id := msg.result.Ticket; id != "" && m.w.tickets[id] != nil {
		m.w.tickets[id].dirty = true
		if m.watching() == id {
			return m.refreshStatus(id)
		}
	}
	return nil
}

// quit ends the stream and the program.
func (m *Model) quit() tea.Cmd {
	m.quitting = true
	m.Stop()
	return tea.Quit
}

// keys takes in what a read of the keyboard brought. Several printable keys
// that arrive together (a script writing "ay", a slow terminal) are pressed
// one at a time, in order; a paste stays one piece of text.
func (m *Model) keys(k tea.KeyMsg) tea.Cmd {
	if k.Type != tea.KeyRunes || len(k.Runes) < 2 || k.Paste {
		return m.key(k)
	}
	var cmds []tea.Cmd
	for _, r := range k.Runes {
		cmds = append(cmds, m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}))
	}
	return tea.Batch(cmds...)
}

// key takes a key press in.
func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	if k.Type == tea.KeyCtrlC {
		return m.quit()
	}
	if m.input != nil {
		return m.inputKey(k)
	}
	if m.ask != nil {
		return m.confirmKey(k)
	}
	m.notice = ""
	switch k.String() {
	case "q":
		return m.quit()
	case "up":
		m.move(-1)
	case "down":
		m.move(1)
	case "pgup":
		m.move(-5)
	case "pgdown":
		m.move(5)
	case "home":
		m.move(-1 << 20)
	case "end":
		m.move(1 << 20)
	case "enter", "right":
		return m.open()
	case "esc", "left", "backspace":
		m.back()
	case "a":
		m.beginAnswer()
	case "k":
		return m.triage("keep")
	case "d":
		return m.triage("dismiss")
	case "t":
		m.tail = !m.tail
	case "g":
		return m.gate()
	case "p":
		m.beginPublish()
	case "r":
		return m.refreshAll()
	}
	return nil
}

// move puts the cursor delta rows on, within the screen's rows.
func (m *Model) move(delta int) {
	n := len(m.page().refs())
	e := m.top()
	e.sel += delta
	if e.sel >= n {
		e.sel = n - 1
	}
	if e.sel < 0 {
		e.sel = 0
	}
}

// selected is the row under the cursor, nil when the screen has none.
func (m *Model) selected() *rowRef {
	refs := m.page().refs()
	sel := m.top().sel
	if sel < 0 || sel >= len(refs) {
		return nil
	}
	return &refs[sel]
}

// open goes into the row under the cursor.
func (m *Model) open() tea.Cmd {
	ref := m.selected()
	if ref == nil {
		return nil
	}
	var next navEntry
	switch ref.kind {
	case rowTicket:
		next = navEntry{screen: screenTicket, ticket: ref.id}
	case rowBuild:
		next = navEntry{screen: screenBuild, ticket: ref.ticket}
	case rowReview, rowPublish, rowAsk:
		next = navEntry{screen: screenReview, ticket: ref.ticket}
	case rowSlice:
		next = navEntry{screen: screenSlice, ticket: ref.ticket, slice: ref.id}
	case rowQuestion:
		if ref.slice == "" {
			return nil
		}
		next = navEntry{screen: screenSlice, ticket: ref.ticket, slice: ref.slice}
	default:
		return nil
	}
	m.nav = append(m.nav, next)
	m.last = nil
	if t := m.w.tickets[next.ticket]; t != nil && (t.status == nil || t.dirty) {
		return m.refreshStatus(next.ticket)
	}
	return nil
}

// back goes up one screen.
func (m *Model) back() {
	if len(m.nav) > 1 {
		m.nav = m.nav[:len(m.nav)-1]
		m.last = nil
	}
}

// refreshAll reads the ticket list and the open ticket's status again.
func (m *Model) refreshAll() tea.Cmd {
	cmds := []tea.Cmd{m.loadRoster()}
	if id := m.watching(); id != "" {
		cmds = append(cmds, m.refreshStatus(id))
	}
	return tea.Batch(cmds...)
}

// targetTicket is the ticket a key acts on: the open one, else the one under
// the cursor on the main screen.
func (m *Model) targetTicket() string {
	if t := m.top(); t.screen != screenMain {
		return t.ticket
	}
	if ref := m.selected(); ref != nil && ref.kind == rowTicket {
		return ref.id
	}
	return ""
}

// beginAnswer asks for the answer to a question: the one under the cursor, or
// the only one open.
func (m *Model) beginAnswer() {
	id := m.targetTicket()
	t := m.w.tickets[id]
	if t == nil {
		m.notice = "select a ticket first"
		return
	}
	var q *questionRow
	if ref := m.selected(); ref != nil && ref.kind == rowQuestion {
		for _, x := range t.questions(m.zone) {
			if x.ID == ref.id {
				x := x
				q = &x
			}
		}
		if q == nil || !q.Open {
			m.notice = ref.id + " is already answered"
			return
		}
	} else {
		slice := ""
		if top := m.top(); top.screen == screenSlice {
			slice = top.slice
		}
		open := t.openQuestions(m.zone, slice)
		switch len(open) {
		case 0:
			m.notice = "no question is waiting for an answer"
			return
		case 1:
			q = &open[0]
		default:
			m.notice = "several questions are open: put the cursor on the one to answer"
			return
		}
	}
	about := []string{"slice " + q.Slice + " asked:"}
	about = append(about, strings.Split(q.Text, "\n")...)
	m.input = &textInput{prompt: "answer " + q.ID, about: about, ticket: id, qid: q.ID}
}

// inputKey edits the line being typed.
func (m *Model) inputKey(k tea.KeyMsg) tea.Cmd {
	in := m.input
	switch k.Type {
	case tea.KeyEsc:
		m.input = nil
	case tea.KeyEnter:
		text := strings.TrimSpace(string(in.value))
		if text == "" {
			return nil
		}
		m.input = nil
		args := append([]string{"answer", "--as", "person"}, m.cfg.Scope...)
		args = append(args, in.ticket, in.qid, "--", text)
		return m.exec("answer", in.ticket, args)
	case tea.KeyBackspace:
		if n := len(in.value); n > 0 {
			in.value = in.value[:n-1]
		}
	case tea.KeyCtrlU:
		in.value = nil
	case tea.KeyRunes, tea.KeySpace:
		for _, r := range k.Runes {
			if r == '\n' || r == '\r' || r == '\t' {
				r = ' '
			}
			in.value = append(in.value, r)
		}
	}
	return nil
}

// triage decides an ask with decision keep or dismiss: the one under the
// cursor, or the only one undecided.
func (m *Model) triage(decision string) tea.Cmd {
	id := m.targetTicket()
	t := m.w.tickets[id]
	if t == nil {
		m.notice = "select a ticket first"
		return nil
	}
	asks := t.asks()
	var pick *askRow
	if ref := m.selected(); ref != nil && ref.kind == rowAsk {
		for _, a := range asks {
			if a.ID == ref.id {
				a := a
				pick = &a
			}
		}
	} else {
		var undecided []askRow
		for _, a := range asks {
			if a.Decision == "" {
				undecided = append(undecided, a)
			}
		}
		switch {
		case len(undecided) == 1:
			pick = &undecided[0]
		case len(undecided) == 0 && len(asks) == 1:
			pick = &asks[0]
		case len(asks) == 0:
			m.notice = "no ask is waiting for a decision"
			return nil
		default:
			m.notice = "several asks are waiting: put the cursor on the one to decide"
			return nil
		}
	}
	if pick == nil {
		m.notice = "no ask is waiting for a decision"
		return nil
	}
	return m.exec("triage", id, m.verbArgs("triage", id, pick.ID, decision))
}

// gate opens a gate round on the ticket.
func (m *Model) gate() tea.Cmd {
	id := m.targetTicket()
	if id == "" || m.w.tickets[id] == nil {
		m.notice = "select a ticket first"
		return nil
	}
	return m.exec("gate", id, m.verbArgs("gate", id))
}

// beginPublish asks to publish the ticket: a person's approval of the
// reviewed head, then the push and the pull request.
func (m *Model) beginPublish() {
	id := m.targetTicket()
	if id == "" || m.w.tickets[id] == nil {
		m.notice = "select a ticket first"
		return
	}
	m.ask = &confirmation{
		prompt: "publish " + id + ": approve the reviewed head, push it and open the pull request?",
		run: func() tea.Cmd {
			return m.exec("publish", id, m.verbArgs("publish", id, "--approve"), m.verbArgs("publish", id, "--yes"))
		},
	}
}

// confirmKey answers a yes-or-no: y or enter does it, anything else does not.
func (m *Model) confirmKey(k tea.KeyMsg) tea.Cmd {
	c := m.ask
	m.ask = nil
	if k.String() == "y" || k.String() == "Y" || k.Type == tea.KeyEnter {
		return c.run()
	}
	return nil
}
