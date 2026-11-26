package tui

import (
	"context"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
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
	// list one step back. Chart does the same for a chart's screen.
	Ticket string
	Chart  string
	// Awaiting says Ticket has no folder in the store yet: its screen waits
	// for it and fills in as soon as the ticket has a line in its journal or a
	// row in the ticket list.
	Awaiting bool
	// Scope is the flags that keep every verb on the store the screens were
	// opened on (`--store <path>`); they are added to each command.
	Scope []string
	// Project is the project's name, for the header.
	Project string
	// Renderer paints the screens. Nil draws without color.
	Renderer *lipgloss.Renderer
	// Zone is the time zone the screens show times in. Nil is the local one.
	Zone *time.Location
	// RetryDelay is how long to wait before a read that failed is made again;
	// zero is 300ms.
	RetryDelay time.Duration
	// AwaitEvery is how often the ticket list is read while the ticket is
	// awaited; zero is 2s, and a negative value never reads it again (a test
	// that drives the clock itself).
	AwaitEvery time.Duration
	// IdleAfter is how long a ticket's screen goes with no event before its
	// status is read again, and the least time between two such reads: the
	// brief is written to files that make no journal line, so the screen would
	// otherwise not see it. Zero is 3s.
	IdleAfter time.Duration
	// IdleTick is how often the model looks at the clock for that; zero is 1s,
	// and a negative value never looks (a test that drives the clock itself).
	IdleTick time.Duration
	// StoreHead reads the store's HEAD commit, cheaply and in process. Each
	// idle tick compares it with the last head read; a head that moved marks the
	// ticket list stale, and the next idle tick reads the list (and the open
	// chart) again. A graduation, a chart made or a brief committed by another
	// process adds no line the stream shows, so the head is what says the store
	// moved. Nil never compares.
	StoreHead func() (string, error)
	// MergeEvery is how often a screen that waits on a merge is read again: a
	// merge on the host writes no line to any journal, so the screen sees it
	// only when its verb is read. Zero is 2m, and a negative value never reads
	// it again (a test that settles Init).
	MergeEvery time.Duration
	// Now is the clock the idle read measures by. Nil is time.Now.
	Now func() time.Time
	// Home is the folder a command the screens show names as ~: a session's
	// scratchpad under it says nothing to a reader and names the person on a
	// shared screen. Empty is the user's home folder.
	Home string
	// Open sends a pull request's address to the system's opener, for the key
	// `o`: the command gives it only on a terminal. Nil opens nothing, and the
	// key says the address instead.
	Open func(url string) error
	// Viewer serves a ticket's illustration and says where it is: its address on
	// the loopback interface, or its path in a session whose browser cannot
	// reach it. It is the one action the screens take outside a verb (a verb that
	// serves it blocks), writes nothing, and the command gives it. Nil serves
	// nothing, and the key says so.
	Viewer func(ticket string) (string, error)
	// Width and Height are the screen's size until the terminal reports one;
	// zero is 100 by 30.
	Width, Height int
}

const (
	defaultWidth  = 100
	defaultHeight = 30
	// defaultIdleAfter and defaultIdleTick are Config.IdleAfter and
	// Config.IdleTick when they are zero.
	defaultIdleAfter = 3 * time.Second
	defaultIdleTick  = time.Second
	// defaultMergeEvery is Config.MergeEvery when it is zero.
	defaultMergeEvery = 2 * time.Minute
	// batchMax bounds how many lines of the stream one update takes.
	batchMax = 512
)

// screenKind is which of the nine screens is showing.
type screenKind int

const (
	screenMain    screenKind = iota // the project: its tickets and charts
	screenTicket                    // one ticket: its steps, what needs a person, a step's activity
	screenBuild                     // a ticket's slices and the sessions building them
	screenReview                    // a ticket's gate rounds and the asks they left
	screenSlice                     // one slice: its questions and its timeline
	screenBrief                     // a ticket's brief: its approval, sections and slices
	screenPublish                   // a ticket's publish: body, approval, pull request
	screenChart                     // a chart: its map, tickets, run and activity
	screenLand                      // a ticket's land: its pull requests, the merges to make and the landings
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
	case screenBrief:
		return "brief"
	case screenPublish:
		return "publish"
	case screenChart:
		return "chart"
	case screenLand:
		return "land"
	}
	return "?"
}

// navEntry is a screen on the way in: which one, about what, and where the
// cursor is on it.
type navEntry struct {
	screen screenKind
	ticket string
	slice  string
	chart  string
	sel    int
	// part is the row of the brief screen's Parts list the screen is opened on,
	// by identity (a section by its place, a slice by its id); partSet is false
	// when it is opened for no part and begins on its first row.
	part    rowIdentity
	partSet bool
	// cur is the row the main screen's cursor stands on, by identity, found
	// again on each draw wherever the page now has it; curSet is false until
	// the page has had a row to stand on.
	cur    rowIdentity
	curSet bool
	// open are the fold rows of the main screen that are opened, by group name;
	// byChart is the view of the main screen laid out by chart, which a run does
	// not keep.
	open    map[string]bool
	byChart bool
	// search narrows the main screen to the rows its query matches (search.go).
	search searchState
	// entered is set once a chart screen has started to read its chart.
	entered bool
	// showMap shows the Map panel on the plan of an issue, which hides it.
	showMap bool
	// scroll is how many lines a page with no row to stand on is moved up, so a
	// screen longer than its window can be read to its end.
	scroll int
	// lead is how many lines a page with rows is held above the window its
	// cursor gives, so the lines above the first row can be read: raised by a move
	// up from the first row, by lines coming in above (hold.go) and cleared by any
	// move of the cursor.
	lead int
	// fresh is how many rows came in above the window of a page read below its
	// top, which its first row counts; it is cleared once the window is back at
	// the top.
	fresh int
	// follow keeps a ticket screen's cursor on the current step, and a chart
	// screen's on the first ticket not published, until the person moves it;
	// lastCur is the current step the screen last showed, so a cursor left on
	// the current step moves on with it.
	follow  bool
	lastCur int
	// paneFocus is the title of the pane that has the focus, and paneCurs what
	// each pane keeps while it has not: its cursor's row and its scroll (panes.go).
	// seenSel is the cursor the panes were last read against, so a cursor put on
	// a row by anything else gives its pane the focus. textFocus says the focused
	// pane is text only: it scrolls by lines and has no row under a cursor.
	paneFocus string
	// listFocus is the title of the list of a phase screen that had the focus last:
	// a focus on a pane that is not one of the lists leaves the Detail on its row.
	listFocus string
	// zoom is the title of the pane drawn over the whole body, "" for none.
	zoom string
	// psearch is the query narrowing one pane (searchpane.go).
	psearch   paneSearch
	paneCurs  map[string]paneCursor
	seenSel   int
	seenSet   bool
	textFocus bool
}

