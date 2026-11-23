package mergefacts

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestPRCacheReadOnceReadsTheFileOnceAndAsksNoHost: with ReadOnce the file is
// read at the first question and not again, so the cost of a pull request's
// state does not grow with the file; with no Ask the host is never asked and
// a state kept is given, dated, and one never kept reads unknown. Concurrent
// readers get the same answers.
func TestPRCacheReadOnceReadsTheFileOnceAndAsksNoHost(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pull-requests.json")
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	write := func(state string) {
		t.Helper()
		body := `{"pull_requests":{"https://github.com/acme/app/pull/1":{"state":"` + state + `","at":"` + at.Format(time.RFC3339) + `"}}}`
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("OPEN")
	c := &PRCache{Path: path, ReadOnce: true}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := c.Read("https://github.com/acme/app/pull/1")
			if got.State != "OPEN" || !got.AsOf.Equal(at) || !got.Offline {
				t.Errorf("a kept state = %+v, want OPEN as of %s, offline", got, at)
			}
			if got := c.Read("https://github.com/acme/app/pull/2"); got.State != "" || !got.Offline {
				t.Errorf("a state never kept = %+v, want unknown", got)
			}
		}()
	}
	wg.Wait()
	write("MERGED")
	if got := c.Read("https://github.com/acme/app/pull/1"); got.State != "OPEN" {
		t.Errorf("the file was read again: %+v", got)
	}
	fresh := &PRCache{Path: path}
	if got := fresh.Read("https://github.com/acme/app/pull/1"); got.State != "MERGED" {
		t.Errorf("a cache without ReadOnce missed the file's change: %+v", got)
	}
}
