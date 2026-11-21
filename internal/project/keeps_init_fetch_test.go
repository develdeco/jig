package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKeepsInitFetchesURLsAsSubmoduleOrClone: a URL given to init is fetched
// into the orchestrator as a submodule written with ignore = all (its remote
// left to .gitmodules, the .gitmodules staged for the person, jig.yaml
// untracked), and as a gitignored clone with Clone (whose remote then goes in
// the marker, since a clone git ignores is not something another checkout
// would have).
func TestKeepsInitFetchesURLsAsSubmoduleOrClone(t *testing.T) {
	src := runGitDir(t, filepath.Join(t.TempDir(), "lib"))
	writeFile(t, filepath.Join(src, "README.md"), "# lib\n")
	runGitInProject(t, src, "add", "-A")
	runGitInProject(t, src, "commit", "-m", "init")
	bare := filepath.Join(t.TempDir(), "lib.git")
	runGitInProject(t, filepath.Dir(bare), "clone", "--bare", src, bare)
	slash := filepath.ToSlash(bare)
	url := "file://" + slash
	if !strings.HasPrefix(slash, "/") {
		url = "file:///" + slash
	}

	t.Run("submodule", func(t *testing.T) {
		hub := filepath.Join(t.TempDir(), "hub")
		if err := os.MkdirAll(hub, 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := Init(InitOpts{Dir: hub, JigHome: t.TempDir(), Kind: KindOrchestrator, MakeRepo: true, Repos: []string{url}})
		if err != nil {
			t.Fatalf("Init: %v", err)
		}
		if len(res.Repos) != 1 || !res.Repos[0].Submodule || res.Repos[0].State != RepoCheckedOut {
			t.Errorf("repos = %+v, want lib as a checked-out submodule", res.Repos)
		}
		data, err := os.ReadFile(filepath.Join(hub, ".gitmodules"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "path = lib") || !strings.Contains(string(data), "ignore = all") {
			t.Errorf(".gitmodules = %q, want the lib submodule written with ignore = all", data)
		}
		m, err := LoadMarker(filepath.Join(hub, MarkerFile))
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Repos) != 1 || m.Repos[0].Name != "lib" || m.Repos[0].Remote != "" {
			t.Errorf("marker repos = %+v, want lib with no remote: .gitmodules carries it", m.Repos)
		}
		if status := runGitInProject(t, hub, "status", "--porcelain"); !strings.Contains(status, "A  .gitmodules") || !strings.Contains(status, "?? jig.yaml") {
			t.Errorf("git status in the orchestrator:\n%s", status)
		}
	})

	t.Run("clone", func(t *testing.T) {
		hub := filepath.Join(t.TempDir(), "hub")
		if err := os.MkdirAll(hub, 0o755); err != nil {
			t.Fatal(err)
		}
		res, err := Init(InitOpts{Dir: hub, JigHome: t.TempDir(), Kind: KindOrchestrator, MakeRepo: true, Clone: true, Repos: []string{url}})
		if err != nil {
			t.Fatalf("Init: %v", err)
		}
		if len(res.Fetched) != 1 || res.Fetched[0] != "lib" || res.Repos[0].Submodule {
			t.Errorf("fetched = %v, repos = %+v, want lib fetched as a plain clone", res.Fetched, res.Repos)
		}
		if _, err := os.Stat(filepath.Join(hub, ".gitmodules")); !os.IsNotExist(err) {
			t.Errorf("a clone wrote a .gitmodules: %v", err)
		}
		ignore, err := os.ReadFile(filepath.Join(hub, ".gitignore"))
		if err != nil || !strings.Contains(string(ignore), "/lib/") {
			t.Errorf(".gitignore = %q, %v, want /lib/", ignore, err)
		}
		m, err := LoadMarker(filepath.Join(hub, MarkerFile))
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Repos) != 1 || m.Repos[0].Remote != url {
			t.Errorf("marker repos = %+v, want lib with the URL as its remote", m.Repos)
		}
	})
}