// textInput is a line a person is typing: an answer.
type textInput struct {
	prompt string
	// head and about are what the person answers: who asked, and the question
	// as written. The footer wraps about when it draws, so a resize re-wraps it.
	head, about string
	value       []rune
	ticket      string
	qid         string
}

// confirmation is an action that waits for a yes.
type confirmation struct {
	prompt string
	run    func() tea.Cmd
}

// Model is the TUI: nine screens over the event stream and the verbs. It is a
// bubbletea model, and nothing else in the package needs the terminal: its
// View is plain text a test compares.
type Model struct {
	cfg Config
	st  styles
	// edges remembers the pieces of a panel's border the renderer drew.
	edges edgeCache
	run   classedRunner
	// queue is what run goes through: one command at a time, the next by class.
	queue *queue
	zone  *time.Location

	w             *world
	width, height int
	nav           []navEntry

	input  *textInput
	ask    *confirmation
	last   *outcome
	notice string
	// served is the address each ticket's illustration was given when the person
	// opened it, so opening it again reuses the address.
	served map[string]string
	tail   bool
	// drawn is what the last View drew of each ask (askCut, askWhole), nil when
	// a key has been taken in since that View: a key that acts on an ask acts
	// on what the person read, never on a screen nobody has seen.
	drawn map[string]int

	ctx        context.Context
	cancel     context.CancelFunc
	stream     chan events.Envelope
	streamErr  chan error
	rosterBusy bool
	// mergeRefreshing is set while the read that asks the host for the kept
	// pull request states is out (mergerefresh.go).
	mergeRefreshing bool
	// clockRuns counts the work clocks computed (work.go): the screens that
	// show no time compute none.
	clockRuns int
	// rosterStarted counts the roster reads begun, so a read can be told from one
	// that was already out when something happened.
	rosterStarted int
	rosterFails   int
	retryDelay    time.Duration
	awaitEvery    time.Duration
	quitting      bool

	// The idle read: when the screen last took an event of the ticket it shows
	// and when it last started to read that ticket's status, by now.
	idleAfter, idleTick time.Duration
	// mergeEvery is how often a screen that waits on a merge is read again.
	mergeEvery time.Duration
	now        func() time.Time
	// home matches the home folder in any of its spellings (homePattern).
	home                *regexp.Regexp
	lastEvent, lastRead time.Time
	// The store's head as the idle tick last read it, and whether the ticket
	// list is out of date: the head moved, or a journal line touched a ticket
	// since the list was read, at lastRoster.
	head        string
	headKnown   bool
	rosterStale bool
	lastRoster  time.Time
	// rosterEnded is when the last list read came back: the spacing between
	// reads counts from it, so a slow list is never read again at once.
	rosterEnded time.Time
	// followNew is set while screens opened on no ticket wait to open the
	// first ticket that appears after started, when the stream first names
	// it: a key pressed before that clears it.
	followNew bool
	// folded says, for each row of the main screen's list that a closed fold
	// hides, the fold row it is in: the page of the last draw.
	folded map[rowIdentity]rowIdentity
	// main is the project's list as last built (mainqueue.go), kept while nothing it
	// reads has moved.
	main *mainCache
	// keysOn is what the keys of the screen on top act on, as the last frame
	// found it: the hint line is cut by it, and a key reads it again before it
	// runs.
	keysOn subject
	// hit is what the last frame drew of the body, for a click (mouse.go).
	hit *hitMap
	// sibMemo is the siblings of the screen on top as one frame or one message
	// read them, once read (sibs).
	sibMemo *sibSet
	sibRead bool
	// kept is where the cursor stood on each screen the person left, for the
	// session (moves.go).
	kept map[navKey]navEntry
	// table is the key table (keys.go): the one place a key lives.
	table []keyRow
	// panesNow are the titles of the panes of the screen on top as the last page
	// read had them, in drawing order (panes.go).
	panesNow []string
	// help is the `?` overlay while it is up.
	help *helpView
	// laid holds the layouts the allocator gave lists, by what they were made of
	// (laidOnce), so a frame that reads the same lists measures none.
	laid map[string][][]string
	// laidBuilds counts the lists the allocator laid out and the memo did not hold.
	laidBuilds int
	// pageBuilds counts the pages built: a frame builds one.
	pageBuilds int
	// rowMemo holds the rows of the chart screens built for the world as it
	// stands (chartcache.go).
	rowMemo *rowsMemo
	// shown is the page the last frame drew (chartcache.go).
	shown *shownPage
	// rowReads counts the tickets read into a row of a chart screen.
	rowReads int
	// queueBuilds counts the project's list built (maincache.go): a key that
	// moves nothing the list reads builds none, a search key included.
	queueBuilds int
	// rowBuilds counts the rows of the project's list built from a ticket (their
	// cells), which a step of the clock does not do.
	rowBuilds int
	// detailBuilds counts the details of a ticket's row built, the words after
	// its steps: a key of a search builds none.
	detailBuilds int
	// measured counts the rows the layout measured: a frame that changed none of
	// what a page reads measures none.
	measured int
	// seen is what the last frame drew of the screen on top, which the next one
	// settles the cursor and the window against (hold.go).
	seen    *held
	started time.Time
}

// New builds the model for cfg.
func New(cfg Config) *Model {
	m := &Model{cfg: cfg, w: newWorld(), width: cfg.Width, height: cfg.Height, table: keyRows()}
	if m.width <= 0 {
		m.width = defaultWidth
	}
	if m.height <= 0 {
		m.height = defaultHeight
	}
	m.retryDelay = cfg.RetryDelay
	if m.retryDelay <= 0 {
		m.retryDelay = 300 * time.Millisecond
	}
	m.awaitEvery = cfg.AwaitEvery
	if m.awaitEvery == 0 {
		m.awaitEvery = 2 * time.Second
	}
	m.idleAfter, m.idleTick = cfg.IdleAfter, cfg.IdleTick
	if m.idleAfter == 0 {
		m.idleAfter = defaultIdleAfter
	}
	if m.idleTick == 0 {
		m.idleTick = defaultIdleTick
	}
	m.mergeEvery = cfg.MergeEvery
	if m.mergeEvery == 0 {
		m.mergeEvery = defaultMergeEvery
	}
	m.now = cfg.Now
	if m.now == nil {
		m.now = time.Now
	}
	home := cfg.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	m.home = homePattern(home)
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
		m.queue = &queue{run: cfg.Run}
		m.run = m.queue.do
	}
	m.nav = []navEntry{{screen: screenMain}}
	m.followNew, m.started = cfg.Ticket == "" && cfg.Chart == "", m.now()
	if cfg.Ticket != "" {
		t, _ := m.w.ticketFor(cfg.Ticket)
		t.awaiting = cfg.Awaiting
		m.nav = append(m.nav, navEntry{screen: screenTicket, ticket: cfg.Ticket, follow: true})
	} else if cfg.Chart != "" {
		m.nav = append(m.nav, navEntry{screen: screenChart, chart: cfg.Chart, follow: true})
	}
	m.syncScreen()
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
	m.readHead()
	cmds := []tea.Cmd{m.startStream(), m.loadRoster()}
	if id := m.watching(); id != "" {
		cmds = append(cmds, m.refreshStatus(id))
	}
	if m.awaitingTicket() {
		cmds = append(cmds, m.awaitAfter())
	}
	cmds = append(cmds, m.idleAfterTick(), m.mergeAfterTick())
	return tea.Batch(cmds...)
}

