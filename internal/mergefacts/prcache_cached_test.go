package mergefacts

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/repohost"
)

// TestPRCacheCachedNeverAsks: Cached gives this run's answer, else the kept
// one however old (dated), else nothing, and asks the host about none of them.
func TestPRCacheCachedNeverAsks(t *testing.T) {
	t.Parallel()
	a := &asker{state: map[string]string{"asked": repohost.StateOpen, "kept": repohost.StateOpen}}
	path := filepath.Join(t.TempDir(), "prs.json")
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cache(a, path, time.Minute, &now).Read("kept")

	now = now.Add(time.Hour)
	c := cache(a, path, time.Minute, &now)
	if got := c.Read("asked"); got.State != repohost.StateOpen {
		t.Fatalf("Read = %+v", got)
	}
	a.asked = nil
	if got := c.Cached("asked"); got.State != repohost.StateOpen || !got.AsOf.IsZero() {
		t.Errorf("this run's answer = %+v, want open, undated", got)
	}
	if got := c.Cached("kept"); got.State != repohost.StateOpen || got.AsOf.IsZero() {
		t.Errorf("a kept answer = %+v, want open, dated, though older than MaxAge", got)
	}
	if got := c.Cached("never"); got.State != "" {
		t.Errorf("an unknown pull request = %+v, want no state", got)
	}
	if len(a.asked) != 0 {
		t.Errorf("Cached asked the host about %v", a.asked)
	}
}
