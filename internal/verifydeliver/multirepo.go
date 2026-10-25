package verifydeliver

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// CodeMultiRepoNotYet is the code of the refusal of a publish for a ticket whose
// slices build in more than one repo.
const CodeMultiRepoNotYet = "MULTI_REPO_NOT_YET"

// checkOneRepo refuses a publish for a ticket whose slices build in more than
// one repo of the project. Publish ships one repo (selectRepo): on a ticket
// that spans repos it would say nothing of the others and stop short of the
// work. Until publish opens a pull request for each repo, that is told here
// instead. The gate has no such refusal: it reviews every repo the ticket's
// slices build in (gateRepos). A slice whose workspace no repo owns yet (`jig
// validate` says so) counts for none.
func checkOneRepo(d Deps, ticket string, slices []store.Slice) error {
	if len(d.Cfg.Repos) < 2 {
		return nil
	}
	repos := ready.ProjectRepos(d.Cfg, d.Machine.Clones)
	place := ready.Place(repos, slices)
	used := map[string]bool{}
	for _, s := range slices {
		used[place.Repo[s.Workspace]] = true
	}
	var touched []string
	for _, r := range repos {
		if used[r.Name] {
			touched = append(touched, r.Name)
		}
	}
	if len(touched) < 2 {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("this ticket's slices span repos %s; publish ships one repo until multi-repo publishing lands", joinNames(touched)),
		Code: CodeMultiRepoNotYet,
		Help: []string{fmt.Sprintf("%s can be built, verified and reviewed in each repo, but `jig publish` takes a ticket that builds in one repo", ticket)},
	}
}

// joinNames lists names in prose: "a", "a and b", "a, b and c".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
