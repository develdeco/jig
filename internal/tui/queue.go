package tui

import (
	"slices"
	"strings"
	"sync"
)

// class is how much a command waits on: the commands the screens run are not
// equal. A key is the person acting, the screen the person opened is the one
// they are looking at, and every other read keeps what is on show warm. The
// queue takes the highest class waiting first, so a person never waits on a
// list read that nobody is looking at.
type class int

const (
	// classBackground is a read nobody asked for by name: the roster, a status
	// or a chart kept warm.
	classBackground class = iota
	// classOpened is a read about the screen the person opened: the ticket's
	// status, the chart, the brief. Whether a read is of the screen on show is
	// told when the queue picks the next command, not when the read was asked
	// for: a read waiting for a screen the person has left is a background read,
	// and one waiting for the screen they have just opened is the next.
	classOpened
	// classKey is a command a key started: an answer, a triage, a publish, a
	// gate. It is never dropped and never merged with another.
	classKey
)

// classedRunner runs a command in its class; about is what a classOpened read is
// of.
type classedRunner func(c class, about about, args []string) (output string, code int)

// about is what a read concerns: the ticket whose status or brief it reads, or
// the chart it reads.
type about struct{ ticket, chart string }

// onShow is the screen the person is looking at, as far as a read can be of it:
// its ticket, or its chart and the tickets and issue the chart lists.
type onShow struct {
	ticket, chart string
	chartTickets  []string
}

// concerns says a read about a is of the screen on show.
func (s onShow) concerns(a about) bool {
	if a.chart != "" && a.chart == s.chart {
		return true
	}
	if a.ticket == "" {
		return false
	}
	return a.ticket == s.ticket || slices.Contains(s.chartTickets, a.ticket)
}

// queueJob is one command waiting for its turn, or running.
type queueJob struct {
	class class
	about about
	args  []string
	line  string
	done  chan struct{}
	out   string
	code  int
}

// queue is a Runner made safe for concurrent use that runs one command at a
// time, as the commands of one store must (a status read for a screen never
// overlaps a publish that a key started), and chooses which waits next:
//
//   - the next to run is the earliest of the highest class waiting, a read's
//     class being told as the screen on show stands when the next is picked;
//   - a command that is running is not interrupted;
//   - a key's command is never dropped;
//   - a background read asked for while the same command line waits adds
//     nothing: its asker takes the answer of the one waiting. Asked for while
//     the same line runs, it is asked again once, after the one running, so a
//     change that came in meanwhile is read.
type queue struct {
	run Runner

	mu      sync.Mutex
	screen  onShow
	waiting []*queueJob
	running bool
	// started counts the commands begun, for a test.
	started int
}

// show says which screen is on show: the reads about it run before the reads
// that keep the rest warm, whenever they were asked for.
func (q *queue) show(s onShow) {
	q.mu.Lock()
	q.screen = s
	q.mu.Unlock()
}

// effective is the class a waiting job has now; the caller holds q.mu.
func (q *queue) effective(j *queueJob) class {
	if j.class == classOpened && !q.screen.concerns(j.about) {
		return classBackground
	}
	return j.class
}

// do puts the command in the queue and returns what it printed once it ran.
func (q *queue) do(c class, about about, args []string) (string, int) {
	line := strings.Join(args, " ")
	q.mu.Lock()
	var job *queueJob
	if c != classKey {
		for _, w := range q.waiting {
			if w.class != classKey && w.line == line {
				job = w
				if c > w.class {
					w.class = c
				}
				break
			}
		}
	}
	if job == nil {
		job = &queueJob{class: c, about: about, args: args, line: line, done: make(chan struct{})}
		q.waiting = append(q.waiting, job)
	}
	if !q.running {
		q.running = true
		go q.drain()
	}
	q.mu.Unlock()
	<-job.done
	return job.out, job.code
}

// drain runs the waiting commands, the next by class, until none waits.
func (q *queue) drain() {
	for {
		q.mu.Lock()
		if len(q.waiting) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		best := 0
		for i, w := range q.waiting {
			if q.effective(w) > q.effective(q.waiting[best]) {
				best = i
			}
		}
		job := q.waiting[best]
		q.waiting = append(q.waiting[:best], q.waiting[best+1:]...)
		q.started++
		q.mu.Unlock()

		job.out, job.code = q.run(job.args)
		close(job.done)
	}
}