// Stop ends the stream. The program that runs the model calls it when it is
// done; quitting from a key does it too.
func (m *Model) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// awaitingTicket is whether the ticket the screens were opened on is one that
// does not exist yet.
func (m *Model) awaitingTicket() bool {
	t := m.w.tickets[m.cfg.Ticket]
	return m.cfg.Ticket != "" && t != nil && t.awaiting
}

// awaitMsg asks for the ticket list to be read again while a ticket is awaited.
type awaitMsg struct{}

// awaitAfter asks, after a moment, to look for the awaited ticket in the
// ticket list: a ticket that exists but has no line yet shows nowhere else.
func (m *Model) awaitAfter() tea.Cmd {
	if m.awaitEvery < 0 {
		return nil
	}
	return tea.Tick(m.awaitEvery, func(time.Time) tea.Msg { return awaitMsg{} })
}

// idleMsg asks the model to look at whether the ticket on its screen has gone
// quiet.
type idleMsg struct{}

// idleAfterTick asks, after a moment, for that look.
func (m *Model) idleAfterTick() tea.Cmd {
	if m.idleTick < 0 {
		return nil
	}
	return tea.Tick(m.idleTick, func(time.Time) tea.Msg { return idleMsg{} })
}

// mergeMsg asks the model to read the screen on top again if it waits on a
// merge.
type mergeMsg struct{}

// mergeAfterTick asks, after a moment, for that read. It runs for the life of
// the model, whichever screen is on top: onMerge reads only for a screen that
// waits.
func (m *Model) mergeAfterTick() tea.Cmd {
	if m.mergeEvery < 0 {
		return nil
	}
	return tea.Tick(m.mergeEvery, func(time.Time) tea.Msg { return mergeMsg{} })
}

// onMerge reads what the screen on top shows of a merge. A chart whose run
// ends at a merge reads the ticket list and the chart: the chart verb asks the
// host about the pull requests it names each time, and a merge changes no
// ticket's status. A ticket published with a pull request, not closed, reads
// its status. Every other screen, the project's list included, reads nothing.
// A read already out is not doubled: the loads refuse a second one.
func (m *Model) onMerge() tea.Cmd {
	if e := m.top(); e.screen == screenMain {
		if m.mainWaitsOnMerge() {
			return m.refreshMergesThenRoster()
		}
		return nil
	}
	if e := m.top(); e.screen == screenChart {
		if c := m.w.charts[e.chart]; c != nil && c.waitsOnMerge() {
			return tea.Batch(m.loadRoster(), m.loadChart(e.chart))
		}
		return nil
	}
	if id := m.watching(); id != "" && m.ticketWaitsOnMerge(id) {
		return m.refreshStatus(id)
	}
	return nil
}

// onIdle reads the status of the ticket on the screen when it has had no event
// for idleAfter and its status was last read no less long ago. The brief and the
// slices are written to files that make no journal line, and a status is the
// only place the screens can see them; the read is made for the watched ticket
// alone, so the cost of a quiet screen is one status every few seconds.
func (m *Model) onIdle() tea.Cmd {
	return tea.Batch(m.onStoreIdle(), m.onWatchedIdle())
}

// onStoreIdle compares the store's head with the last one read, on every
// screen, and reads the ticket list again, and the chart on the screen, when
// the list is stale. The first head is the baseline and a head that could not
// be read marks nothing and keeps the baseline. A list read is made no sooner
// than idleAfter after the last one and never doubled, so a store that does not
// move costs one head read a tick and no verb. A brief written to its files
// without a commit may leave the head where it is; the ticket's own screen
// still sees it through its idle status read.
func (m *Model) onStoreIdle() tea.Cmd {
	m.readHead()
	if !m.rosterStale || m.rosterBusy || m.now().Sub(m.rosterEnded) < m.idleAfter {
		return nil
	}
	cmds := []tea.Cmd{m.loadRoster()}
	if e := m.top(); e.screen == screenChart {
		cmds = append(cmds, m.loadChart(e.chart))
	}
	return tea.Batch(cmds...)
}

// readHead reads the store's head and marks the list stale when it moved since
// the last one read. The first read is the baseline: Init makes it next to the
// first list read, so a commit that lands in the first second is a move.
func (m *Model) readHead() {
	if m.cfg.StoreHead == nil {
		return
	}
	if head, err := m.cfg.StoreHead(); err == nil {
		if m.headKnown && head != m.head {
			m.rosterStale = true
		}
		m.head, m.headKnown = head, true
	}
}

// onWatchedIdle is the idle read of the screen's own ticket or chart.
func (m *Model) onWatchedIdle() tea.Cmd {
	if e := m.top(); e.screen == screenChart {
		return m.onChartIdle(e.chart)
	}
	t := m.w.tickets[m.watching()]
	if t == nil || t.awaiting || t.loading {
		return nil
	}
	now := m.now()
	if now.Sub(m.lastEvent) < m.idleAfter || now.Sub(m.lastRead) < m.idleAfter {
		return nil
	}
	return m.refreshStatus(t.id)
}

