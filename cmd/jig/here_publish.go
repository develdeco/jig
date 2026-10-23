package main

import (
	"sort"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// init registers `jig here`'s gate and publish section (sections.go): how the
// located ticket stands in review and in publishing - the round open now, the
// latest round's verdict and whether its reviewer built what it reviewed, and
// what a publish has recorded of the pull request - so an agent that is
// asked to review, or to publish, finds the state without a command of its
// own.
func init() {
	registerHereSection(hereSection{
		Order:  330,
		Name:   "publish",
		Blocks: herePublishBlocks,
		JSON: func(c hereCtx) (any, error) {
			s, ok, err := herePublishStatus(c)
			if err != nil || !ok {
				return nil, err
			}
			return s, nil
		},
	})
}

// herePublishStatus reads the located ticket's gate and publish state: ok is
// false when `jig here` located no store or no ticket, or the ticket has no
// gate or publish record to show.
func herePublishStatus(c hereCtx) (verifydeliver.Status, bool, error) {
	if c.Store == nil || c.Where.Ticket == "" {
		return verifydeliver.Status{}, false, nil
	}
	s, err := verifydeliver.ReadStatus(c.Store, c.Where.Ticket)
	if err != nil {
		return verifydeliver.Status{}, false, err
	}
	if s.Rounds == 0 && s.Open == nil && s.BodyFor == "" && s.PR == "" {
		return verifydeliver.Status{}, false, nil
	}
	return s, true, nil
}

// herePublishBlocks renders the section: the open round, the latest round and
// the publish.
func herePublishBlocks(c hereCtx) ([]string, error) {
	s, ok, err := herePublishStatus(c)
	if err != nil || !ok {
		return nil, err
	}
	var blocks []string
	if s.Open != nil {
		kv := [][2]string{
			{"round", strconv.Itoa(s.Open.Round)}, {"head", shortSHA(s.Open.Head)}, {"copy", s.Open.Copy},
			{"drafts", strconv.Itoa(s.Open.Drafts)},
		}
		if s.Open.Reviewer.Known() {
			kv = append(kv, [2]string{"reviewer", reviewerLabel(s.Open.Reviewer)})
		}
		blocks = append(blocks, axi.KV("review", kv))
	}
	if s.Rounds > 0 {
		kv := [][2]string{{"rounds", strconv.Itoa(s.Rounds)}, {"verdict", s.Verdict}}
		if s.Independent != "" {
			kv = append(kv, [2]string{"independent", string(s.Independent)})
		}
		if s.Reviewer != nil && s.Reviewer.Known() {
			kv = append(kv, [2]string{"reviewer", reviewerLabel(*s.Reviewer)})
		}
		repos := make([]string, 0, len(s.ReviewedSHA))
		for repo := range s.ReviewedSHA {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		for _, repo := range repos {
			kv = append(kv, [2]string{"reviewed_" + repo, shortSHA(s.ReviewedSHA[repo])})
		}
		blocks = append(blocks, axi.KV("gate", kv))
	}
	if s.Verdict == "clean" || s.BodyFor != "" || s.ApprovedFor != "" || s.PR != "" {
		kv := [][2]string{}
		if s.BodyFor != "" {
			kv = append(kv, [2]string{"body_for", shortSHA(s.BodyFor)})
		}
		if s.ApprovedFor != "" {
			kv = append(kv, [2]string{"approved_for", shortSHA(s.ApprovedFor)})
		}
		if s.PR != "" {
			kv = append(kv, [2]string{"pr", s.PR})
		}
		kv = append(kv, [2]string{"published", map[bool]string{true: "yes", false: "no"}[s.Published]})
		blocks = append(blocks, axi.KV("publish", kv))
	}
	return blocks, nil
}
