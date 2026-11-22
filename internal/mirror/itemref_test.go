package mirror

import (
	"errors"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

func TestParseItemRef(t *testing.T) {
	ok := map[string]ItemRef{
		"48":            {Provider: "github", Number: 48},
		" 48 ":          {Provider: "github", Number: 48},
		"#48":           {Provider: "github", Number: 48},
		"Owner/Repo#48": {Provider: "github", Repo: "owner/repo", Number: 48},
		"https://github.com/Owner/Repo/issues/48":   {Provider: "github", Repo: "owner/repo", Number: 48},
		"https://github.com/o/r/issues/48/":         {Provider: "github", Repo: "o/r", Number: 48},
		"https://github.com/o/r/issues/48?x=1#frag": {Provider: "github", Repo: "o/r", Number: 48},
	}
	for in, want := range ok {
		got, err := ParseItemRef(in)
		if err != nil || got != want {
			t.Errorf("ParseItemRef(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	bad := []string{"", "0", "-3", "4x", "#", "##4", "o/r", "o/r#", "o/r#x", "/r#4", "o/#4", "a/b/c#4",
		"https://github.com/o/r/pull/48", "https://gitlab.com/o/r/issues/48", "https://github.com/o/r/issues/x",
		"https://github.com/o/r/issues", "https://github.com/o/r/issues/48/files", "99999999999999999999"}
	for _, in := range bad {
		_, err := ParseItemRef(in)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" || !strings.Contains(strings.Join(ae.Help, " "), "owner/repo#48") {
			t.Errorf("ParseItemRef(%q) err = %v; want VALIDATION_ERROR naming the accepted forms", in, err)
		}
	}
}

func TestCheckItemRepo(t *testing.T) {
	cfg := project.Config{GitHub: &project.GitHubTracker{Repo: "Owner/Repo"}}
	if err := CheckItemRepo(ItemRef{Number: 4}, cfg); err != nil {
		t.Errorf("bare ref: %v", err)
	}
	if err := CheckItemRepo(ItemRef{Repo: "owner/repo", Number: 4}, cfg); err != nil {
		t.Errorf("same repo, other case: %v", err)
	}
	err := CheckItemRepo(ItemRef{Repo: "other/repo", Number: 4}, cfg)
	if err == nil || !strings.Contains(err.Error(), "Owner/Repo") {
		t.Errorf("other repo err = %v; want it naming the tracker repo", err)
	}
	err = CheckItemRepo(ItemRef{Number: 4}, project.Config{})
	if err == nil || !strings.Contains(err.Error(), "trackers:") {
		t.Errorf("no tracker err = %v; want it naming trackers:", err)
	}
}