// onChartIdle reads the ticket list and the chart on the screen when something
// happened to one of its tickets since it was read and the screen has had no
// event for idleAfter, and the last read is no less long ago. A merge on the
// host writes no line to any journal, so what a ticket waits for to be merged
// changes only in what the verb says: the read after the quiet is where the
// screen sees it, and a chart nothing happened to is not read again for it.
func (m *Model) onChartIdle(name string) tea.Cmd {
	c := m.w.charts[name]
	if c == nil || !c.dirty || c.loading {
		return nil
	}
	now := m.now()
	if now.Sub(m.lastEvent) < m.idleAfter || now.Sub(m.lastRead) < m.idleAfter {
		return nil
	}
	return tea.Batch(m.loadRoster(), m.loadChart(name))
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
	m.forgetSibs()
	if _, key := msg.(tea.KeyMsg); !key {
		// A key moves the cursor, or starts a command whose result is a message
		// of its own: what the screens built from the world holds.
		m.w.gen++
	}
	next, cmd := m.update(msg)
	m.settleLast(msg)
	m.syncScreen()
	m.forgetSibs()
	return next, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return m, m.keys(msg)
	case tea.MouseMsg:
		return m, m.mouse(msg)
	case eventsMsg:
		return m, tea.Batch(m.onEvents(msg), m.waitEvents())
	case streamEndMsg:
		if msg.err != nil && !m.quitting {
			m.notice = "the event stream ended: " + msg.err.Error()
		}
	case rosterMsg:
		return m, m.onRoster(msg)
	case chartMsg:
		return m, m.onChart(msg)
	case awaitMsg:
		if m.awaitingTicket() {
			return m, tea.Batch(m.loadRoster(), m.awaitAfter())
		}
	case idleMsg:
		return m, tea.Batch(m.onIdle(), m.idleAfterTick())
	case mergeRefreshedMsg:
		m.mergeRefreshing = false
		return m, m.loadRoster()
	case mergeMsg:
		return m, tea.Batch(m.onMerge(), m.mergeAfterTick())
	case statusMsg:
		return m, m.onStatus(msg)
	case briefMsg:
		return m, m.onBrief(msg)
	case retryMsg:
		if msg.chart != "" {
			return m, m.loadChart(msg.chart)
		}
		if msg.ticket == "" {
			return m, m.loadRoster()
		}
		if m.watching() == msg.ticket {
			return m, m.refreshStatus(msg.ticket)
		}
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
	// A line about the brief (its approval, a check of the slices) says what
	// the screens read of its text may have changed.
	for _, e := range envs {
		if t := m.w.tickets[e.Ticket]; t != nil && e.Src == events.SrcJournal && stepOfLine(e) == stepBrief {
			t.briefStale = true
		}
	}
	// A line of a ticket may be one no commit carries (a landing written by
	// another session), so it says the list may be out of date as well.
	for _, e := range envs {
		if e.Src != events.SrcJournal || e.Ticket == "" {
			continue
		}
		// The replay of what the store held at start is older than the list
		// read made then; a line from another machine merges in with a store
		// commit, which the head comparison sees.
		if at, ok := e.At(); !ok || !at.Before(m.lastRoster) {
			m.rosterStale = true
		}
	}
	var cmds []tea.Cmd
	if fresh {
		cmds = append(cmds, m.loadRoster())
	}
	m.openNewTicket(touched)
	if e := m.top(); e.screen == screenChart {
		cmds = append(cmds, m.chartTouched(e.chart, touched)...)
	}
	if id := m.watching(); id != "" && touched[id] {
		m.lastEvent = m.now()
		cmds = append(cmds, m.refreshStatus(id))
		if m.top().screen == screenBrief {
			cmds = append(cmds, m.loadBrief(id))
		}
	}
	return tea.Batch(cmds...)
}

// openNewTicket opens, on screens opened on no ticket that still wait for one,
// the earliest of touched that the stream first named after the screens
// started: a session minted it, and it is what the person is here to watch.
// The list stays one step back.
func (m *Model) openNewTicket(touched map[string]bool) {
	if !m.followNew || m.top().screen != screenMain {
		return
	}
	pick := ""
	for id := range touched {
		t := m.w.tickets[id]
		if !t.firstAt.After(m.started) {
			continue
		}
		if p := m.w.tickets[pick]; p == nil || t.firstAt.Before(p.firstAt) || (t.firstAt.Equal(p.firstAt) && id < pick) {
			pick = id
		}
	}
	if pick == "" {
		return
	}
	m.followNew = false
	m.nav = append(m.nav, navEntry{screen: screenTicket, ticket: pick, follow: true})
}

// chartTouched is what the chart screen on top does about the tickets a batch
// of the stream touched: the ones that are the chart's have their status read
// again, and the chart is read again once the screen has been quiet.
func (m *Model) chartTouched(name string, touched map[string]bool) []tea.Cmd {
	c := m.w.charts[name]
	if c == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, id := range c.ids() {
		if touched[id] {
			c.dirty = true
			m.lastEvent = m.now()
			cmds = append(cmds, m.refreshStatus(id))
		}
	}
	if issue := c.issueID(); issue != "" && touched[issue] {
		c.dirty = true
		m.lastEvent = m.now()
		cmds = append(cmds, m.refreshStatus(issue))
	}
	return cmds
}

// retryMsg asks for a read that failed to be made again: the chart's when chart
// is set, else the ticket's status, or the ticket list when ticket is "".
type retryMsg struct{ ticket, chart string }

// A read of a store can fail for a moment while another process replaces a
// file in it (Windows refuses to open a file at the instant it is renamed
// over), so a read that fails is made again, readRetries times, before the
// screens say so.
const readRetries = 5

func (m *Model) onStatus(msg statusMsg) tea.Cmd {
	t := m.w.tickets[msg.ticket]
	if t == nil {
		return nil
	}
	t.loading = false
	if msg.info.Err != nil {
		t.failures++
		if t.failures < readRetries {
			return m.retryAfter(msg.ticket)
		}
		m.notice = "status " + msg.ticket + ": " + msg.info.Err.Msg
		return nil
	}
	t.failures = 0
	m.noteChanges(t, t.status, msg.info)
	t.status = msg.info
	if t.dirty && m.watching() == msg.ticket {
		return m.refreshStatus(msg.ticket)
	}
	return nil
}

func (m *Model) onRoster(msg rosterMsg) tea.Cmd {
	cmd := m.takeRoster(msg)
	if m.last != nil && !m.last.Fixed && m.last.Ticket != "" && msg.seq <= m.last.After {
		// The read was already out when the command's result arrived: it may
		// not say what the command did, so one that starts now does.
		return tea.Batch(cmd, m.loadRoster())
	}
	return cmd
}

