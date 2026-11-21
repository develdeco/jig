package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gittest"
)

// originFetches counts the processes in argv that ask origin for its refs: a
// fetch of origin, whatever refspec it names.
func originFetches(argv [][]string) int {
	n := 0
	for _, a := range argv {
		line := " " + strings.Join(a, " ") + " "
		if strings.Contains(line, " fetch ") && strings.Contains(line, " origin ") {
			n++
		}
	}
	return n
}

// TestPublishFetchesOriginOnce: a publish asks origin for its refs once, in
// the clone, whose copy of the branch it chooses after that fetch. Not
// parallel: it counts through the process's trace.
func TestPublishFetchesOriginOnce(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
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
}
