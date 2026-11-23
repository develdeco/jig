package tui

import (
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
	// classOpened is a read of the screen the person opened: the ticket's
	// status, the chart, the brief.
	classOpened
	// classKey is a command a key started: an answer, a triage, a publish, a
	// gate. It is never dropped and never merged with another.
	classKey
)

// classedRunner runs a command in its class.
type classedRunner func(c class, args []string) (output string, code int)

// queueJob is one command waiting for its turn, or running.
type queueJob struct {
	class class
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
//   - the next to run is the earliest of the highest class waiting;
//   - a command that is running is not interrupted;
//   - a key's command is never dropped;
//   - a background read asked for while the same command line waits adds
//     nothing: its asker takes the answer of the one waiting. Asked for while
//     the same line runs, it is asked again once, after the one running, so a
//     change that came in meanwhile is read.
type queue struct {
	run Runner

	mu      sync.Mutex
	waiting []*queueJob
	running bool
	// started counts the commands begun, for a test.
	started int
}

// do puts the command in the queue and returns what it printed once it ran.
func (q *queue) do(c class, args []string) (string, int) {
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
		job = &queueJob{class: c, args: args, line: line, done: make(chan struct{})}
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
			if w.class > q.waiting[best].class {
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
