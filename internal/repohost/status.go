package repohost

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// What GitHub says of a pull request's mergeability.
const (
	MergeableYes      = "MERGEABLE"
	MergeableConflict = "CONFLICTING"
	MergeableUnknown  = "UNKNOWN"
)

// Status is what the host says of one pull request when a landing asks: where
// it stands, whether it can merge as it is, the branch it goes into and, once
// merged, the commit the merge made.
type Status struct {
	// State is StateOpen, StateClosed or StateMerged.
	State string
	// Mergeable is MergeableYes, MergeableConflict or MergeableUnknown (the
	// host has not worked it out yet, or says nothing).
	Mergeable string
	// Base is the branch the pull request goes into.
	Base string
	// MergeCommit is the commit the merge made, "" while the pull request is
	// not merged or the host names none.
	MergeCommit string
}

// MergeMethods are the ways a repository allows a pull request to be merged.
type MergeMethods struct {
	Squash, Merge, Rebase bool
}

// Preferred is the method a landing names when the person gave none: squash
// where the repo allows it, else merge, else rebase. "" when the repo allows
// none of them.
func (m MergeMethods) Preferred() string {
	switch {
	case m.Squash:
		return "squash"
	case m.Merge:
		return "merge"
	case m.Rebase:
		return "rebase"
	}
	return ""
}

// Allows reports whether the repo allows method ("squash", "merge" or
// "rebase").
func (m MergeMethods) Allows(method string) bool {
	switch method {
	case "squash":
		return m.Squash
	case "merge":
		return m.Merge
	case "rebase":
		return m.Rebase
	}
	return false
}

// PRStatus asks gh for the status of the pull request at url. known is false
// when gh cannot say, as PRState has it: gh is not installed, the call fails
// or times out, or url is no pull request. ghBin and env are as PRState takes
// them.
func PRStatus(ghBin string, env []string, url string) (Status, bool) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return Status{}, false
	}
	out, ok := runGh(ghBin, env, "pr", "view", url, "--json", "state,mergeable,baseRefName,mergeCommit")
	if !ok {
		return Status{}, false
	}
	return parsePRStatus(out)
}

// parsePRStatus reads the answer of `gh pr view --json
// state,mergeable,baseRefName,mergeCommit`. An answer with no known state is
// not known.
func parsePRStatus(out string) (Status, bool) {
	var v struct {
		State       string `json:"state"`
		Mergeable   string `json:"mergeable"`
		BaseRefName string `json:"baseRefName"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return Status{}, false
	}
	s := Status{State: strings.ToUpper(v.State), Mergeable: strings.ToUpper(v.Mergeable), Base: v.BaseRefName}
	switch s.State {
	case StateOpen, StateClosed, StateMerged:
	default:
		return Status{}, false
	}
	switch s.Mergeable {
	case MergeableYes, MergeableConflict:
	default:
		s.Mergeable = MergeableUnknown
	}
	if v.MergeCommit != nil {
		s.MergeCommit = v.MergeCommit.Oid
	}
	return s, true
}

// AllowedMergeMethods asks gh which merge methods the repository of the pull
// request at prURL allows. known is false when gh cannot say.
func AllowedMergeMethods(ghBin string, env []string, prURL string) (MergeMethods, bool) {
	ref, err := ParsePRURL(prURL)
	if err != nil {
		return MergeMethods{}, false
	}
	out, ok := runGh(ghBin, env, "repo", "view", ref.Host+"/"+ref.Owner+"/"+ref.Repo,
		"--json", "squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed")
	if !ok {
		return MergeMethods{}, false
	}
	var v struct {
		Squash bool `json:"squashMergeAllowed"`
		Merge  bool `json:"mergeCommitAllowed"`
		Rebase bool `json:"rebaseMergeAllowed"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return MergeMethods{}, false
	}
	return MergeMethods{Squash: v.Squash, Merge: v.Merge, Rebase: v.Rebase}, true
}

// runGh runs gh with args under the same limit and lookup rules PRState has,
// and returns its stdout; ok is false when it could not be run, failed or
// timed out.
func runGh(ghBin string, env []string, args ...string) (string, bool) {
	if ghBin == "" {
		path, err := exec.LookPath("gh")
		if err != nil {
			return "", false
		}
		ghBin = path
	}
	cmd := exec.Command(ghBin, args...)
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
	return stdout.String(), true
}
