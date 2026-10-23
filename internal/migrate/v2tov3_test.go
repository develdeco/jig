package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

func TestNextStepAppliesOnlyTheNext(t *testing.T) {
	cases := []struct {
		version int
		to      int
		want    int // the step's From; 0 for a refusal
		msg     string
	}{
		{1, 0, 1, ""},
		{1, 2, 1, ""},
		{2, 0, 2, ""},
		{2, 3, 2, ""},
		{1, 3, 0, "one step at a time"},
		{2, 2, 0, "one step at a time"},
		{2, 4, 0, "one step at a time"},
		{3, 0, 0, "the newest this jig version knows"},
		{4, 0, 0, "newer than this jig version understands"},
		{0, 0, 0, "no migration from schema_version 0"},
	}
	for _, c := range cases {
		step, err := NextStep(project.Config{SchemaVersion: c.version}, c.to)
		if c.msg == "" {
			if err != nil || step.From != c.want {
				t.Errorf("NextStep(v%d, --to %d) = step from %d, %v; want the step from %d", c.version, c.to, step.From, err, c.want)
			}
			continue
		}
		var ae *axi.Error
		if err == nil || !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" || !strings.Contains(ae.Msg, c.msg) {
			t.Errorf("NextStep(v%d, --to %d) = %v, want a refusal saying %q", c.version, c.to, err, c.msg)
		}
	}
	if len(Steps) != int(project.CurrentSchemaVersion-1) {
		t.Errorf("%d steps for schema %d: every version below the current one needs a step", len(Steps), project.CurrentSchemaVersion)
	}
	for from, step := range Steps {
		if step.From != from || step.Plan == nil || step.Check == nil || step.Apply == nil || step.Help == nil {
			t.Errorf("Steps[%d] = %+v is not complete", from, step)
		}
	}
	if !Steps[1].UsesMap || Steps[2].UsesMap {
		t.Errorf("only the v1 to v2 step takes a rename map")
	}
}

func TestRewriteProjectYAMLToV3KeepsEverythingElse(t *testing.T) {
	v2 := `# the demo project
schema_version: 2
name: demo # the project
keys:
    DEMO: everything in demo
trackers:
    - github:
          repo: org/demo
          project: https://github.com/orgs/org/projects/1
repos:
    - remote: https://example.invalid/org/demo.git
      target: main
platform: platform/
gate:
    fix_rounds: 5
context:
    owner: me
staircase: [fast, careful]
builder_effort:
    first: low
future_key: kept
`
	out, err := RewriteProjectYAMLToV3([]byte(v2), "demo")
	if err != nil {
		t.Fatalf("RewriteProjectYAMLToV3: %v", err)
	}
	text := string(out)
	for _, want := range []string{"# the demo project", "# the project", "schema_version: 3", "projects: [demo]", "DEMO: everything in demo", "repo: org/demo", "platform: platform/", "fix_rounds: 5", "owner: me", "staircase: [fast, careful]", "first: low", "future_key: kept"} {
		if !strings.Contains(text, want) {
			t.Errorf("the v3 file lacks %q:\n%s", want, text)
		}
	}
	for _, gone := range []string{"name:", "repos:", "remote:", "target:", "schema_version: 2"} {
		if strings.Contains(text, gone) {
			t.Errorf("the v3 file still has %q:\n%s", gone, text)
		}
	}
	// name's slot is projects': the order of the rest is the file's own.
	if strings.Index(text, "projects:") > strings.Index(text, "keys:") {
		t.Errorf("projects: is not in name:'s place:\n%s", text)
	}

	// And it reads back as a v3 store config.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "store.yaml"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := project.LoadStoreConfig(dir)
	if err != nil || cfg.SchemaVersion != 3 || cfg.Name != "demo" || cfg.GitHub == nil || *cfg.Gate.FixRounds != 5 || len(cfg.Staircase) != 2 {
		t.Errorf("the migrated store.yaml reads as %+v, %v", cfg, err)
	}

	if _, err := RewriteProjectYAMLToV3([]byte("- a\n- b\n"), "demo"); err == nil {
		t.Error("a project.yaml that is no mapping was rewritten")
	}
	// A file with no name: or repos: still gets projects: and the version.
	bare, err := RewriteProjectYAMLToV3([]byte("keys:\n  K: x\n"), "demo")
	if err != nil || !strings.Contains(string(bare), "projects: [demo]") || !strings.Contains(string(bare), "schema_version: 3") {
		t.Errorf("bare rewrite = %q, %v", bare, err)
	}
}