func (m *Model) takeRoster(msg rosterMsg) tea.Cmd {
	m.rosterBusy = false
	m.rosterEnded = m.now()
	if msg.err != nil {
		m.rosterFails++
		if m.rosterFails < readRetries {
			return m.retryAfter("")
		}
		m.notice = "tickets: " + msg.err.Error()
		return nil
	}
	m.rosterFails = 0
	was := m.awaitingTicket()
	var shown []string
	if e := m.top(); e.screen == screenChart && m.w.charts[e.chart] != nil {
		shown = m.w.charts[e.chart].listing.Tickets
	}
	first := !m.w.rosterRead
	under, onRow := m.rowUnderCursor()
	m.w.setRoster(msg.rows)
	m.w.setCharts(msg.charts)
	m.putCursorOn(under, onRow)
	var cmds []tea.Cmd
	if first && m.top().screen == screenMain && m.mainWaitsOnMerge() {
		// What the roster shows of a merge is the last state kept, which may be
		// old at launch: the host is asked once, in the background, as the merge
		// timer does after.
		cmds = append(cmds, m.refreshMergesThenRoster())
	}
	if was && !m.awaitingTicket() {
		// The awaited ticket is listed: read it.
		cmds = append(cmds, m.refreshStatus(m.cfg.Ticket))
	}
	if e := m.top(); e.screen == screenChart && m.w.charts[e.chart] != nil {
		switch c := m.w.charts[e.chart]; {
		case !e.entered:
			// The chart the screens were opened on is listed: read it.
			cmds = append(cmds, m.enterChart(e)...)
		case !slices.Equal(shown, c.listing.Tickets):
			// Tickets were graduated into it, or taken out: read it again.
			cmds = append(cmds, m.enterChart(e)...)
		}
	}
	return tea.Batch(cmds...)
}

// onChart takes a chart's standing in. A read that failed is made again, as the
// ticket list's is; a verb that printed no chart leaves the chart as the listing
// has it, with no notice.
func (m *Model) onChart(msg chartMsg) tea.Cmd {
	c := m.w.charts[msg.name]
	if c == nil {
		return nil
	}
	c.loading = false
	if msg.err != nil {
		c.failures++
		if c.failures < readRetries {
			return tea.Tick(m.retryDelay, func(time.Time) tea.Msg { return retryMsg{chart: msg.name} })
		}
		m.notice = "chart " + msg.name + ": " + msg.err.Error()
		return nil
	}
	c.failures = 0
	c.info = msg.info
	if msg.info != nil {
		// A read that printed no chart leaves the wait as it was, so one blank
		// read does not stop the timer.
		c.mergeWait = len(msg.info.Ends) > 0
	}
	// The plan of an issue reads its need and its suite from the issue's own
	// status, read whenever the chart is.
	return m.refreshIssue(c)
}

// retryAfter asks, after a moment, for the read of ticket (or of the ticket
// list) to be made again.
func (m *Model) retryAfter(ticket string) tea.Cmd {
	return tea.Tick(m.retryDelay, func(time.Time) tea.Msg { return retryMsg{ticket: ticket} })
}

// enterChart is what opening the chart screen e does (and what a change of the
// chart's tickets does again): the chart is read, and so is the status of each
// of its tickets that has not been, so the steps drawn are the store's word.
func (m *Model) enterChart(e *navEntry) []tea.Cmd {
	c := m.w.charts[e.chart]
	e.entered = true
	cmds := []tea.Cmd{m.loadChart(e.chart), m.refreshIssue(c)}
	for _, id := range c.ids() {
		if t := m.w.tickets[id]; t != nil && t.status == nil {
			cmds = append(cmds, m.refreshStatus(id))
		}
	}
	return cmds
}

// standingPrint is where the ticket stands, as its phases' states and words:
// what the panel's line is made against, so the line goes when it changes. A
// command with no ticket is about the chart on show, whose verdict is its
// standing; one made elsewhere has none and stays until the screen changes.
func (m *Model) standingPrint(ticket string) string {
	if ticket == "" {
		if e := m.top(); e.screen == screenChart {
			if c := m.w.charts[e.chart]; c != nil && c.info != nil {
				return strings.Join([]string{c.info.Code, c.info.Reason, c.info.Prompt}, "\x1f")
			}
		}
		return ""
	}
	t := m.w.tickets[ticket]
	if t == nil {
		return ""
	}
	var parts []string
	for _, p := range m.phaseOf(t).Phases {
		parts = append(parts, string(p.Name)+"\x1f"+p.State+"\x1f"+p.Text)
	}
	return strings.Join(parts, "\x1e")
}

// settleLast keeps the last command's line against what it was about. The
// ticket's phases are the roster's digest, so the first roster read that began
// after the result arrived (the chart's read, for a line with no ticket) takes
// the standing as the command left it, and a status read or a read that was
// already out does not; each later message removes the line once the ticket's
// phases read differently, or the chart's verdict does.
func (m *Model) settleLast(msg tea.Msg) {
	if m.last == nil {
		return
	}
	if !m.last.Fixed {
		switch r := msg.(type) {
		case rosterMsg:
			if m.last.Ticket != "" && r.err == nil && r.seq > m.last.After {
				m.last.Print, m.last.Fixed = m.standingPrint(m.last.Ticket), true
			}
		case chartMsg:
			if m.last.Ticket == "" && r.err == nil {
				m.last.Print, m.last.Fixed = m.standingPrint(""), true
			}
		}
		return
	}
	if m.standingPrint(m.last.Ticket) != m.last.Print {
		m.last = nil
	}
}

func (m *Model) onRan(msg ranMsg) tea.Cmd {
	m.last = &msg.result
	m.last.After = m.rosterStarted
	if msg.result.Label == labelPublishWhat {
		m.publishRead(m.last)
	}
	var cmds []tea.Cmd
	if msg.result.Ticket != "" {
		// The roster read that follows is the one that says what the command did.
		cmds = append(cmds, m.loadRoster())
	}
	if e := m.top(); e.screen == screenChart {
		// What a key did to one of the chart's tickets is read once things are quiet.
		if c := m.w.charts[e.chart]; c != nil && c.has(msg.result.Ticket) {
			c.dirty = true
		}
	}
	if id := msg.result.Ticket; id != "" && m.w.tickets[id] != nil {
		m.w.tickets[id].dirty = true
		if m.watching() == id {
			cmds = append(cmds, m.refreshStatus(id))
		}
	}
	return tea.Batch(cmds...)
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
		// No screen is drawn between the keys of one read.
		m.drawn = nil
	}
	return tea.Batch(cmds...)
}

// key takes a key press in.
func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	if k.Type == tea.KeyCtrlC {
		return m.quit()
	}
	// A person at the keys chooses what to look at.
	m.followNew = false
	if m.input != nil {
		return m.inputKey(k)
	}
	if m.ask != nil {
		return m.confirmKey(k)
	}
	if m.help != nil {
		return m.helpKey(k)
	}
	m.notice = ""
	if m.top().screen == screenMain {
		if m.nav[0].search.open {
			return m.searchKey(k)
		}
	} else if m.top().psearch.open {
		return m.paneSearchKey(k)
	}
	if m.top().screen == screenLand {
		// The land screen shows the landing and acts on nothing: the session
		// merges and records it at the person's word, said to it, not here.
		switch k.String() {
		case "a", "k", "d", "g", "p", "u", "v":
			m.notice = `landing is said to the session: "Land ` + m.top().ticket + `"`
			return nil
		}
	}
	return m.runKey(k.String())
}

