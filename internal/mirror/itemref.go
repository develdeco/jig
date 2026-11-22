package mirror

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// ItemRef names one tracker item. Repo is empty when the ref names none (the
// bare number and #48 mean the project's own tracker repo), and is the
// lower-cased owner/repo otherwise.
type ItemRef struct {
	Provider string
	Repo     string
	Number   int
}

// acceptedItemForms is what every refused ref says it could have been.
const acceptedItemForms = "the number (48), #48, owner/repo#48, or the issue's URL (https://github.com/owner/repo/issues/48)"

// itemParsers is the per-provider parser table; only github has an entry.
var itemParsers = map[string]func(string) (ItemRef, bool){
	"github": parseGitHubItem,
}

// ParseItemRef reads a tracker item's ref: the bare number, #48,
// owner/repo#48 or the issue URL. It never refuses by repo; CheckItemRepo does.
func ParseItemRef(s string) (ItemRef, error) {
	s = strings.TrimSpace(s)
	for name, parse := range itemParsers {
		if ref, ok := parse(s); ok {
			ref.Provider = name
			return ref, nil
		}
	}
	return ItemRef{}, &axi.Error{
		Msg:  fmt.Sprintf("%q is not a tracker item ref", s),
		Code: "VALIDATION_ERROR",
		Help: []string{"Name the item as " + acceptedItemForms},
	}
}

func parseGitHubItem(s string) (ItemRef, bool) {
	if s == "" {
		return ItemRef{}, false
	}
	if strings.Contains(s, "://") {
		return parseGitHubURL(s)
	}
	if i := strings.Index(s, "#"); i >= 0 {
		repo, num := s[:i], s[i+1:]
		n, ok := positive(num)
		if !ok {
			return ItemRef{}, false
		}
		if repo == "" {
			return ItemRef{Number: n}, true
		}
		if !validRepo(repo) {
			return ItemRef{}, false
		}
		return ItemRef{Repo: strings.ToLower(repo), Number: n}, true
	}
	n, ok := positive(s)
	return ItemRef{Number: n}, ok
}

func parseGitHubURL(s string) (ItemRef, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Host, "github.com") {
		return ItemRef{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "issues" {
		return ItemRef{}, false
	}
	n, ok := positive(parts[3])
	repo := parts[0] + "/" + parts[1]
	if !ok || !validRepo(repo) {
		return ItemRef{}, false
	}
	return ItemRef{Repo: strings.ToLower(repo), Number: n}, true
}

// positive reads a positive decimal integer made of digits only.
func positive(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// validRepo reports whether s is owner/repo with two non-empty parts and no
// other separator or space.
func validRepo(s string) bool {
	owner, repo, ok := strings.Cut(s, "/")
	if !ok || owner == "" || repo == "" {
		return false
	}
	return !strings.ContainsAny(s[len(owner)+1:], "/#? ") && !strings.ContainsAny(owner, "#? ")
}

// CheckItemRepo refuses a ref naming another repo than the project's tracker
// repo (compared without case), and a project with no GitHub tracker.
func CheckItemRepo(ref ItemRef, cfg project.Config) error {
	if cfg.GitHub == nil {
		return &axi.Error{
			Msg:  "this project has no GitHub tracker (trackers: in project.yaml) to read an item from",
			Code: "VALIDATION_ERROR",
			Help: []string{"Add a github: entry under trackers: in project.yaml"},
		}
	}
	if ref.Repo != "" && !strings.EqualFold(ref.Repo, cfg.GitHub.Repo) {
		return &axi.Error{
			Msg:  fmt.Sprintf("%s#%d is not in the project's tracker repo %s", ref.Repo, ref.Number, cfg.GitHub.Repo),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Name an item of %s by its number (%d)", cfg.GitHub.Repo, ref.Number)},
		}
	}
	return nil
}
