package main

import (
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// init registers `jig here`'s verification section (sections.go). `jig here`
// itself lists the commits that verified and those still being verified
// (verificationsIn); this section adds how the located slice stands at the
// repo's HEAD, which it cannot show: a failed verification, a verification
// that could not run, and the time and summary of each.
func init() {
	registerHereSection(hereSection{
		Order:  320,
		Name:   "verify",
		Blocks: hereVerifyBlocks,
		JSON: func(c hereCtx) (any, error) {
			head, err := hereVerifyHead(c)
			if err != nil || head == nil {
				return nil, err
			}
			return head, nil
		},
	})
}

// hereVerifyHeadInfo is the latest verification of the located slice at the
// repo's HEAD, as `jig here --json` shows it.
type hereVerifyHeadInfo struct {
	Slice   string `json:"slice"`
	Commit  string `json:"commit"`
	State   string `json:"state"`
	Attempt int    `json:"attempt,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// hereVerifyHead reads how HEAD of the located repo stands for the located
// slice from the ticket's journal: nil when `jig here` located no ticket, slice
// or repo, when the repo has no commit, or when the journal says nothing of
// verifying that commit for that slice.
func hereVerifyHead(c hereCtx) (*hereVerifyHeadInfo, error) {
	if c.Store == nil || c.Where.Ticket == "" || c.Where.Slice == "" || c.Where.RepoDir == "" {
		return nil, nil
	}
	head, err := gitx.RevParse(c.Where.RepoDir, "HEAD")
	if err != nil {
		// A repo with no commit yet has no HEAD to verify.
		return nil, nil
	}
	lines, err := journal.Read(c.Store, c.Where.Ticket)
	if err != nil {
		return nil, err
	}
	v := journal.VerificationState(lines, head, c.Where.Slice)
	if v.State == journal.VerifyNone {
		return nil, nil
	}
	return &hereVerifyHeadInfo{Slice: c.Where.Slice, Commit: head, State: v.State, Attempt: v.Attempt, Seconds: v.Seconds, Summary: v.Summary}, nil
}

// hereVerifyBlocks renders the section: one verification block for HEAD.
func hereVerifyBlocks(c hereCtx) ([]string, error) {
	h, err := hereVerifyHead(c)
	if err != nil || h == nil {
		return nil, err
	}
	kv := [][2]string{{"slice", h.Slice}, {"commit", shortSHA(h.Commit)}, {"state", h.State}}
	if h.Attempt > 0 {
		kv = append(kv, [2]string{"attempt", strconv.Itoa(h.Attempt)})
	}
	if h.Seconds > 0 {
		kv = append(kv, [2]string{"seconds", strconv.Itoa(h.Seconds)})
	}
	if h.Summary != "" {
		kv = append(kv, [2]string{"summary", h.Summary})
	}
	return []string{axi.KV("verification", kv)}, nil
}
