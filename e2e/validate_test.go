package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// TestValidateFixture asserts `jig validate` accepts the fixture ticket as
// committed: a well-formed brief, slices.yaml matching the current brief
// section hashes, resolvable blocked_by ids with no cycles, workspace ids
// present in the repo manifest, and non-empty oracles.
func TestValidateFixture(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{})

	r := runJig(t, fx.StoreDir, "validate", fx.Ticket)
	if r.Code != 0 {
		t.Fatalf("jig validate exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("stdout missing \"valid: yes\":\n%s", r.Stdout)
	}
}
