package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/pool"
)

// originFetches counts the processes in argv that ask origin for its refs: a
// fetch of origin, and the clone that makes a lease (which fetches it all).
func originFetches(argv [][]string) int {
	n := 0
	for _, a := range argv {
		line := " " + strings.Join(a, " ") + " "
		fetch := len(a) > 2 && a[len(a)-1] == "origin" && strings.Contains(line, " fetch ")
		if fetch || strings.Contains(line, " clone ") {
			n++
		}
	}
	return n
}

// TestPublishFetchesOriginOnce: a publish asks origin for its refs once,
// whether its lease is a clone made now or one an earlier run left, where it
// used to ask twice for the one it reused. Not parallel: it counts through the
// process's trace.
func TestPublishFetchesOriginOnce(t *testing.T) {
	for _, c := range []struct {
		name   string
		reused bool
	}{{"a fresh lease", false}, {"a reused lease", true}} {
		t.Run(c.name, func(t *testing.T) {
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			gateToClean(t, fx, d)
			if c.reused {
				if _, err := pool.Acquire(d.Home, "fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket, pool.Publish); err != nil {
					t.Fatalf("Acquire the publish lease: %v", err)
				}
			}
			var err error
			argv := gittest.GitArgv(t, func() {
				_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
			})
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if n := originFetches(argv); n != 1 {
				t.Errorf("publish fetched from origin %d times, want 1: %v", n, argv)
			}
		})
	}
}
