package events

import (
	"context"
	"time"
)

// PollInterval is how often Follow reads the scope again.
const PollInterval = 250 * time.Millisecond

// maxPollFailures is how many polls in a row may fail before Follow gives up
// with the error: a file replaced at the moment it is opened (a sharing
// violation on Windows) fails one poll and not the next, but a permission
// that is gone fails them all.
const maxPollFailures = 40

// FollowOptions are Follow's settings.
type FollowOptions struct {
	// Since leaves out lines stamped before it (zero: none).
	Since time.Time
	// Interval is the time between polls; zero is PollInterval.
	Interval time.Duration
}

// Follow emits the stream of scope - the lines in it now, oldest first, then
// each line added to it, as the poll that finds it sorts them - until ctx
// ends (it then returns nil) or emit returns an error (which it returns).
//
// Each poll opens, reads and closes every file; none is held between polls,
// so a writer that replaces a journal (Windows will not rename over an open
// file) is never in its way. A line is told from one already emitted by its
// hash, per file, and the ledger by the offset it was read to. A line that
// reaches the scope late - a merge that brought another machine's lines in -
// is emitted when it is first seen, out of order with those already emitted.
func Follow(ctx context.Context, scope Scope, o FollowOptions, emit func(Envelope) error) error {
	interval := o.Interval
	if interval <= 0 {
		interval = PollInterval
	}
	r := NewReader(scope, o.Since)
	timer := time.NewTimer(interval)
	defer timer.Stop()

	failures := 0
	for {
		envs, err := r.Poll()
		for _, e := range envs {
			if err := emit(e); err != nil {
				return err
			}
		}
		if err == nil {
			failures = 0
		} else if failures++; failures >= maxPollFailures {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			timer.Reset(interval)
		}
	}
}
