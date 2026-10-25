package ready

import (
	"slices"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/store"
)

// Unplaced is the repo name Place gives a workspace that no repo owns (none
// declares it, or several do), so a batch of such slices still has a name to
// print.
const Unplaced = "?"

// Repo is one repo of a project and the workspace ids its manifest declares
// (its .claude/jig.yaml, or the detected default "root").
type Repo struct {
	Name       string
	Workspaces []string
	// Oracles are the names of the oracles its manifest declares, sorted.
	Oracles []string
	// Dir is the repo's clone on this machine, "" when it has none, and Err
	// why the manifest of that clone could not be read, nil when it could. A
	// repo with no clone, or no manifest, declares nothing here (ProjectRepos
	// fills these).
	Dir string
	Err error
}

// Read reports whether the manifest of the repo was read, so that Workspaces
// is what it declares and not merely what is known of it.
func (r Repo) Read() bool { return r.Dir != "" && r.Err == nil }

// SplitWorkspace reads a workspace as a slice writes it in a project of
// several repos: "<repo>/<id>", where repo is one of repos. A workspace that
// does not begin with the name of a repo and a slash is a bare id, and ok is
// false. In a project of one repo no slice qualifies its workspace (Place).
func SplitWorkspace(written string, repos []string) (repo, id string, ok bool) {
	repo, id, found := strings.Cut(written, "/")
	if !found || id == "" || !slices.Contains(repos, repo) {
		return "", written, false
	}
	return repo, id, true
}

// Placement is where the workspaces of one ticket's slices are built. Its
// maps are keyed by the workspace as the slice writes it.
type Placement struct {
	// Repo maps a workspace to the name of the repo that owns it. A workspace
	// that no repo owns maps to Unplaced.
	Repo map[string]string
	// ID maps a workspace to its id in the manifest of the repo that owns it:
	// the workspace itself when it is written bare, the id after the repo's
	// name when it is written "<repo>/<id>".
	ID map[string]string
	// Unplaced lists, sorted, the bare workspaces that no repo declares.
	Unplaced []string
	// Ambiguous lists, sorted, the bare workspaces that more than one repo
	// declares: the slice has to name its repo, and Declared says which repos
	// could be meant.
	Ambiguous []string
	Declared  map[string][]string
}

// Place maps each workspace the slices name to the repo that owns it. A
// project with one repo gives that repo every workspace, declared or not (the
// workspace is its bare id, as it was before a project could have several
// repos). With several, a slice names its repo: "<repo>/<id>" is that repo's
// workspace id, and a bare id belongs to the one repo whose manifest declares
// it. A bare id that two repos declare is Ambiguous, and one that nobody
// declares is Unplaced: both map to the Unplaced repo name until the slice
// says which repo it means.
func Place(repos []Repo, all []store.Slice) Placement {
	p := Placement{Repo: map[string]string{}, ID: map[string]string{}, Declared: map[string][]string{}}
	if len(repos) == 1 {
		for _, s := range all {
			p.Repo[s.Workspace], p.ID[s.Workspace] = repos[0].Name, s.Workspace
		}
		return p
	}
	names := make([]string, len(repos))
	declares := map[string][]string{}
	for i, r := range repos {
		names[i] = r.Name
		for _, id := range r.Workspaces {
			if !slices.Contains(declares[id], r.Name) {
				declares[id] = append(declares[id], r.Name)
			}
		}
	}
	for _, s := range all {
		written := s.Workspace
		if _, done := p.Repo[written]; done {
			continue
		}
		if repo, id, ok := SplitWorkspace(written, names); ok {
			p.Repo[written], p.ID[written] = repo, id
			continue
		}
		p.ID[written] = written
		switch owners := declares[written]; len(owners) {
		case 0:
			p.Repo[written] = Unplaced
			p.Unplaced = appendOnce(p.Unplaced, written)
		case 1:
			p.Repo[written] = owners[0]
		default:
			p.Repo[written] = Unplaced
			p.Ambiguous = appendOnce(p.Ambiguous, written)
			p.Declared[written] = owners
		}
	}
	sort.Strings(p.Unplaced)
	sort.Strings(p.Ambiguous)
	return p
}

// BuiltIn names the repos the slices build in, in the order of repos: the
// repos Place gives one of their workspaces. A project of one repo is that
// repo whatever the slices say, and a ticket none of whose slices is placed
// in a repo (it has none yet, say) is taken to build in every repo of the
// project, so a question asked of its repos still has repos to ask.
func BuiltIn(repos []Repo, all []store.Slice) []string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
	}
	if len(repos) < 2 {
		return names
	}
	place := Place(repos, all)
	used := map[string]bool{}
	for _, name := range place.Repo {
		used[name] = true
	}
	var out []string
	for _, name := range names {
		if used[name] {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return names
	}
	return out
}

func appendOnce(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}