// move puts the cursor delta rows on, within the screen's rows. A cursor
// that was following the current step stays where it moved.
func (m *Model) move(delta int) {
	m.moveBy(delta)
	m.rememberMove(m.geometry())
	m.noteCursor()
}

func (m *Model) moveBy(delta int) {
	m.followSel()
	pg := m.geometry()
	n := len(pg.refs())
	e := m.top()
	m.syncPanes(pg)
	// The keys move the focused pane's cursor, within its rows.
	lo, cnt := m.paneSpan(pg)
	hi := lo + cnt - 1
	if n == 0 || e.textFocus {
		e.scroll = skipNames(pg.lines, clampStart(pg.lines, m.bodyRows(), e.scroll+delta), delta, m.bodyRows())
		return
	}
	e.follow = false
	if e.lead == 0 || delta <= -(1<<19) || delta >= 1<<19 {
		// A page held above its cursor's window is read a line at a time first.
		if m.moveWindowed(pg, delta) {
			return
		}
	}
	m.windowStart(pg, e.sel, m.bodyRows(), true) // holds only the lines there are
	switch {
	case delta <= -(1 << 19):
		// Home: the focused pane's first row; the window follows it, and a page that
		// has no Detail is held at its top.
		e.sel, e.lead = lo, 0
		if lo == 0 && !pg.follows() {
			e.lead = 1 << 20
		}
		return
	case delta >= 1<<19:
		e.sel, e.lead = hi, 0
		return
	case e.lead > 0 || (delta < 0 && e.sel == 0 && lo == 0):
		// A page held above its cursor's window is read a line at a time, from
		// whatever row the cursor is on, until it is back at the window: where the
		// focused pane has no row to move to the page is read, not the cursor moved.
		// Up from the first row moves the page above it; any other move is the
		// cursor's, and the window follows it. The window clamps lead
		// to the lines there are, so the first move after a screen opened at
		// its top starts at once.
		// A window that would start where it cannot, on the top or the names of the
		// list it is in, is the one it was: the page goes on to the next line it
		// can show.
		h := m.bodyRows()
		from := func() int {
			f, _, _ := windowSpan(pg, e.sel, m.windowStart(pg, e.sel, h, false), h, e.lead == 0)
			return f
		}
		was := from()
		for i := 0; i < 8; i++ {
			if delta < 0 {
				e.lead = min(e.lead-delta, 1<<20)
			} else {
				e.lead = max(0, e.lead-delta)
			}
			if e.lead == 0 || e.lead == 1<<20 || from() != was {
				break
			}
		}
		return
	}
	was := e.sel
	e.sel = min(max(e.sel+delta, lo), hi)
	if e.sel != was {
		e.lead = 0
	}
}

// rowUnderCursor is the row the cursor stands on, false when the screen has no
// row or none is under it.
func (m *Model) rowUnderCursor() (rowRef, bool) {
	e := m.top()
	if e.follow {
		return rowRef{}, false
	}
	pg := m.page()
	m.syncPanes(pg)
	refs := pg.refs()
	if e.sel < 0 || e.sel >= len(refs) || e.textFocus {
		return rowRef{}, false
	}
	return refs[e.sel], true
}

// putCursorOn moves the cursor to the row ref names, which a new list of rows
// may have put elsewhere: the person's place is the ticket, not its number in
// the list. A row the list no longer has leaves the cursor where it was.
func (m *Model) putCursorOn(ref rowRef, ok bool) {
	if !ok {
		return
	}
	refs := m.page().refs()
	for i, r := range refs {
		if r == ref {
			m.top().sel = i
			return
		}
	}
	// A row the new list lacks leaves the cursor on the last one there is.
	if e := m.top(); e.sel >= len(refs) {
		e.sel = max(0, len(refs)-1)
	}
}

// followSel puts a following cursor on the ticket's current step, and moves
// a cursor the person left on the current step on to the next one when the
// step is done.
func (m *Model) followSel() {
	e := m.top()
	if e.screen == screenMain {
		m.followRow(e)
		return
	}
	if e.screen == screenChart && e.follow {
		if c := m.w.charts[e.chart]; c != nil {
			e.sel = m.chartFirstOpen(c)
		}
		return
	}
	if e.screen != screenTicket {
		return
	}
	t := m.w.tickets[e.ticket]
	if t == nil || isMulti(t) {
		return
	}
	cur := currentStep(m.stepWords(t))
	if e.follow || (e.sel == e.lastCur && cur != e.lastCur) {
		e.sel = cur
	}
	e.lastCur = cur
}

// switchView is `s` on the main screen: the list by state, or by chart. The
// cursor stays on its ticket (its chart, or its fold row) across the switch.
func (m *Model) switchView() {
	e := m.top()
	if e.screen != screenMain || m.readingStore() {
		return
	}
	was, ok := rowRef{}, false
	if refs := m.mainRefs(); e.sel >= 0 && e.sel < len(refs) {
		was, ok = refs[e.sel], true
	}
	m.nav[0].byChart = !m.nav[0].byChart
	e.lead = 0
	refs := m.mainRefs()
	e.sel = min(e.sel, max(len(refs)-1, 0))
	if ok {
		// The same row where the view has it, else the same ticket (or chart, or
		// fold) wherever it is drawn, else the fold row that hides it.
		if i, found := m.findRow(refs, was.identity()); found {
			e.sel = i
		}
	}
	e.curSet = false
	m.noteCursor()
}

// noteCursor remembers the row under the main screen's cursor as an identity.
func (m *Model) noteCursor() {
	e := m.top()
	if e.screen != screenMain {
		return
	}
	refs := m.mainRefs()
	if e.sel >= 0 && e.sel < len(refs) {
		e.cur, e.curSet = refs[e.sel].identity(), true
	}
}

// followRow puts the main screen's cursor on the row it stood on, wherever the
// page now has it. A row that left the page leaves the cursor on the row at
// the same place, or the nearest one, and that row is the one it follows from
// then on.
func (m *Model) followRow(e *navEntry) {
	refs := m.mainRefs()
	if len(refs) == 0 {
		return
	}
	if e.curSet {
		for i, r := range refs {
			if r.identity() == e.cur {
				e.sel = i
				return
			}
		}
	}
	if fold, ok := m.folded[e.cur]; ok && e.curSet {
		// The row is folded away: the cursor is on the fold row that holds it.
		for i, r := range refs {
			if r.identity() == fold {
				e.sel, e.cur = i, fold
				return
			}
		}
	}
	e.sel = min(max(e.sel, 0), len(refs)-1)
	e.cur, e.curSet = refs[e.sel].identity(), true
}

