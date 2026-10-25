package mergefacts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/repohost"
)

// asker is a host that answers from a table and counts what it was asked.
type asker struct {
	state map[string]string
	asked []string
	down  bool
}

func (a *asker) ask(url string) (string, bool) {
	a.asked = append(a.asked, url)
	if a.down {
		return "", false
	}
	s, ok := a.state[url]
	return s, ok
}

// cache is a PRCache over the asker, kept in a file of its own, with a clock the
// test moves.
func cache(a *asker, path string, maxAge time.Duration, now *time.Time) *PRCache {
	return &PRCache{Ask: a.ask, Path: path, MaxAge: maxAge, Now: func() time.Time { return *now }}
}

// TestPRCacheAsksOncePerRun: a pull request is asked about once in a run, and
// the answer is the host's own, not dated.
func TestPRCacheAsksOncePerRun(t *testing.T) {
	t.Parallel()
	a := &asker{state: map[string]string{"u": repohost.StateOpen}}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	c := cache(a, "", 0, &now)
	for i := 0; i < 3; i++ {
		if got := c.Read("u"); got != (PRRead{State: repohost.StateOpen}) {
			t.Fatalf("Read #%d = %+v, want open, read now", i, got)
		}
	}
	if len(a.asked) != 1 {
		t.Errorf("the host was asked %d times in one run, want once", len(a.asked))
	}
}

// TestPRCacheKeepsWhatItLearned: a merged answer is kept for good and never
// asked again; an open one is asked again every time with no MaxAge, and once
// older than MaxAge with one; the answer kept is dated.
func TestPRCacheKeepsWhatItLearned(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache", "pull-requests.json")
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	now := start
	host := &asker{state: map[string]string{"merged": repohost.StateMerged, "open": repohost.StateOpen}}

	// Run 1 learns both.
	run1 := cache(host, path, 0, &now)
	run1.Read("merged")
	run1.Read("open")
	if len(host.asked) != 2 {
		t.Fatalf("run 1 asked %v, want both", host.asked)
	}

	// Run 2, an hour later, with no MaxAge: the merged one is not asked again
	// and keeps its date; the open one is asked again.
	now = start.Add(time.Hour)
	host.asked = nil
	run2 := cache(host, path, 0, &now)
	if got := run2.Read("merged"); got != (PRRead{State: repohost.StateMerged, AsOf: start}) {
		t.Errorf("merged, a run later = %+v, want merged as of the first run", got)
	}
	if got := run2.Read("open"); got != (PRRead{State: repohost.StateOpen}) {
		t.Errorf("open, with no MaxAge = %+v, want it read now", got)
	}
	if strings.Join(host.asked, ",") != "open" {
		t.Errorf("run 2 asked %v, want only the open one", host.asked)
	}

	// Run 3, ten minutes after run 2, with a MaxAge of an hour: the open
	// answer stands, dated; at two hours it is asked again.
	now = start.Add(time.Hour + 10*time.Minute)
	host.asked = nil
	run3 := cache(host, path, time.Hour, &now)
	if got := run3.Read("open"); got != (PRRead{State: repohost.StateOpen, AsOf: start.Add(time.Hour)}) {
		t.Errorf("open within MaxAge = %+v, want the kept answer, dated", got)
	}
	if len(host.asked) != 0 {
		t.Errorf("an answer within MaxAge asked the host: %v", host.asked)
	}
	now = start.Add(3 * time.Hour)
	run4 := cache(host, path, time.Hour, &now)
	if got := run4.Read("open"); got != (PRRead{State: repohost.StateOpen}) || len(host.asked) != 1 {
		t.Errorf("open past MaxAge = %+v after asking %v, want it asked again and read now", got, host.asked)
	}
}

// TestPRCacheWhenTheHostCannotBeAsked: the last answer is given, dated and
// marked offline; with nothing kept, there is no state. Nothing is kept of a
// failure.
func TestPRCacheWhenTheHostCannotBeAsked(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pull-requests.json")
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	now := start
	host := &asker{state: map[string]string{"known": repohost.StateOpen}}
	cache(host, path, 0, &now).Read("known")

	now = start.Add(time.Hour)
	host.down = true
	c := cache(host, path, 0, &now)
	if got := c.Read("known"); got != (PRRead{State: repohost.StateOpen, AsOf: start, Offline: true}) {
		t.Errorf("a known pull request, host down = %+v, want the old answer, dated and offline", got)
	}
	if got := c.Read("never-seen"); got != (PRRead{Offline: true}) {
		t.Errorf("an unknown pull request, host down = %+v, want no state, offline", got)
	}
	host.down = false
	host.state["never-seen"] = repohost.StateClosed
	if got := cache(host, path, 0, &now).Read("never-seen"); got.State != repohost.StateClosed || got.Offline {
		t.Errorf("after the host came back = %+v, want it read", got)
	}
}

// TestPRCacheToleratesAFileItCannotRead: a file that is not the cache holds
// nothing and is written over by the next answer; with no path nothing is
// written at all.
func TestPRCacheToleratesAFileItCannotRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pull-requests.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	host := &asker{state: map[string]string{"u": repohost.StateMerged}}
	if got := cache(host, path, 0, &now).Read("u"); got.State != repohost.StateMerged {
		t.Fatalf("Read over a corrupt file = %+v, want the host's answer", got)
	}
	again := &asker{}
	if got := cache(again, path, 0, &now).Read("u"); got.State != repohost.StateMerged || len(again.asked) != 0 {
		t.Errorf("Read after the rewrite = %+v asking %v, want the kept merged answer", got, again.asked)
	}

	kept := &asker{state: map[string]string{"u": repohost.StateMerged}}
	cache(kept, "", 0, &now).Read("u")
	cache(kept, "", 0, &now).Read("u")
	if len(kept.asked) != 2 {
		t.Errorf("a cache with no path kept an answer from one run to the next: the host was asked %d times, want 2", len(kept.asked))
	}
}

// TestPRCacheStopsAskingADeadHost: asks that fail slowly are paid for once: when
// they have taken slowFailures in all, the host is taken as down and the rest of
// the run is answered from what was kept (a failure that is quick, such as a
// pull request the host does not hold, costs nothing).
func TestPRCacheStopsAskingADeadHost(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pull-requests.json")
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	now := start
	kept := &asker{state: map[string]string{"old": repohost.StateOpen}}
	cache(kept, path, 0, &now).Read("old")

	now = start.Add(time.Hour)
	var asked []string
	c := &PRCache{Path: path, Now: func() time.Time { return now }, Ask: func(url string) (string, bool) {
		asked = append(asked, url)
		now = now.Add(10 * time.Second) // a call that hangs before it fails
		return "", false
	}}
	for _, url := range []string{"a", "b", "c", "old"} {
		c.Read(url)
	}
	if strings.Join(asked, ",") != "a,b" {
		t.Errorf("the host was asked %v, want a and b only: 20s of failures took it as down", asked)
	}
	if got := c.Read("old"); got != (PRRead{State: repohost.StateOpen, AsOf: start, Offline: true}) {
		t.Errorf("a kept answer once the host is down = %+v, want it, dated and offline", got)
	}
}
