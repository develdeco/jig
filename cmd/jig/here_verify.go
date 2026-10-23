package main

import (
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// init registers `jig here`'s verification section (sections.go).
func init() {
	registerHereSection(hereSection{
		Order:  320,
		Name:   "verify",
		Blocks: hereVerifyBlocks,
		JSON: func(c hereCtx) (any, error) {
			v, err := hereVerification(c)
			if err != nil || (len(v.Pending) == 0 && v.Head == nil) {
				return nil, err
			}
			return v, nil
		},
	})
}

// hereVerifyPending is one verification the journal shows started and not
// finished, in `jig here --json`.
type hereVerifyPending struct {
	Slice   string `json:"slice"`
	Commit  string `json:"commit"`
	Attempt int    `json:"attempt"`
	Since   string `json:"since,omitempty"`
	Session string `json:"session,omitempty"`
}

// hereVerifyHead is the latest verification of the located slice at the
// repo's HEAD, in `jig here --json`.
type hereVerifyHead struct {
	Slice   string `json:"slice"`
	Commit  string `json:"commit"`
	State   string `json:"state"`
	Attempt int    `json:"attempt,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// hereVerifyInfo is what the section knows of the located ticket.
type hereVerifyInfo struct {
	Pending []hereVerifyPending `json:"pending,omitempty"`
	Head    *hereVerifyHead     `json:"head,omitempty"`
}

// hereVerification reads the ticket's verifications from its journal: the ones
// still pending, and, when `jig here` located a slice in a repo, how HEAD
// stands for that slice. A directory that located no ticket, or a store that
// did not resolve, has none.
func hereVerification(c hereCtx) (hereVerifyInfo, error) {
	var info hereVerifyInfo
	if c.Store == nil || c.Where.Ticket == "" {
		return info, nil
	}
	lines, err := journal.Read(c.Store, c.Where.Ticket)
	if err != nil {
		return info, err
	}
	for _, p := range journal.Pending(lines) {
		info.Pending = append(info.Pending, hereVerifyPending{Slice: p.Slice, Commit: p.Commit, Attempt: p.Attempt, Since: p.TS, Session: p.Session})
	}
	if c.Where.Slice != "" && c.Where.RepoDir != "" {
		// A repo with no commit yet has no HEAD; the section then says nothing of it.
		if head, err := gitx.RevParse(c.Where.RepoDir, "HEAD"); err == nil {
			if v := journal.VerificationState(lines, head, c.Where.Slice); v.State != journal.VerifyNone {
				info.Head = &hereVerifyHead{Slice: c.Where.Slice, Commit: head, State: v.State, Attempt: v.Attempt, Seconds: v.Seconds, Summary: v.Summary}
			}
		}
	}
	return info, nil
}

// hereVerifyBlocks renders the section: a verifying table for the pending
// verifications and a verification block for HEAD's.
func hereVerifyBlocks(c hereCtx) ([]string, error) {
	info, err := hereVerification(c)
	if err != nil {
		return nil, err
	}
	var blocks []string
	if len(info.Pending) > 0 {
		rows := make([][]string, len(info.Pending))
		for i, p := range info.Pending {
			rows[i] = []string{p.Slice, shortSHA(p.Commit), strconv.Itoa(p.Attempt), p.Since}
		}
		blocks = append(blocks, axi.Table("verifying", []string{"slice", "commit", "attempt", "since"}, rows))
	}
	if h := info.Head; h != nil {
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
		blocks = append(blocks, axi.KV("verification", kv))
	}
	return blocks, nil
}
