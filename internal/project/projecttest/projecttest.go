// Package projecttest is how a test (and the test fixture) makes a project
// known to the code under test: it writes the per-machine mapping of a v2
// store's project and reads a project's config the way a verb does. Only tests
// import it; the jig binary maps projects through `jig init` and `jig here`.
package projecttest

import (
	"fmt"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// InitProject loads storePath's config (a v2 store's project.yaml), validates
// that every clone name matches a repo declared there (by Repo.Name), and
// records the mapping in the per-machine store under the jig home root
// jigHome, keyed by the project's name. `jig init` makes v3 projects and maps
// them itself (project.Init); this is how an existing v2 store, and the test
// fixture's, gets its clones mapped.
func InitProject(jigHome, storePath string, clones map[string]string) (project.Config, error) {
	cfg, err := project.LoadStoreConfig(storePath)
	if err != nil {
		return project.Config{}, err
	}

	repoNames := map[string]bool{}
	for _, r := range cfg.Repos {
		repoNames[r.Name()] = true
	}
	for name := range clones {
		if !repoNames[name] {
			return project.Config{}, &axi.Error{
				Msg:  fmt.Sprintf("clone %q does not match any repo declared in project.yaml", name),
				Code: "VALIDATION_ERROR",
			}
		}
	}

	absStore, err := filepath.Abs(storePath)
	if err != nil {
		return project.Config{}, fmt.Errorf("project: resolve store path: %w", err)
	}
	absClones := make(map[string]string, len(clones))
	for name, p := range clones {
		abs, err := filepath.Abs(p)
		if err != nil {
			return project.Config{}, fmt.Errorf("project: resolve clone %s: %w", name, err)
		}
		absClones[name] = abs
	}

	if err := project.UpdateMachine(jigHome, func(machine map[string]project.MachineProject) error {
		machine[cfg.Name] = project.MachineProject{Store: absStore, Clones: absClones}
		return nil
	}); err != nil {
		return project.Config{}, err
	}

	return cfg, nil
}

// SaveMachine replaces the whole per-machine project mapping under jigHome with
// m, under the mapping's lock.
func SaveMachine(jigHome string, m map[string]project.MachineProject) error {
	return project.UpdateMachine(jigHome, func(machine map[string]project.MachineProject) error {
		for k := range machine {
			delete(machine, k)
		}
		for k, v := range m {
			machine[k] = v
		}
		return nil
	})
}

// LoadConfig is project.LoadConfigWith without a memo of the repos' origins.
func LoadConfig(jigHome, storeRoot, name string, roots ...string) (project.Config, error) {
	return project.LoadConfigWith(nil, jigHome, storeRoot, name, roots...)
}
