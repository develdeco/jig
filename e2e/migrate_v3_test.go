package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/project"
)

// TestStoreMigrateV2ToV3AndV2StillOpens: a v2 store (the fixture is pinned at
// schema 2) opens unchanged under a jig whose current schema is 3 - status,
// here and ticket new all work on it - and `jig store migrate` takes it to v3
// as one step: store.yaml replaces project.yaml in one commit authored by the
// person, and the project's jig.yaml lands uncommitted in the mapped clone.
// Every verb then works on the v3 store, from the same clone, with the repos
// the marker names. Only the next step is ever applied, a flag the step does
// not use is refused, and nothing is half done.
func TestStoreMigrateV2ToV3AndV2StillOpens(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})

	// v2 opens.
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "project.yaml")); err != nil {
		t.Fatalf("the fixture store is not a v2 store: %v", err)
	}
	if r := projRun(t, env, fx.RepoDir, "status", fx.Ticket); !strings.Contains(r.Stdout, "ticket: JIG-1") {
		t.Errorf("jig status on the v2 store:\n%s", r.Stdout)
	}
	if got := hereJSON(t, env, fx.RepoDir); got.Project != "fixture" || got.Repo.Name != "fixture-repo" {
		t.Errorf("jig here on the v2 store: project %q, repo %+v", got.Project, got.Repo)
	}
	if r := projRun(t, env, fx.RepoDir, "ticket", "new", "--title", "Minted on v2"); !strings.Contains(r.Stdout, "JIG-2") {
		t.Errorf("jig ticket new on the v2 store:\n%s", r.Stdout)
	}
	// ticket new committed and pushed; the store is level with origin again.
	gitLog(t, fx.StoreDir, "fetch", "origin")

	// The dry run says what the step does and changes nothing.
	r := projRun(t, env, fx.RepoDir, "store", "migrate", "--dry-run")
	for _, want := range []string{"files[2]{action,path}:", "move,store.yaml", "write,", "jig.yaml", "Nothing was changed"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig store migrate --dry-run stdout lacks %q:\n%s", want, r.Stdout)
		}
	}
	if strings.Contains(r.Stdout, "rename[") {
		t.Errorf("a v2 to v3 plan renames nothing:\n%s", r.Stdout)
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "store.yaml")); !os.IsNotExist(err) {
		t.Errorf("the dry run wrote store.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fx.RepoDir, "jig.yaml")); !os.IsNotExist(err) {
		t.Errorf("the dry run wrote the marker: %v", err)
	}

	// One step at a time, and no flag the step does not use.
	projRefusal(t, "migrate --to 4", runJig(t, env, fx.RepoDir, "store", "migrate", "--to", "4"), 2, "one step at a time", "from schema_version 2 to 3")
	projRefusal(t, "migrate --to 2", runJig(t, env, fx.RepoDir, "store", "migrate", "--to", "2"), 2, "one step at a time")
	projRefusal(t, "migrate --map on a v2 store", runJig(t, env, fx.RepoDir, "store", "migrate", "--map", filepath.Join(fx.Dir, "no-such-map.yaml")), 2, "--map applies to the step from schema_version 1")

	before := gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	r = projRun(t, env, fx.RepoDir, "store", "migrate", "--to", "3")
	for _, want := range []string{"files[2]{action,path}:", "move,store.yaml", "Commit jig.yaml in", "Push the store's branch"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig store migrate stdout lacks %q:\n%s", want, r.Stdout)
		}
	}

	// The store: store.yaml, no project.yaml, one commit by the person.
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "project.yaml")); !os.IsNotExist(err) {
		t.Errorf("project.yaml survived the migration: %v", err)
	}
	cfg, err := project.LoadStoreConfig(fx.StoreDir)
	if err != nil {
		t.Fatalf("LoadStoreConfig after the migration: %v", err)
	}
	if cfg.SchemaVersion != 3 || cfg.Name != "fixture" || len(cfg.Repos) != 0 || cfg.Keys["JIG"] == "" || cfg.Platform != "platform/" {
		t.Errorf("store.yaml read as %+v, want schema 3, project fixture, keys and platform kept, no repos", cfg)
	}
	text := string(readFileOrFatal(t, filepath.Join(fx.StoreDir, "store.yaml")))
	if strings.Contains(text, "repos:") || strings.Contains(text, "name:") || !strings.Contains(text, "projects: [fixture]") {
		t.Errorf("store.yaml:\n%s", text)
	}
	if n := gitLog(t, fx.StoreDir, "rev-list", "--count", before+"..HEAD"); n != "1" {
		t.Errorf("the migration made %s commits, want 1", n)
	}
	if got := gitLog(t, fx.StoreDir, "log", "-1", "--format=%s|%an|%cn"); got != "jig: migrate store to schema_version 3|jig-fixture|jig-fixture" {
		t.Errorf("the migration commit = %q", got)
	}
	if status := gitLog(t, fx.StoreDir, "status", "--porcelain"); status != "" {
		t.Errorf("the store is dirty after the migration:\n%s", status)
	}

	// The marker: in the mapped clone, uncommitted, naming the repo the way
	// the v2 store did; the store's remote is a path here, so it is left out.
	m := projMarker(t, fx.RepoDir)
	if m.Project != "fixture" || m.Kind != project.KindProject || m.Store != "" || len(m.Repos) != 1 || m.Repos[0].Name != "fixture-repo" || m.Repos[0].Remote != "" {
		t.Errorf("jig.yaml = %+v, want project fixture, kind project, no store, the one repo fixture-repo", m)
	}
	if status := gitLog(t, fx.RepoDir, "status", "--porcelain"); !strings.Contains(status, "?? jig.yaml") {
		t.Errorf("git status in the clone = %q, want jig.yaml left for the person to commit", status)
	}

	// v3 works, from the same clone: the repos come from the marker.
	if r := projRun(t, env, fx.RepoDir, "status", fx.Ticket); !strings.Contains(r.Stdout, "ticket: JIG-1") {
		t.Errorf("jig status on the v3 store:\n%s", r.Stdout)
	}
	if got := hereJSON(t, env, fx.RepoDir); got.Project != "fixture" || got.Repo.Name != "fixture-repo" || got.Kind != "project" {
		t.Errorf("jig here on the v3 store: project %q, kind %q, repo %+v", got.Project, got.Kind, got.Repo)
	}
	if r := projRun(t, env, fx.RepoDir, "validate", fx.Ticket); !strings.Contains(r.Stdout, "valid: yes") {
		t.Errorf("jig validate on the v3 store (the repo's clone and manifest come through the marker):\n%s", r.Stdout)
	}
	if r := projRun(t, env, fx.RepoDir, "ticket", "new", "--title", "Minted on v3"); !strings.Contains(r.Stdout, "JIG-3") {
		t.Errorf("jig ticket new on the v3 store:\n%s", r.Stdout)
	}

	// Nothing left to migrate.
	projRefusal(t, "second migrate", runJig(t, env, fx.RepoDir, "store", "migrate"), 2, "the newest this jig version knows")
}