// findRow is where the row stands among refs, as main now draws it: its
// identity (kind, id and the container it is drawn under) first, else the same
// kind and id wherever it is drawn, else the fold row that hides a row of that
// kind and id (m.folded, as the last mainRefs read it). Every place that puts
// the cursor back on a row finds it by this.
func (m *Model) findRow(refs []rowRef, id rowIdentity) (int, bool) {
	for i, r := range refs {
		if r.identity() == id {
			return i, true
		}
	}
	for i, r := range refs {
		if r.kind == id.kind && r.id == id.id {
			return i, true
		}
	}
	for hidden, fold := range m.folded {
		if hidden.kind != id.kind || hidden.id != id.id {
			continue
		}
		for i, r := range refs {
			if r.identity() == fold {
				return i, true
			}
		}
	}
	return 0, false
}

// selected is the row under the cursor, nil when the screen has none.
func (m *Model) selected() *rowRef {
	m.followSel()
	return m.refIn(m.page())
}

// refIn is the row of pg under the cursor.
func (m *Model) refIn(pg page) *rowRef {
	m.syncPanes(pg)
	refs := pg.refs()
	sel := m.top().sel
	if sel < 0 || sel >= len(refs) || m.top().textFocus {
		// A pane of text has no row under a cursor.
		return nil
	}
	if e := m.top(); e.lead > 0 && !m.rowShown(pg, sel) {
		// A row the window does not show is not under the cursor: a key that
		// acts on it would decide what its person cannot see.
		return nil
	}
	return &refs[sel]
}

// rowShown says whether the sel-th selectable row of pg is in the window the
// screen draws.
func (m *Model) rowShown(pg page, sel int) bool {
	h := m.bodyRows()
	start := m.windowStart(pg, sel, h, false)
	from, to, _ := windowSpan(pg, sel, start, h, m.top().lead == 0 || (m.top().fresh != 0 && start != 0))
	n := 0
	for i, l := range pg.lines {
		n += len(l.hiddenRefs)
		if l.ref == nil {
			continue
		}
		if n == sel {
			return i >= from && i < to
		}
		n++
	}
	return false
}

// open goes into the row under the cursor.
func (m *Model) open() tea.Cmd {
	ref := m.selected()
	if ref == nil {
		return nil
	}
	if ref.kind == rowFold {
		// A fold opens or closes in place: no screen is pushed.
		e := &m.nav[0]
		if e.open == nil {
			e.open = map[string]bool{}
		}
		e.open[ref.id] = !e.open[ref.id]
		return nil
	}
	if m.promptFor(*ref) != "" {
		// An open question's subject is the prompt that answers it.
		m.beginAnswer()
		return nil
	}
	if ref.kind == rowIllustration {
		// The illustration opens in the browser, not in a screen.
		m.openIllustration(ref.ticket)
		return nil
	}
	next, ok := m.openTarget(*ref)
	if !ok {
		if why := opensNothing(*ref); why != "" {
			m.notice = why
		}
		return nil
	}
	return m.goTo(len(m.nav), ref, next)
}

// logNamesNothing is why enter opens nothing on an activity line that names no
// question, slice, round or step, and commitOpensNothing why it opens nothing on
// a store commit.
const (
	logNamesNothing    = "this line names no question, slice, round or step to open"
	commitOpensNothing = "a store commit opens nothing"
)

// opensNothing is why the row ref opens nothing, "" for a row that does open
// something or whose reason is the usual one.
func opensNothing(ref rowRef) string {
	switch {
	case ref.kind == rowCommit:
		return commitOpensNothing
	case ref.kind == rowLog && ref.slice == "" && ref.round == "" && ref.container == "":
		return logNamesNothing
	}
	return ""
}

// openTarget is the screen enter on the row ref opens: none when the row opens
// nothing, or opens the screen the person is on (an ask on the review screen,
// a slice's question on its own screen), which would stack a second copy.
func (m *Model) openTarget(ref rowRef) (navEntry, bool) {
	var next navEntry
	switch ref.kind {
	case rowFold:
		return next, false
	case rowChart:
		next = navEntry{screen: screenChart, chart: ref.id, follow: true}
	case rowTicket:
		next = navEntry{screen: screenTicket, ticket: ref.id, follow: true}
		if chart := m.planOf(ref.id); chart != "" {
			// An issue's screen is its plan, the screen of the chart that plans it.
			next = navEntry{screen: screenChart, chart: chart, follow: true}
		}
	case rowBrief:
		next = navEntry{screen: screenBrief, ticket: ref.ticket}
	case rowBuild:
		next = navEntry{screen: screenBuild, ticket: ref.ticket}
	case rowReview, rowAsk:
		next = navEntry{screen: screenReview, ticket: ref.ticket}
	case rowPublish:
		next = navEntry{screen: screenPublish, ticket: ref.ticket}
	case rowLand:
		next = navEntry{screen: screenLand, ticket: ref.ticket}
	case rowSlice:
		next = navEntry{screen: screenSlice, ticket: ref.ticket, slice: ref.id}
	case rowIssue:
		// An issue's screen is its plan, the screen of the chart that plans it.
		chart := m.planOf(ref.id)
		if chart == "" {
			return next, false
		}
		next = navEntry{screen: screenChart, chart: chart, follow: true}
	case rowSection:
		// A line that names a section opens the brief at it.
		next = navEntry{screen: screenBrief, ticket: ref.ticket, part: ref.identity(), partSet: true}
	case rowSub, rowLog:
		// A sub-step or a line of the log opens the most specific thing it names:
		// its slice, its round (the review screen, which has no screen of a round's
		// own), its step. A question of the line is its prompt while it is open
		// (promptFor) and its slice once answered.
		i := slices.IndexFunc(stepNames[:], func(n string) bool { return strings.ToLower(n) == ref.container })
		switch {
		case ref.slice != "":
			next = navEntry{screen: screenSlice, ticket: ref.ticket, slice: ref.slice}
		case ref.round != "":
			next = navEntry{screen: screenReview, ticket: ref.ticket}
		case i >= 0:
			next = navEntry{screen: stepScreens[i], ticket: ref.ticket}
			if next.screen == screenBrief && ref.kind == rowSub {
				// A brief sub-step opens the part it is about, else the first row.
				next.part, next.partSet = m.briefPartOf(ref.ticket, ref.id)
			}
		default:
			return next, false
		}
	case rowQuestion:
		if ref.slice == "" {
			return next, false
		}
		next = navEntry{screen: screenSlice, ticket: ref.ticket, slice: ref.slice}
	default:
		return next, false
	}
	top := m.top()
	same := next.screen == top.screen && next.ticket == top.ticket && next.slice == top.slice && next.chart == top.chart
	return next, !same
}