// v2Store builds a git-backed v2 store with an origin-less clone mapped for
// its one repo, and returns the input a migration step takes.
func v2Store(t *testing.T, project2 string) (Input, string) {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "Store Person")
	runGit(t, root, "config", "user.email", "store.person@example.invalid")
	yaml := "schema_version: 2\nname: " + project2 + "\nkeys:\n    DEMO: everything in demo\ntrackers: []\nrepos:\n    - remote: https://example.invalid/org/" + project2 + "-app.git\n      target: develop\nplatform: platform/\n"
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "init")

	clone := t.TempDir()
	jigHome := t.TempDir()
	if err := project.UpdateMachine(jigHome, func(m map[string]project.MachineProject) error {
		m[project2] = project.MachineProject{Store: root, Clones: map[string]string{project2 + "-app": clone}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := project.LoadStoreConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	return Input{Store: &store.Store{Root: root}, Cfg: cfg, JigHome: jigHome}, clone
}

func TestPlanV2toV3AndApply(t *testing.T) {
	in, clone := v2Store(t, "demo")
	plan, err := Steps[2].Plan(in)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Files) != 2 || plan.Files[0].Action != "move" || plan.Files[0].Path != "store.yaml" || plan.Files[0].From != "project.yaml" || plan.Files[1].Action != "write" || !strings.HasSuffix(plan.Files[1].Path, "/jig.yaml") {
		t.Errorf("plan files = %+v, want the move of project.yaml and the write of the marker", plan.Files)
	}
	// A plan changes nothing.
	if _, err := os.Stat(filepath.Join(in.Store.Root, "store.yaml")); !os.IsNotExist(err) {
		t.Errorf("the plan wrote store.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, "jig.yaml")); !os.IsNotExist(err) {
		t.Errorf("the plan wrote the marker: %v", err)
	}

	if warnings, err := Steps[2].Check(in, plan); err != nil || len(warnings) != 0 {
		t.Errorf("Check = %v, %v", warnings, err)
	}
	before := runGit(t, in.Store.Root, "rev-parse", "HEAD")
	if err := Steps[2].Apply(in, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if n := runGit(t, in.Store.Root, "rev-list", "--count", before+"..HEAD"); n != "1" {
		t.Errorf("Apply made %s commits, want 1", n)
	}
	if got := runGit(t, in.Store.Root, "log", "-1", "--format=%s|%an"); got != "jig: migrate store to schema_version 3|Store Person" {
		t.Errorf("commit = %q", got)
	}
	if _, err := os.Stat(filepath.Join(in.Store.Root, "project.yaml")); !os.IsNotExist(err) {
		t.Errorf("project.yaml is still there: %v", err)
	}
	if status := runGit(t, in.Store.Root, "status", "--porcelain"); status != "" {
		t.Errorf("the store is dirty:\n%s", status)
	}
	m, err := project.LoadMarker(filepath.Join(clone, "jig.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// The repo is "demo-app", not the project's name, so it is written; the
	// target is not main; the remote is a URL the clone (no origin) does not
	// give; no store: the store has no origin.
	if m.Project != "demo" || m.Kind != project.KindProject || m.Store != "" || len(m.Repos) != 1 ||
		m.Repos[0].Name != "demo-app" || m.Repos[0].Target != "develop" || m.Repos[0].Remote != "https://example.invalid/org/demo-app.git" {
		t.Errorf("marker = %+v", m)
	}
	cfg, err := project.LoadConfig(in.JigHome, in.Store.Root, "demo", clone)
	if err != nil || len(cfg.Repos) != 1 || cfg.Repos[0].Name() != "demo-app" || cfg.Repos[0].TargetBranch() != "develop" || cfg.Repos[0].Remote != "https://example.invalid/org/demo-app.git" {
		t.Errorf("the migrated project reads as %+v, %v; want the v2 repo back", cfg.Repos, err)
	}
	help := strings.Join(Steps[2].Help(in, plan), "\n")
	if !strings.Contains(help, "Commit jig.yaml in "+clone) || !strings.Contains(help, "Push the store") {
		t.Errorf("help = %q", help)
	}
}

func TestMarkerForWritesTheStoreRemoteOnlyWhenItIsNotAPath(t *testing.T) {
	in, clone := v2Store(t, "demo")
	runGit(t, in.Store.Root, "remote", "add", "origin", "https://example.invalid/org/demo-tickets.git")
	m := markerFor(in, in.Cfg, in.Cfg.Repos[0], "demo-app", clone)
	if m.Store != "https://example.invalid/org/demo-tickets.git" {
		t.Errorf("store = %q, want the origin URL", m.Store)
	}
	runGit(t, in.Store.Root, "remote", "set-url", "origin", filepath.ToSlash(t.TempDir()))
	if m := markerFor(in, in.Cfg, in.Cfg.Repos[0], "demo-app", clone); m.Store != "" {
		t.Errorf("store = %q, want none for an origin that is a path on this machine", m.Store)
	}
	// A repo named like the project, on the default target, with the clone's own
	// origin, needs no repo entry at all.
	in.Cfg.Repos = []project.Repo{{Remote: "https://example.invalid/org/demo.git"}}
	runGit(t, clone, "init", "-b", "main")
	runGit(t, clone, "remote", "add", "origin", "https://example.invalid/org/demo.git")
	if m := markerFor(in, in.Cfg, in.Cfg.Repos[0], "demo", clone); len(m.Repos) != 0 {
		t.Errorf("repos = %+v, want none: nothing the clone does not say", m.Repos)
	}
}

func TestPlanV2toV3Refusals(t *testing.T) {
	refused := func(t *testing.T, in Input, want ...string) {
		t.Helper()
		_, err := Steps[2].Plan(in)
		var ae *axi.Error
		if err == nil || !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
			t.Fatalf("Plan err = %v, want a VALIDATION_ERROR", err)
		}
		for _, w := range want {
			if !strings.Contains(ae.Msg+strings.Join(ae.Help, "\n"), w) {
				t.Errorf("refusal %q / %v lacks %q", ae.Msg, ae.Help, w)
			}
		}
	}

	t.Run("several repos", func(t *testing.T) {
		in, _ := v2Store(t, "demo")
		in.Cfg.Repos = append(in.Cfg.Repos, project.Repo{Remote: "https://example.invalid/org/second.git"})
		refused(t, in, "spans 2 repos", "not supported yet")
	})
	t.Run("no repos", func(t *testing.T) {
		in, _ := v2Store(t, "demo")
		in.Cfg.Repos = nil
		refused(t, in, "declares no repos")
	})
	t.Run("no clone mapped", func(t *testing.T) {
		in, _ := v2Store(t, "demo")
		in.JigHome = t.TempDir()
		refused(t, in, `no clone of repo "demo-app" mapped`, "jig map demo-app=")
	})
	t.Run("the mapped clone is gone", func(t *testing.T) {
		in, clone := v2Store(t, "demo")
		if err := os.RemoveAll(clone); err != nil {
			t.Fatal(err)
		}
		refused(t, in, "is not a directory")
	})
	t.Run("another project's marker", func(t *testing.T) {
		in, clone := v2Store(t, "demo")
		if err := project.WriteMarker(clone, project.Marker{Jig: "1", Project: "other"}); err != nil {
			t.Fatal(err)
		}
		refused(t, in, `already marks project "other"`)
	})
	t.Run("not a v2 store", func(t *testing.T) {
		in, _ := v2Store(t, "demo")
		in.Cfg.SchemaVersion = 3
		refused(t, in, "not 2")
	})
}

func TestPlanV2toV3KeepsAMarkerThatIsThere(t *testing.T) {
	in, clone := v2Store(t, "demo")
	mine := "# mine\njig: \"1\"\nproject: demo\nkind: project\n"
	if err := os.WriteFile(filepath.Join(clone, "jig.yaml"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := Steps[2].Plan(in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files[1].Action != "keep" {
		t.Errorf("plan files = %+v, want the marker kept", plan.Files)
	}
	if err := Steps[2].Apply(in, plan); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(clone, "jig.yaml")); string(got) != mine {
		t.Errorf("the marker was rewritten:\n%s", got)
	}
	if help := strings.Join(Steps[2].Help(in, plan), "\n"); !strings.Contains(help, "left as it is") {
		t.Errorf("help = %q, want it to say the marker was kept", help)
	}
}

// A commit that fails puts the store back and takes the marker away.
func TestApplyV2toV3RestoresTheStoreWhenTheCommitFails(t *testing.T) {
	in, clone := v2Store(t, "demo")
	plan, err := Steps[2].Plan(in)
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(in.Store.Root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused by the hook >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := runGit(t, in.Store.Root, "rev-parse", "HEAD")

	err = Steps[2].Apply(in, plan)
	var ae *axi.Error
	if err == nil || !errors.As(err, &ae) || ae.Code != "STORE_MIGRATE_APPLY_FAILED" {
		t.Fatalf("Apply err = %v, want STORE_MIGRATE_APPLY_FAILED", err)
	}
	if _, err := os.Stat(filepath.Join(in.Store.Root, "project.yaml")); err != nil {
		t.Errorf("project.yaml was not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(in.Store.Root, "store.yaml")); !os.IsNotExist(err) {
		t.Errorf("store.yaml was left behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, "jig.yaml")); !os.IsNotExist(err) {
		t.Errorf("the marker was left behind: %v", err)
	}
	if got := runGit(t, in.Store.Root, "rev-parse", "HEAD"); got != before {
		t.Errorf("HEAD moved to %s", got)
	}
	if status := runGit(t, in.Store.Root, "status", "--porcelain"); status != "" {
		t.Errorf("the store is dirty after the failed migration:\n%s", status)
	}
}

// The v1 step still refuses what it always did.
func TestPlanV1toV2NeedsItsMap(t *testing.T) {
	st := newV1GitStore(t)
	cfg, err := project.LoadStoreConfig(st.Root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Steps[1].Plan(Input{Store: st, Cfg: cfg})
	var ae *axi.Error
	if err == nil || !errors.As(err, &ae) || !strings.Contains(ae.Msg, "--map is required") {
		t.Errorf("Plan without a map = %v, want --map is required", err)
	}
}
