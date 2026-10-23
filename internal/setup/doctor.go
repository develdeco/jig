package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Finding is one line of what `jig doctor` reports: a piece jig can set up,
// whether it is in place, what it costs, and the command that puts it right.
type Finding struct {
	// Piece is skills, hooks, githook or hint.
	Piece string
	// Harness is the harness the piece is for ("" for one that has none).
	Harness string
	// Where is the scope that has it: global or here ("" when it is nowhere).
	Where string
	// State is StateInstalled, StateStale, StatePartial or StateMissing.
	State string
	// Detail says more: which events are missing, which version a stub is.
	Detail string
	// Cost is what the piece costs a session.
	Cost string
	// Fix is the command that installs or refreshes it ("" when nothing is
	// needed).
	Fix string
}

// tokens estimates a token count at four bytes a token.
func tokens(n int) int { return (n + 3) / 4 }

// SkillsCost says what a set of skills costs a session: the name and
// description of each sit in the agent's context from the start, and a body
// only when the skill is used.
func SkillsCost(skills []Skill) string {
	n := 0
	for _, s := range skills {
		n += len(s.Name) + len(description(s.Body))
	}
	return fmt.Sprintf("~%d tokens of skill descriptions in every session; a skill's body loads only when it is used", tokens(n))
}

func description(body []byte) string {
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		if d, ok := strings.CutPrefix(line, "description: "); ok {
			return d
		}
	}
	return ""
}

// HooksCost says what a harness's hook entries cost: a jig process for each
// event, and at the start of a session the text of jig here.
func HooksCost(h Harness) string {
	return fmt.Sprintf("a short jig process on each of %d events; the start of a session also carries the text of jig here", len(h.Events))
}

// GithookCost says what the global git hooks cost.
func GithookCost() string {
	return "one extra sh step per git hook; jig itself runs after a commit only"
}

// HintCost says what the hint costs a session.
func HintCost() string {
	return fmt.Sprintf("~%d tokens in every session", HintTokens())
}

// unwrappedHooks are the hooks a repository can keep that the global wrappers
// do not run: server-side ones (the repository is pushed into) and the two
// client ones left out on purpose (ClientHooks). Under the global
// core.hooksPath a repository's own copy of any of them stops running.
func unwrappedHooks() []string {
	return []string{
		"reference-transaction", "fsmonitor-watchman",
		"pre-receive", "update", "post-receive", "post-update", "push-to-checkout", "proc-receive",
	}
}

// CloneInspection is how one repository stands for recording its commits.
type CloneInspection struct {
	CloneResult
	// Unwrapped lists the hooks in the repository's own hooks directory that
	// jig's wrappers do not run, so they would stop under the global setting.
	Unwrapped []string
}

// InspectClone reads how clone records its commits, without changing it: the
// state InstallClone would leave it in if nothing were missing. A repository
// is covered when git runs jig's own hooks directory for it, has a block when
// its post-commit holds jig's, and is none otherwise.
func (g GitConfig) InspectClone(clone, hooksDir string) (CloneInspection, error) {
	res := CloneInspection{CloneResult: CloneResult{Clone: clone, State: CloneNone}}
	h, err := g.cloneHooks(clone)
	if err != nil {
		return res, err
	}
	for _, name := range unwrappedHooks() {
		info, err := os.Stat(filepath.Join(h.Common, "hooks", name))
		if err == nil && !info.IsDir() {
			res.Unwrapped = append(res.Unwrapped, name)
		}
	}
	if pointsAt(h.Dir, hooksDir) {
		res.State = CloneCovered
		return res, nil
	}
	res.Path = filepath.Join(h.Dir, "post-commit")
	data, exists, err := readIfExists(res.Path)
	if err != nil {
		return res, err
	}
	if exists {
		if _, _, found, err := findBlock(data); err == nil && found {
			res.State = CloneHasBlock
		}
	}
	if res.State == CloneNone && h.Shared {
		res.Reason = "core.hooksPath is set outside the repository, to " + filepath.ToSlash(h.Dir)
	}
	return res, nil
}
