package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// writersFixture is the fixture with its store clone writing as alice.
func writersFixture(t *testing.T) (*fixture.Fixture, *store.Store, env) {
	t.Helper()
	fx, st, e := workFixture(t)
	if _, err := gitx.Run(fx.StoreDir, "config", "jig.writer", "alice"); err != nil {
		t.Fatal(err)
	}
	return fx, st, e
}

// TestStoreMigrateWritersDryRunChangesNothing: --dry-run says what the move
// would do and leaves the store on main with no claims branch.
func TestStoreMigrateWritersDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	fx, st, e := writersFixture(t)

	out, code := runMain(t, e, "", "store", "migrate", "--writers", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"writers:\n", "  writer: alice\n", "  branch: writers/alice\n", "  claims: to create\n", "  ids: 1\n", "Nothing was changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if on, _ := st.Mode(); on {
		t.Error("a dry run made the store a writers store")
	}
	if got, _ := gitx.Run(fx.StoreDir, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("a dry run left the store on %q", got)
	}
}

// TestStoreMigrateWriters: the move makes the claims branch with an id line
// per ticket, puts the clone on its writer branch, says so, is refused for a
// dirty store, and a second run joins instead of making it again.
func TestStoreMigrateWriters(t *testing.T) {
	t.Parallel()
	fx, st, e := writersFixture(t)

	// A dirty store is refused before anything happens.
	stray := filepath.Join(fx.StoreDir, "stray.txt")
	if err := os.WriteFile(stray, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, e, "", "store", "migrate", "--writers")
	if code == 0 || !strings.Contains(out, "STORE_CONFLICT") || !strings.Contains(out, "uncommitted") {
		t.Fatalf("migrate --writers of a dirty store: exit %d, want a STORE_CONFLICT\n%s", code, out)
	}
	if on, _ := st.Mode(); on {
		t.Fatal("a refused migration made the store a writers store")
	}
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}

	out, code = runMain(t, e, "", "store", "migrate", "--writers")
	if code != 0 {
		t.Fatalf("migrate --writers: exit %d\n%s", code, out)
	}
	for _, want := range []string{"writers:\n", "  writer: alice\n", "  branch: writers/alice\n", "  claims: created\n", "  ids: 1\n", "main is never pushed again"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if on, _ := st.Mode(); !on {
		t.Error("the store is not in writers mode after the move")
	}
	if got, _ := gitx.Run(fx.StoreDir, "symbolic-ref", "--short", "HEAD"); got != "writers/alice" {
		t.Errorf("the store is on %q, want writers/alice", got)
	}
	if ids, err := st.Claims(); err != nil || len(ids) != 1 || ids[0].Ticket != fx.Ticket || ids[0].Kind != store.ClaimID {
		t.Errorf("claims = %+v, %v; want one id line for %s", ids, err, fx.Ticket)
	}

	out, code = runMain(t, e, "", "store", "migrate", "--writers")
	if code != 0 || !strings.Contains(out, "  claims: joined\n") {
		t.Errorf("second migrate --writers: exit %d, want it to join\n%s", code, out)
	}
	// Ordinary commands go on working in a writers store, on its branch.
	if out, code := runMain(t, e, "", "ticket", "new", "--title", "after the move"); code != 0 || !strings.Contains(out, "id: JIG-2") {
		t.Errorf("ticket new in a writers store: exit %d\n%s", code, out)
	}
	if got, _ := gitx.Run(fx.StoreDir, "ls-remote", "--heads", "origin", "writers/alice"); !strings.Contains(got, "refs/heads/writers/alice") {
		t.Errorf("the ticket was not pushed to writers/alice: %q", got)
	}
}

// TestStoreMigrateWritersTakesNoSchemaFlags: --writers does not change the
// schema, so --map and --to are refused with it.
func TestStoreMigrateWritersTakesNoSchemaFlags(t *testing.T) {
	t.Parallel()
	_, st, e := writersFixture(t)
	for _, args := range [][]string{
		{"store", "migrate", "--writers", "--to", "3"},
		{"store", "migrate", "--writers", "--map", "x.yaml"},
	} {
		out, code := runMain(t, e, "", args...)
		if code == 0 || !strings.Contains(out, "VALIDATION_ERROR") || !strings.Contains(out, "does not change its schema_version") {
			t.Errorf("jig %s: exit %d, want a VALIDATION_ERROR\n%s", strings.Join(args, " "), code, out)
		}
	}
	if on, _ := st.Mode(); on {
		t.Error("a refused invocation made the store a writers store")
	}
}

// TestStoreMigrateWritersNeedsAnOrigin: a store with no origin is refused.
func TestStoreMigrateWritersNeedsAnOrigin(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{Standalone: true})
	if _, err := gitx.Run(fx.StoreDir, "config", "jig.writer", "alice"); err != nil {
		t.Fatal(err)
	}
	e := testEnv(fx.Home).inDir(fx.RepoDir)
	out, code := runMain(t, e, "", "store", "migrate", "--writers")
	if code == 0 || !strings.Contains(out, "no origin") {
		t.Errorf("exit %d, want a refusal naming the missing origin\n%s", code, out)
	}
}
