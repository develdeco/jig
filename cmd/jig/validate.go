package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// cmdValidate implements `jig validate <ticket>`.
func cmdValidate(args []string, stdout io.Writer) int {
	fs := newFlagSet("validate")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, err := resolveStoreForProject(*projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	problems, err := validateTicket(st, cfg, mp, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	if len(problems) > 0 {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("ticket %s failed validation", ticket),
			Code: "VALIDATION_ERROR",
			Help: problems,
		})
	}

	blocks := []string{"valid: yes"}
	if data, rerr := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md")); rerr == nil {
		hashes := store.BriefSectionHashes(data)
		headings := make([]string, 0, len(hashes))
		for h := range hashes {
			headings = append(headings, h)
		}
		sort.Strings(headings)
		rows := make([][]string, 0, len(headings))
		for _, h := range headings {
			rows = append(rows, []string{h, hashes[h]})
		}
		blocks = append(blocks, axi.Table("sections", []string{"heading", "sha256"}, rows))
	}
	blocks = append(blocks, axi.Help(fmt.Sprintf("Run `jig run %s` to start the frontier", ticket)))
	axi.Render(stdout, blocks...)
	return 0
}

// validateTicket checks that ticket's brief, slices.yaml, and manifest
// agree, returning every problem found (nil means valid). Two ids around a
// ticket are reserved by jig's own machinery and reported as problems on
// their own: the ticket id, which names the ticket's pool leases (see
// pool.CheckTicket for the suffixes it refuses), and the slice id "gate",
// which the gate reviewer's dispatch uses as its own session slice name
// (checked below, alongside every other slice id).
func validateTicket(st *store.Store, cfg project.Config, mp project.MachineProject, ticket string) ([]string, error) {
	// An id that cannot name its leases is the only problem worth reporting
	// on its own: every path below is derived from it.
	if err := pool.CheckTicket(ticket); err != nil {
		return []string{err.Error()}, nil
	}

	var problems []string

	briefData, briefErr := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if briefErr != nil {
		problems = append(problems, fmt.Sprintf("brief.md not found: %v", briefErr))
	} else if hashes := store.BriefSectionHashes(briefData); len(hashes) == 0 {
		problems = append(problems, `brief.md has no "## " sections`)
	}

	slices, err := st.ReadSlices(ticket)
	if err != nil {
		problems = append(problems, fmt.Sprintf("slices.yaml did not parse: %v", err))
		return problems, nil
	}

	var hashes map[string]string
	if briefErr == nil {
		hashes = store.BriefSectionHashes(briefData)
	}
	hashExists := func(h string) bool {
		for _, cur := range hashes {
			if cur == h {
				return true
			}
		}
		return false
	}

	ids := map[string]bool{}
	for _, sl := range slices {
		ids[sl.ID] = true
	}
	for _, sl := range slices {
		if sl.ID == "gate" {
			problems = append(problems, `slice id "gate" is reserved for the gate reviewer's own dispatch, which uses it as the session's slice name`)
		}
	}

	var m manifest.Manifest
	manifestOK := false
	if len(cfg.Repos) > 0 {
		repoDir := mp.Clones[cfg.Repos[0].Name()]
		if repoDir == "" {
			problems = append(problems, fmt.Sprintf("no machine-mapped clone for repo %q", cfg.Repos[0].Name()))
		} else {
			resolved, merr := manifest.Resolve(repoDir)
			if merr != nil {
				problems = append(problems, fmt.Sprintf("manifest resolve failed: %v", merr))
			} else {
				m = resolved
				manifestOK = true
			}
		}
	} else {
		problems = append(problems, "project.yaml declares no repos")
	}

	for _, sl := range slices {
		for _, h := range sl.FromBrief {
			if !hashExists(h) {
				problems = append(problems, fmt.Sprintf("slice %s: from_brief hash %s does not match any current brief section", sl.ID, h))
			}
		}
		for _, b := range sl.BlockedBy {
			if !ids[b] {
				problems = append(problems, fmt.Sprintf("slice %s: blocked_by %q does not exist", sl.ID, b))
			}
		}
		if manifestOK {
			if _, ok := m.Workspace(sl.Workspace); !ok {
				problems = append(problems, fmt.Sprintf("slice %s: workspace %q not in manifest", sl.ID, sl.Workspace))
			}
		}
		if strings.TrimSpace(sl.Oracle) == "" {
			problems = append(problems, fmt.Sprintf("slice %s: oracle is empty", sl.ID))
		}
	}

	if cyc := findBlockedByCycle(slices); cyc != "" {
		problems = append(problems, "blocked_by cycle: "+cyc)
	}

	return problems, nil
}

// findBlockedByCycle runs a DFS over slices' blocked_by edges and returns a
// description of the first cycle found, or "" when the graph is acyclic.
func findBlockedByCycle(slices []store.Slice) string {
	edges := map[string][]string{}
	for _, sl := range slices {
		edges[sl.ID] = sl.BlockedBy
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string
	var found string

	var dfs func(n string)
	dfs = func(n string) {
		if found != "" {
			return
		}
		color[n] = gray
		path = append(path, n)
		for _, next := range edges[n] {
			if found != "" {
				return
			}
			switch color[next] {
			case gray:
				idx := -1
				for i, p := range path {
					if p == next {
						idx = i
						break
					}
				}
				if idx == -1 {
					found = strings.Join(append(append([]string{}, path...), next), "->")
				} else {
					found = strings.Join(append(append([]string{}, path[idx:]...), next), "->")
				}
				return
			case white:
				dfs(next)
			}
		}
		path = path[:len(path)-1]
		color[n] = black
	}

	for _, sl := range slices {
		if found != "" {
			break
		}
		if color[sl.ID] == white {
			dfs(sl.ID)
		}
	}
	return found
}