// TestStoreMigrateV2ToV3Refusals: the step refuses before it writes anything: a
// project with no mapped clone to put the marker in, a marker of another
// project, a dirty store, and a store with no identity.
func TestStoreMigrateV2ToV3Refusals(t *testing.T) {
	t.Parallel()

	t.Run("no mapped clone", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		if err := project.UpdateMachine(env.home, func(m map[string]project.MachineProject) error {
			mp := m["fixture"]
			mp.Clones = map[string]string{}
			m["fixture"] = mp
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		r := runJig(t, env, fx.StoreDir, "store", "migrate")
		projRefusal(t, "migrate with no clone mapped", r, 2, `no clone of repo "fixture-repo" mapped`, "jig map fixture-repo=")
		if _, err := os.Stat(filepath.Join(fx.StoreDir, "project.yaml")); err != nil {
			t.Errorf("the refused migration touched project.yaml: %v", err)
		}
	})

	t.Run("another project's marker", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		if err := project.WriteMarker(fx.RepoDir, project.Marker{Jig: "1", Project: "other", Kind: project.KindProject}); err != nil {
			t.Fatal(err)
		}
		r := runJig(t, env, fx.RepoDir, "store", "migrate", "--store", fx.StoreDir)
		projRefusal(t, "migrate with a foreign marker", r, 2, `already marks project "other"`)
		if _, err := os.Stat(filepath.Join(fx.StoreDir, "store.yaml")); !os.IsNotExist(err) {
			t.Errorf("the refused migration wrote store.yaml: %v", err)
		}
	})

	t.Run("a dirty store", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		writeFileOrFatal(t, filepath.Join(fx.StoreDir, "stray.txt"), []byte("uncommitted\n"))
		r := runJig(t, env, fx.RepoDir, "store", "migrate")
		projRefusal(t, "migrate with a dirty store", r, 1, "STORE_CONFLICT", "uncommitted changes")
		if _, err := os.Stat(filepath.Join(fx.RepoDir, "jig.yaml")); !os.IsNotExist(err) {
			t.Errorf("the refused migration wrote the marker: %v", err)
		}
	})

	t.Run("an existing marker is kept", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		mine := "# my marker\njig: \"1\"\nproject: fixture\nkind: project\nrepos:\n    - name: fixture-repo\n      target: develop\n"
		writeFileOrFatal(t, filepath.Join(fx.RepoDir, "jig.yaml"), []byte(mine))
		projRun(t, env, fx.RepoDir, "store", "migrate")
		if got := string(readFileOrFatal(t, filepath.Join(fx.RepoDir, "jig.yaml"))); got != mine {
			t.Errorf("the migration rewrote the marker that was there:\n%s", got)
		}
	})
}
