package verify

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// TestRunPushesTheLinesAndTheStateToTheStoreRemote: the pending line is pushed
// when it is written and the outcome with the slice state at the end, so a
// reader on another machine sees both from the remote alone.
func TestRunPushesTheLinesAndTheStateToTheStoreRemote(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	before, err := gitx.RevParse(r.fx.StoreRemote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	res, err := Run(r.deps, r.request("a", sha))
	if err != nil || res.State != journal.VerifyPass || res.PushErr != "" {
		t.Fatalf("Run = %+v, %v, want a pass pushed", res, err)
	}
	after, err := gitx.RevParse(r.fx.StoreRemote, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("the store remote did not move: nothing was pushed")
	}
	// Two checkpoints: the pending line, then the outcome.
	if n, err := gitx.Run(r.fx.StoreRemote, "rev-list", "--count", before+".."+after); err != nil || n != "2" {
		t.Errorf("commits pushed = %q, %v, want 2: pending and the outcome", n, err)
	}
	if msg, _ := gitx.Run(r.fx.StoreRemote, "log", "-1", "--format=%s"); msg == "" {
		t.Error("the pushed commit has no message")
	}
	body, err := gitx.Run(r.fx.StoreRemote, "show", "HEAD:tickets/"+r.fx.Ticket+"/journal.ndjson")
	if err != nil {
		// The store layout may keep the ticket at its root; either way the
		// journal is in the commit.
		body, err = gitx.Run(r.fx.StoreRemote, "show", "HEAD:"+r.fx.Ticket+"/journal.ndjson")
	}
	if err != nil || !strings.Contains(body, `"event":"verified"`) {
		t.Errorf("the pushed journal lacks the verified line: %v\n%s", err, body)
	}
}

// TestRunKeepsTheLinesWhenThePushNeverGoesThrough: a store remote that is
// gone fails every push; the run tries three times a push, waiting a little
// longer each time, then leaves the commit local, reports why, and still
// records the whole verification.
func TestRunKeepsTheLinesWhenThePushNeverGoesThrough(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	if err := os.RemoveAll(r.fx.StoreRemote); err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	r.deps.Sleep = func(d time.Duration) { waits = append(waits, d) }

	res, err := Run(r.deps, r.request("a", sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyPass || res.PushErr == "" {
		t.Errorf("Result = %+v, want a pass with the push error", res)
	}
	if got, want := events(r.lines()), []string{"verify-pending", "oracle:pass", "verified"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal events = %v, want %v", got, want)
	}
	if st := r.state("a"); st.State != "green" {
		t.Errorf("slice a state = %+v, want green", st)
	}
	// Two checkpoints, each tried three times with two waits between.
	half := 500 * time.Millisecond
	if want := []time.Duration{half, 2 * half, half, 2 * half}; !reflect.DeepEqual(waits, want) {
		t.Errorf("waits = %v, want %v", waits, want)
	}
	if !strings.Contains(r.log.String(), "push failed after 3 tries") {
		t.Errorf("the log does not say the push failed:\n%s", r.log.String())
	}
}
