package setup

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInspectCloneStates(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	clone := initRepo(t, g)

	res, err := g.InspectClone(clone, hooksDir)
	if err != nil || res.State != CloneNone {
		t.Fatalf("a repository with nothing: %+v, %v", res, err)
	}

	if _, err := g.InstallClone(clone, hooksDir, "jig"); err != nil {
		t.Fatal(err)
	}
	if res, err := g.InspectClone(clone, hooksDir); err != nil || res.State != CloneHasBlock || !strings.HasSuffix(filepath.ToSlash(res.Path), ".git/hooks/post-commit") {
		t.Errorf("after InstallClone: %+v, %v", res, err)
	}
	// Inspecting writes nothing: uninstall still finds the block there.
	if res, err := g.UninstallClone(clone, hooksDir); err != nil || !res.Changed {
		t.Errorf("uninstall after inspect: %+v, %v", res, err)
	}
	if res, err := g.InspectClone(clone, hooksDir); err != nil || res.State != CloneNone {
		t.Errorf("after uninstall: %+v, %v", res, err)
	}

	// Under the global core.hooksPath the repository is covered.
	if _, err := g.InstallGlobal(hooksDir, "jig"); err != nil {
		t.Fatal(err)
	}
	if res, err := g.InspectClone(clone, hooksDir); err != nil || res.State != CloneCovered {
		t.Errorf("under the global hooks: %+v, %v", res, err)
	}
}

func TestInspectCloneNamesTheHooksTheWrappersDoNotRun(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	clone := initRepo(t, g)
	writeOrFail(t, filepath.Join(clone, ".git", "hooks", "reference-transaction"), "#!/bin/sh\n", 0o755)
	writeOrFail(t, filepath.Join(clone, ".git", "hooks", "post-receive"), "#!/bin/sh\n", 0o755)
	writeOrFail(t, filepath.Join(clone, ".git", "hooks", "pre-commit"), "#!/bin/sh\n", 0o755)
	res, err := g.InspectClone(clone, filepath.Join(t.TempDir(), "hooks"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(res.Unwrapped)
	if !slices.Equal(res.Unwrapped, []string{"post-receive", "reference-transaction"}) {
		t.Errorf("Unwrapped = %v", res.Unwrapped)
	}
}

func TestCostsSayWhatTheyCost(t *testing.T) {
	t.Parallel()
	if c := SkillsCost(testSkills()); !strings.Contains(c, "tokens of skill descriptions") {
		t.Errorf("SkillsCost = %q", c)
	}
	h, _ := HarnessNamed("claude")
	if c := HooksCost(h); !strings.Contains(c, "5 events") {
		t.Errorf("HooksCost = %q", c)
	}
	if !strings.Contains(HintCost(), "tokens in every session") || HintTokens() <= 0 || HintTokens() > 150 {
		t.Errorf("HintCost = %q, %d tokens", HintCost(), HintTokens())
	}
	if GithookCost() == "" {
		t.Error("GithookCost is empty")
	}
}
