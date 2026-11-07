package verifydeliver

// A keep the closing round finds already fixed (GATE-15). `jig triage <T> <f>
// keep` is applied by the next round that closes (recordedTriage), which turns
// the kept ask or note into a fix slice. When the code moved since the round
// that raised the finding, and the closing round's reviewer no longer reports
// it (neither --still-present nor --prior), the work it asked for was built in
// between: the keep is spent as a fix, not queued again. Recorded and shown,
// never refused: the round's findings.yaml clears the finding, and the review
// notes read it kept, then fixed.

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/develdeco/jig/internal/store"
	"gopkg.in/yaml.v3"
)

// keptAndFixed returns, of the outstanding asks and notes (the earlier rounds'
// findings this round's reviewer did not report again), the ids that carry a
// pending keep from `jig triage` and were raised at another head than the one
// this round reviewed. At the same head nothing can have fixed the finding, so
// its keep applies as it always did; a head either round does not record
// (a scripted round) cannot show that the code moved, so it applies too. A
// dismiss is not touched, a keep on a file the round's reviewer did not read
// applies as it always did, and a keep without a build target counts like any
// other: the work is done whatever the target.
func keptAndFixed(st *store.Store, ticket string, round int, outstanding []Finding, files roundFiles, rv *Review) ([]KeptFixed, error) {
	reviewed := map[string]bool{}
	for _, p := range rv.Result.ReviewedPaths {
		if norm, err := normalizeRepoRelPath(p); err == nil {
			reviewed[norm] = true
		}
	}
	decisions, err := ReadTriageDecisions(st, ticket)
	if err != nil {
		return nil, err
	}
	var out []KeptFixed
	for _, f := range outstanding {
		d, ok := decisions[f.ID]
		if !ok || !d.Pending || d.Outcome != TriageKeep {
			continue
		}
		// A keep is spent as fixed only on code the round read: the finding's
		// file must be in reviewed_paths (a noted finding's file is put into
		// must_review while its keep is pending, so a reviewer has to read it).
		if !reviewed[f.File] {
			continue
		}
		repo, _, ok := files.of(f.File)
		if !ok {
			continue
		}
		raised, err := headRaisedAt(st, ticket, round, f.ID, repo.Name)
		if err != nil {
			return nil, err
		}
		now := rv.headOf(repo.Name)
		if raised != "" && now != "" && raised != now {
			out = append(out, KeptFixed{ID: f.ID, By: d.Provenance, File: f.File, Title: f.Title})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// KeptFixed is a keep spent as fixed at a round's close: the finding, and who
// kept it ("person" or "agent"). The round's findings.yaml records ID and By
// beside cleared, the gate report lists it, and `jig status` counts it.
type KeptFixed struct {
	ID string `yaml:"id"`
	By string `yaml:"by"`
	// File and Title are the finding's, for the gate report; not recorded.
	File  string `yaml:"-"`
	Title string `yaml:"-"`
}

// keptFixedIDs is the ids of ks.
func keptFixedIDs(ks []KeptFixed) []string {
	var ids []string
	for _, k := range ks {
		ids = append(ids, k.ID)
	}
	return ids
}

// headRaisedAt is the head the latest earlier round that holds finding id
// reviewed in repo, "" when no round holds it or that round recorded none.
func headRaisedAt(st *store.Store, ticket string, round int, id, repo string) (string, error) {
	for n := round - 1; n >= 1; n-- {
		ff, ok, err := readFindingsYAML(st, ticket, n)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		held := false
		for _, f := range ff.Findings {
			if f.ID == id {
				held = true
				break
			}
		}
		if !held {
			continue
		}
		data, err := os.ReadFile(filepath.Join(gateRoundDir(st, ticket, n), "report.yaml"))
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			return "", err
		}
		var rep reportYAML
		if err := yaml.Unmarshal(data, &rep); err != nil {
			return "", err
		}
		return rep.ReviewedSHA[repo], nil
	}
	return "", nil
}

// withoutIDs is fs less the findings whose id is in ids.
func withoutIDs(fs []Finding, ids []string) []Finding {
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	var out []Finding
	for _, f := range fs {
		if !drop[f.ID] {
			out = append(out, f)
		}
	}
	return out
}

// mergeIDs is a and b as one sorted list without repeats.
func mergeIDs(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range append(append([]string(nil), a...), b...) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
