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

// The states a pull request is in, as GitHub spells them.
const (
	StateOpen   = "OPEN"
	StateClosed = "CLOSED"
	StateMerged = "MERGED"
)

// PRState asks gh for the state of the pull request at url: StateOpen,
// StateClosed or StateMerged. known is false when gh cannot say - it is not
// installed, the call fails or times out, url is no pull request - so a caller
// that wants to be told "not merged" does not mistake "could not ask" for it.
// ghBin is the gh program ("" looks it up on PATH) and env its whole
// environment (nil inherits this process's), as NewWithEnv takes them.
func PRState(ghBin string, env []string, url string) (state string, known bool) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", false
	}
	if ghBin == "" {
		path, err := exec.LookPath("gh")
		if err != nil {
			return "", false
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
		return "", false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return "", false
		}
	case <-time.After(prStateLimit):
		_ = cmd.Process.Kill()
		<-done
		return "", false
	}
	state = parsePRState(stdout.String())
	return state, state != ""
}

// parsePRState reads the answer of `gh pr view --json state`: {"state":
// "OPEN"|"CLOSED"|"MERGED"}, upper-cased. An answer without a state, or one
// that is none of the three, is "": not known.
func parsePRState(out string) string {
	var v struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return ""
	}
	switch s := strings.ToUpper(v.State); s {
	case StateOpen, StateClosed, StateMerged:
		return s
	}
	return ""
}
