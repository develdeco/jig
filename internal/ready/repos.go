package ready

import (
	"sort"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/project"
)

// ProjectRepos lists the repos of the project cfg describes, in its order, with
// the workspaces and oracles each declares in the manifest of its clone. clones
// maps a repo's name to its clone on this machine; a repo with none declares
// nothing (Repo.Dir is empty), and one whose manifest does not resolve says why
// in Repo.Err.
func ProjectRepos(cfg project.Config, clones map[string]string) []Repo {
	repos := make([]Repo, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		repo := Repo{Name: r.Name(), Dir: clones[r.Name()]}
		if repo.Dir != "" {
			m, err := manifest.Resolve(repo.Dir)
			repo.Err = err
			if err == nil {
				for _, ws := range m.Workspaces {
					repo.Workspaces = append(repo.Workspaces, ws.ID)
				}
				for name := range m.Oracles {
					repo.Oracles = append(repo.Oracles, name)
				}
				sort.Strings(repo.Oracles)
			}
		}
		repos = append(repos, repo)
	}
	return repos
}
