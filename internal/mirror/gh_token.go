package mirror

import (
	"os/exec"
	"strings"

	"github.com/develdeco/jig/internal/axi"
)

// ghAuthToken reads the token `gh auth token` prints, once per process
// (brief.md#The GitHub client). No gh on PATH is refused the same way
// internal/repohost refuses it, GH_NOT_INSTALLED with the same help, so an
// operator sees one consistent message whichever path found gh missing.
func ghAuthToken() (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", &axi.Error{
			Msg:  "gh CLI is not installed",
			Code: "GH_NOT_INSTALLED",
			Help: []string{"Install gh from https://cli.github.com"},
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", &axi.Error{
			Msg:  "gh auth token failed: " + err.Error(),
			Code: "GH_NOT_INSTALLED",
			Help: []string{"Run `gh auth login` and `gh auth refresh -s project`"},
		}
	}
	return strings.TrimSpace(string(out)), nil
}
