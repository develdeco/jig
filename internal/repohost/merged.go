package repohost

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// prStateLimit bounds one `gh pr view`: a precondition check must not hang on
// a dead network.
const prStateLimit = 30 * time.Second

// PRMerged asks gh whether the pull request at url is merged. known is false
// when gh cannot say - it is not installed, the call fails or times out, url
// is no pull request - so a caller that wants to be told "not merged" does not
// mistake "could not ask" for it. ghBin is the gh program ("" looks it up on
// PATH) and env its whole environment (nil inherits this process's), as
// NewWithEnv takes them.
func PRMerged(ghBin string, env []string, url string) (merged, known bool) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return false, false
	}
	if ghBin == "" {
		path, err := exec.LookPath("gh")
		if err != nil {
			return false, false
		}
		ghBin = path
	}
	cmd := exec.Command(ghBin, "pr", "view", url, "--json", "state")
	if env != nil {
		cmd.Env = env
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return false, false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return false, false
		}
	case <-time.After(prStateLimit):
		_ = cmd.Process.Kill()
		<-done
		return false, false
	}
	return parsePRState(stdout.String())
}

// parsePRState reads the answer of `gh pr view --json state`: {"state":
// "OPEN"|"CLOSED"|"MERGED"}. An answer without a state is not known.
func parsePRState(out string) (merged, known bool) {
	var v struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil || v.State == "" {
		return false, false
	}
	return strings.EqualFold(v.State, "MERGED"), true
}
