package mergefacts

import (
	"encoding/json"
	"os"
	"time"

	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// PRRead is one answer about a pull request's state.
type PRRead struct {
	// State is repohost.StateOpen, StateClosed or StateMerged, "" when the
	// state is not known (the host could not be asked and nothing was kept).
	State string
	// AsOf is when State was read, when this run did not read it itself; the
	// zero time for an answer the host gave now.
	AsOf time.Time
	// Offline says the host could not be asked, so State is the last answer it
	// gave (or "" when it never did).
	Offline bool
}

// PRs says what state a pull request is in.
type PRs interface {
	Read(url string) PRRead
}

// PRCache reads pull request states from the repo host and remembers what it
// was told, so that a command asking about many tickets does not ask the
// network for each one:
//
//   - in this run, a pull request is asked about once;
//   - a merged pull request stays merged, so an answer of merged is kept on
//     this machine for good and never asked again;
//   - an open or closed answer is kept too, and stands for MaxAge before the
//     host is asked again (0: it never stands, the host is asked every time);
//   - when the host cannot be asked, the last answer is given, dated, instead
//     of none; and once asking has failed for slowFailures in all, in this run,
//     the host is taken as down and not asked again, so a dead network costs a
//     command that long and no more.
//
// The file is this machine's, never the store's: it is the host's word as of a
// time, and deleting it costs only the next reads. Not safe for concurrent
// use.
type PRCache struct {
	// Ask reads one state from the host: repohost.PRState with the gh program
	// and environment of the command.
	Ask func(url string) (state string, known bool)
	// Path is the file the answers are kept in, "" to keep none.
	Path string
	// MaxAge is how long an open or closed answer stands.
	MaxAge time.Duration
	// Now is the clock, time.Now when nil.
	Now func() time.Time

	run    map[string]PRRead
	failed time.Duration
}

// slowFailures is how long asks that failed may take in all, in one run, before
// the host is taken as down for the rest of it.
const slowFailures = 15 * time.Second

// prRecord is one kept answer.
type prRecord struct {
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

// prFile is the wire shape of the cache file.
type prFile struct {
	PullRequests map[string]prRecord `json:"pull_requests"`
}

// Read says what state the pull request at url is in, by the rules of PRCache.
func (c *PRCache) Read(url string) PRRead {
	if got, ok := c.run[url]; ok {
		return got
	}
	now := c.now()
	kept, have := c.load()[url]
	if have && (kept.State == repohost.StateMerged || (c.MaxAge > 0 && now.Sub(kept.At) < c.MaxAge)) {
		return PRRead{State: kept.State, AsOf: kept.At}
	}
	if c.failed < slowFailures {
		if state, known := c.ask(url); known {
			got := PRRead{State: state}
			c.remember(url, prRecord{State: state, At: now})
			if c.run == nil {
				c.run = map[string]PRRead{}
			}
			c.run[url] = got
			return got
		}
		c.failed += c.now().Sub(now)
	}
	if have {
		return PRRead{State: kept.State, AsOf: kept.At, Offline: true}
	}
	return PRRead{Offline: true}
}

func (c *PRCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *PRCache) ask(url string) (string, bool) {
	if c.Ask == nil {
		return "", false
	}
	return c.Ask(url)
}

// load reads the cache file. A file that is missing, or that does not parse,
// holds nothing: it is a cache.
func (c *PRCache) load() map[string]prRecord {
	if c.Path == "" {
		return nil
	}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return nil
	}
	var f prFile
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	return f.PullRequests
}

// remember keeps one answer: the file is read again first, so another jig's
// answers written since are not lost, and written atomically. A write that
// fails is dropped; the next run asks again.
func (c *PRCache) remember(url string, rec prRecord) {
	if c.Path == "" {
		return
	}
	kept := c.load()
	if kept == nil {
		kept = map[string]prRecord{}
	}
	kept[url] = rec
	data, err := json.MarshalIndent(prFile{PullRequests: kept}, "", "  ")
	if err != nil {
		return
	}
	_ = store.AtomicWrite(c.Path, append(data, '\n'))
}

var _ PRs = (*PRCache)(nil)
