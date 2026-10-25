package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// OrchestratorName is the name of the project, and of the orchestrator's folder,
// an Opts.Orchestrator fixture makes.
const OrchestratorName = "shop"

// NestedRepoNames are the repos an Opts.Orchestrator fixture nests in its
// orchestrator, in the project's order.
var NestedRepoNames = []string{"api", "web"}

// orchestratorBrief is the brief of the orchestrator fixture's ticket: one
// section for each repo's slice.
const orchestratorBrief = `# JIG-1: show an order's total in the shop

## Goal

The shop shows the total of an order, which the api adds up and the web page
shows as dollars.

## Seams

The change is tested at the public interface of each repo: the api's Total and
the web page's Label.

## API - the total

The api adds up the prices of an order, in cents.

## Web - the label

The web page shows that total as dollars.
`

// orchestratorSlices is the ticket's slices.yaml: one slice in each nested
// repo, each naming its repo as "<repo>/<workspace>". The two hashes are those
// of the brief's section of the same name.
const orchestratorSlices = `slices:
  - id: server
    workspace: api/server
    goal: Add up the prices of an order in the api.
    oracle: test
    blocked_by: []
    from_brief: [%q]
  - id: ui
    workspace: web/ui
    goal: Show the total as dollars in the web page.
    oracle: test
    blocked_by: []
    from_brief: [%q]
`

// buildOrchestrator is Build for Opts.Orchestrator (see there): the shape
// `jig init --orchestrator` leaves around two repos.
//
//	<dir>/shop/               the orchestrator: a git repository on main with
//	                          a jig.yaml marker of kind orchestrator, committed
//	                          by nobody
//	<dir>/shop/api, web/      the nested repos: one commit each on main, a bare
//	                          remote as origin, a manifest declaring a product
//	                          workspace (server, ui) and a lib workspace both
//	                          declare
//	<dir>/shop-tickets/       the v3 store, with no remote, holding ticket JIG-1
//	                          with a slice in each repo
//
// The pieces are written by hand rather than through project.Init, which
// resolves the person's git identity from the environment a library may not
// touch; the files are the ones Init writes.
func buildOrchestrator(dir string, opts Opts, testdataDir string) (*Fixture, error) {
	switch {
	case opts.SecondRepo, opts.ScenarioBranch != "", opts.EnvFail:
		return nil, fmt.Errorf("fixture: Orchestrator takes none of SecondRepo, ScenarioBranch and EnvFail: its project, store and repos are its own")
	}
	orchDir := filepath.Join(dir, OrchestratorName)
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		return nil, fmt.Errorf("fixture: create %s: %w", orchDir, err)
	}
	if err := initGitRepo(orchDir); err != nil {
		return nil, err
	}

	nested := map[string]string{}
	remotes := map[string]string{}
	var repos []project.MarkerRepo
	for _, name := range NestedRepoNames {
		repoDir := filepath.Join(orchDir, name)
		if err := copyTree(filepath.Join(testdataDir, "orchestrator", name), repoDir); err != nil {
			return nil, err
		}
		if err := rewriteJigYAML(repoDir, "", "", false); err != nil {
			return nil, err
		}
		if err := initGitRepo(repoDir); err != nil {
			return nil, err
		}
		if err := commitAll(repoDir, "fixture: initial "+name+" workspaces"); err != nil {
			return nil, err
		}
		remote := filepath.Join(dir, name+".git")
		if err := runGit(dir, "clone", "--bare", repoDir, remote); err != nil {
			return nil, err
		}
		if err := runGit(repoDir, "remote", "add", "origin", remote); err != nil {
			return nil, err
		}
		nested[name], remotes[name] = repoDir, remote
		repos = append(repos, project.MarkerRepo{Name: name, Path: name})
	}

	storeDir := filepath.Join(dir, OrchestratorName+"-tickets")
	if err := buildOrchestratorStore(storeDir); err != nil {
		return nil, err
	}

	if err := project.WriteMarker(orchDir, project.Marker{Project: OrchestratorName, Kind: project.KindOrchestrator, Repos: repos}); err != nil {
		return nil, err
	}
	jigHome := opts.Home
	if jigHome == "" {
		var err error
		if jigHome, err = home.Root(); err != nil {
			return nil, fmt.Errorf("fixture: resolve jig home: %w", err)
		}
	}
	err := project.UpdateMachine(jigHome, func(m map[string]project.MachineProject) error {
		m[OrchestratorName] = project.MachineProject{Store: storeDir, Clones: nested, Root: orchDir}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fixture: map the orchestrator: %w", err)
	}

	return &Fixture{
		Dir:             dir,
		StoreDir:        storeDir,
		Ticket:          Ticket,
		Home:            jigHome,
		OrchestratorDir: orchDir,
		NestedDirs:      nested,
		NestedRemotes:   remotes,
	}, nil
}

// buildOrchestratorStore writes the v3 store of the orchestrator fixture: what
// `jig init` scaffolds, then ticket JIG-1 with its brief and its slices,
// committed.
func buildOrchestratorStore(storeDir string) error {
	storeYAML, err := project.NewStoreYAML(OrchestratorName, "JIG")
	if err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
	}{
		{project.StoreFile, storeYAML},
		{"ledger.md", []byte("# Ledger\n")},
		{".gitignore", []byte("*.lock\n.*.tmp\n")},
		{".gitattributes", []byte(gitx.StoreAttributes)},
	}
	for _, f := range files {
		if err := writeFile(filepath.Join(storeDir, f.name), f.data); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(storeDir, "platform"), 0o755); err != nil {
		return fmt.Errorf("fixture: create platform dir: %w", err)
	}

	st, err := store.Open(storeDir)
	if err != nil {
		return err
	}
	brief := normalizeNewlines([]byte(orchestratorBrief))
	hashes := store.BriefSectionHashes(brief)
	var headings []string
	for h := range hashes {
		headings = append(headings, h)
	}
	sort.Strings(headings)
	api, web := hashes["API - the total"], hashes["Web - the label"]
	if api == "" || web == "" {
		return fmt.Errorf("fixture: the orchestrator brief lacks its API and Web sections (it has %v)", headings)
	}
	ticketDir := st.TicketDir(Ticket)
	if err := writeFile(filepath.Join(ticketDir, "brief.md"), brief); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(ticketDir, "slices.yaml"), []byte(fmt.Sprintf(orchestratorSlices, api, web))); err != nil {
		return err
	}
	if err := initGitRepo(storeDir); err != nil {
		return err
	}
	return commitAll(storeDir, "fixture: initial store")
}