// askRow is the position, among the rows of the review screen on top, of the
// ask ref names, else of the first ask; 0 when it holds none.
func (m *Model) askRow(pg page, ref *rowRef) int {
	first := -1
	for i, r := range pg.refs() {
		if r.kind != rowAsk {
			continue
		}
		if ref != nil && ref.kind == rowAsk && r.id == ref.id {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return max(0, first)
}

// back goes up one screen.
func (m *Model) back() {
	if len(m.nav) > 1 {
		m.cut(len(m.nav) - 1)
		m.last = nil
	}
}

// refreshAll reads the ticket list and the open ticket's status again.
func (m *Model) refreshAll() tea.Cmd {
	var cmds []tea.Cmd
	if m.top().screen == screenMain && m.run != nil {
		// What merged on the host is asked in the background first; the roster
		// follows it.
		cmds = append(cmds, m.refreshMergesThenRoster())
	} else {
		cmds = append(cmds, m.loadRoster())
	}
	if e := m.top(); e.screen == screenChart {
		cmds = append(cmds, m.loadChart(e.chart))
		if c := m.w.charts[e.chart]; c != nil {
			for _, id := range c.ids() {
				cmds = append(cmds, m.refreshStatus(id))
			}
			cmds = append(cmds, m.refreshIssue(c))
		}
	}
	if id := m.watching(); id != "" {
		cmds = append(cmds, m.refreshStatus(id))
		if t := m.w.tickets[id]; t != nil {
			t.briefStale = true
		}
		if m.top().screen == screenBrief {
			cmds = append(cmds, m.loadBrief(id))
		}
	}
	return tea.Batch(cmds...)
}

// targetTicket is the ticket a key acts on: the open one, else the one under
// the cursor on the main screen and on a chart's.
func (m *Model) targetTicket() string {
	if t := m.top(); t.screen != screenMain && t.screen != screenChart {
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
	if ref := m.selected(); ref != nil && (ref.kind == rowQuestion || (ref.kind == rowLog && ref.question != "")) {
		qid := ref.id
		if ref.kind == rowLog {
			qid = ref.question
		}
		for _, x := range t.questions(m.zone) {
			if x.ID == qid {
				x := x
				q = &x
			}
		}
		if q == nil || !q.Open {
			m.notice = qid + " is already answered"
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
	m.input = &textInput{prompt: "answer " + q.ID, head: "slice " + q.Slice + " asked:", about: q.Text, ticket: id, qid: q.ID}
	// A question is never answered from a cut: when the prompt with the whole
	// question would leave the body under its floor, there is no prompt.
	if need := minBodyRows + headerRows + len(m.footer(m.width)); m.height < need {
		m.input = nil
		m.notice = "the screen is too short to show the whole question " + q.ID + ": it needs " + strconv.Itoa(need) + " rows, this has " + strconv.Itoa(m.height)
	}
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
	// A person decides an ask from what the screen shows: one drawn cut, or not
	// drawn at all, is not decided.
	switch drawn, seen := m.drawn[pick.ID]; {
	case m.drawn == nil:
		m.notice = "the screen changed under the key before it was drawn: press it again"
		return nil
	case drawn == askWhole:
	case seen && m.top().ticket == id:
		// The person is on the ticket's own screen: opening it is not a way out.
		m.notice = "ask " + pick.ID + " is not drawn whole on this screen: make the screen taller"
		return nil
	case seen:
		m.notice = "ask " + pick.ID + " is not drawn whole on this screen: make the screen taller, or open " + id + " to read it"
		return nil
	case m.top().ticket == id:
		m.notice = "ask " + pick.ID + " is read and decided on the review screen, which this screen does not draw it on"
		return nil
	default:
		m.notice = "ask " + pick.ID + " is not shown on this screen: open " + id + " to read it"
		return nil
	}
	return m.exec("triage", id, m.verbArgs("triage", id, pick.ID, decision))
}

// requeue puts a stalled or env-blocked slice back in the queue: the one the
// keys act on (the cursor's, or the slice screen's), else the only one. It runs
// at once: a requeue is a retry the builder's next claim takes.
func (m *Model) requeue() tea.Cmd {
	id := m.keysOn.ticket
	slice := m.keysOn.slice
	if slice == "" {
		// can said at least one is stuck.
		stuck := m.w.tickets[id].stuckSlices()
		if len(stuck) != 1 {
			m.notice = "several slices are stalled: put the cursor on the one to requeue"
			return nil
		}
		slice = stuck[0]
	}
	return m.exec("requeue", id, m.verbArgs("requeue", id, "--slice", slice))
}

// askApprove asks to approve the brief as it stands: the verb records the
// approval as the person's, since the runner has them at the keys.
func (m *Model) askApprove() {
	id := m.targetTicket()
	m.ask = &confirmation{
		prompt: "approve the brief of " + id + " as it is now?",
		run: func() tea.Cmd {
			return m.exec("validate", id, m.verbArgs("validate", id))
		},
	}
}

// gate opens a gate round on the ticket.
func (m *Model) gate() tea.Cmd {
	id := m.targetTicket()
	if id == "" || m.w.tickets[id] == nil {
		m.notice = "select a ticket first"
		return nil
	}
	if m.w.tickets[id].awaiting {
		m.notice = id + " does not exist yet"
		return nil
	}
	return m.exec("gate", id, m.verbArgs("gate", id))
}

// beginPublish renders the ticket's pull request body, which pushes nothing, to
// read what its What says: the verb's own word on it comes back as publishRead.
func (m *Model) beginPublish() tea.Cmd {
	id := m.targetTicket()
	if id == "" || m.w.tickets[id] == nil {
		m.notice = "select a ticket first"
		return nil
	}
	if m.w.tickets[id].awaiting {
		m.notice = id + " does not exist yet"
		return nil
	}
	return m.exec(labelPublishWhat, id, m.verbArgs("publish", id, "--body"))
}

// whatPending says a rendered body has a What nobody wrote: a `what` row (or one
// for each repo of a ticket of several) the verb says is pending.
func whatPending(out string) bool {
	for k, v := range parseAxi(out).KV["publish_body"] {
		if (k == "what" || strings.HasPrefix(k, "what_")) && v == "pending" {
			return true
		}
	}
	return false
}

// publishRead takes in the render beginPublish ran. With a What to write it
// stops: nothing is asked or pushed, and the panel keeps the verb's own help
// line for it, whole. A render the verb refused is shown as any refusal. A body
// with its What asks to publish, and the render leaves no line of its own.
func (m *Model) publishRead(res *outcome) {
	s := res.shown()
	if s.Code != 0 || whatPending(s.Out) {
		return
	}
	m.last = nil
	m.askPublish(res.Ticket)
}

// askPublish asks to publish the ticket: a person's approval of the
// reviewed head, then the push and the pull request.
func (m *Model) askPublish(id string) {
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
