package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// TestReservedLeaseSuffixTicketRefused reproduces the lease-key collision:
// ticket X's gate lease is pool/<repo>/X-gate and its publish lease is
// pool/<repo>/X-publish, so a ticket named X-gate would build in the
// directory X's gate resets and cleans. Every command that takes a ticket
// must refuse such an id before any lease exists.
func TestReservedLeaseSuffixTicketRefused(t *testing.T) {
	fx, home := newFixture(t, fixture.Opts{})

	for _, id := range []string{fx.Ticket + "-gate", fx.Ticket + "-GATE", fx.Ticket + "-publish"} {
		src := filepath.Join(fx.StoreDir, fx.Ticket)
		dst := filepath.Join(fx.StoreDir, id)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"brief.md", "slices.yaml"} {
			data, err := os.ReadFile(filepath.Join(src, name))
			if err != nil {
				t.Fatalf("read fixture %s: %v", name, err)
			}
			if err := os.WriteFile(filepath.Join(dst, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		for _, args := range [][]string{
			{"validate", id},
			{"run", id, "--backend", "fake", "--scenario", fx.ScenarioDir},
			{"gate", id, "--scenario", fx.ScenarioDir},
			{"publish", id, "--yes"},
			{"status", id},
		} {
			r := runJig(t, fx.StoreDir, args...)
			if r.Code == 0 || !strings.Contains(r.Stdout, "reserves") || !strings.Contains(r.Stdout, id) {
				t.Errorf("jig %s: exit %d, want a non-zero exit naming %s as a reserved id\nstdout:\n%s\nstderr:\n%s",
					strings.Join(args, " "), r.Code, id, r.Stdout, r.Stderr)
			}
		}

		if _, err := os.Stat(poolBuildLeaseDir(home, "fixture-repo", id)); !os.IsNotExist(err) {
			t.Errorf("a lease exists at %s after refusing ticket %s (stat err %v)", poolBuildLeaseDir(home, "fixture-repo", id), id, err)
		}
	}
}
