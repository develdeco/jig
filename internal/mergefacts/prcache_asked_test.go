package mergefacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTheHostDidNotAnswerOnlyIfItWasAsked: a kept state read where no host is
// asked (no Ask, or Cached) is dated and is not offline; one read after the host
// was asked and failed is offline.
func TestTheHostDidNotAnswerOnlyIfItWasAsked(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pull-requests.json")
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	body := `{"pull_requests":{"https://github.com/acme/app/pull/1":{"state":"OPEN","at":"` + at.Format(time.RFC3339) + `"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/acme/app/pull/1"
	never := &PRCache{Path: path}
	if got := never.Read(url); got.Offline || got.State != "OPEN" || !got.AsOf.Equal(at) {
		t.Errorf("Read with no host to ask = %+v, want the kept state, dated, not offline", got)
	}
	if got := never.Read("https://github.com/acme/app/pull/2"); got.Offline || got.State != "" {
		t.Errorf("Read of a state never kept with no host = %+v, want unknown and not offline", got)
	}
	if got := never.Cached(url); got.Offline || got.State != "OPEN" {
		t.Errorf("Cached = %+v, want the kept state, not offline", got)
	}
	failed := &PRCache{Path: path, Ask: func(string) (string, bool) { return "", false }}
	if got := failed.Read(url); !got.Offline || got.State != "OPEN" {
		t.Errorf("Read after the host failed = %+v, want the kept state, offline", got)
	}
}
