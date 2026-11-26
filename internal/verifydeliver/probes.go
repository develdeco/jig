package verifydeliver

// The probe record (ADR 0036, as ADR 0067 keeps it): a probe is a mutation that
// proves one finding - one term mutated in the round's worktree, the narrowest
// test command run against it, the file put back - and recording it is the
// reviewer's choice. jig keeps each probe in the round's findings.yaml with the
// git blob of its file at the round's head, and hands the next round every
// earlier probe as current or stale. Whether a survivor is a problem stays the
// reviewer's judgment (ADR 0007): jig checks shape and compares blobs.

import (
	"fmt"
	"sort"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// ProbeRecord is one probe as findings.yaml keeps it: what the reviewer
// reported, and the git blob of its file at the round's head, which says
// whether a later head still has the code it probed. Round is the round that
// made it, set when the probes are read back and never written.
type ProbeRecord struct {
	Round    int    `yaml:"-"`
	Seam     string `yaml:"seam"`
	File     string `yaml:"file"`
	Line     int    `yaml:"line"`
	Mutation string `yaml:"mutation"`
	Command  string `yaml:"command"`
	Outcome  string `yaml:"outcome"`
	Blob     string `yaml:"blob"`
}

// result is the probe as the reviewer reported it.
func (p ProbeRecord) result() ResultProbe {
	return ResultProbe{Seam: p.Seam, File: p.File, Line: p.Line, Mutation: p.Mutation, Command: p.Command, Outcome: p.Outcome}
}

// normalizeProbes normalizes the file of each probe, once the result has
// passed validateReviewResult: what findings.yaml keeps is the file as the
// round names it.
func normalizeProbes(probes []ResultProbe) []ResultProbe {
	var out []ResultProbe
	for _, p := range probes {
		if norm, err := normalizeRepoRelPath(p.File); err == nil {
			p.File = norm
		}
		out = append(out, p)
	}
	return out
}

// placeProbeFile refuses the file of a drafted probe when it names no repo of
// the round or is not present at head there. It is checked as the draft is
// taken, not only when the round is done: a probe is always of code at head,
// and a draft cannot be taken back, so one mistyped file would otherwise leave
// the round unable to close.
func (rf roundFiles) placeProbeFile(file string) error {
	norm, err := normalizeRepoRelPath(file)
	if err != nil {
		return reviewInvalid(fmt.Sprintf("file %q: %v", file, err))
	}
	if _, _, ok := rf.of(norm); !ok {
		return reviewInvalid(rf.unknownRepoFile(file))
	}
	present, err := rf.atHead(norm)
	if err != nil {
		return fmt.Errorf("verifydeliver: probes: check %q at head: %w", norm, err)
	}
	if !present {
		return reviewInvalid(fmt.Sprintf("file %q is not present at head", file))
	}
	return nil
}

// headBlobs is the blob of each of files at the commit under review in its
// repo, by the file as the round names it: one ls-tree per repo. A file that
// is not a blob at head is left out.
func (rf roundFiles) headBlobs(files []string) (map[string]string, error) {
	byRepo := rf.byRepo(files)
	blobs := map[string]string{}
	for _, r := range rf.repos {
		rels := uniqueSorted(byRepo[r.Name])
		if len(rels) == 0 {
			continue
		}
		got, err := gitx.BlobsAtRev(r.Dir, r.Head, rels)
		if err != nil {
			return nil, inRepo(rf.qualified, r.Name, fmt.Errorf("verifydeliver: probes: read blobs at head: %w", err))
		}
		for rel, blob := range got {
			blobs[rf.qualify(r.Name, rel)] = blob
		}
	}
	return blobs, nil
}

// uniqueSorted is list sorted, without repeats.
func uniqueSorted(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// recordProbes is a round's probes as findings.yaml keeps them: each with its
// file's blob at the head the round reviewed.
func recordProbes(rf roundFiles, probes []ResultProbe) ([]ProbeRecord, error) {
	if len(probes) == 0 {
		return nil, nil
	}
	var files []string
	for _, p := range probes {
		files = append(files, p.File)
	}
	blobs, err := rf.headBlobs(files)
	if err != nil {
		return nil, err
	}
	var out []ProbeRecord
	for _, p := range probes {
		out = append(out, ProbeRecord{
			Seam: p.Seam, File: p.File, Line: p.Line, Mutation: p.Mutation, Command: p.Command, Outcome: p.Outcome,
			Blob: blobs[p.File],
		})
	}
	return out, nil
}

// probesBefore reads the probes of every gate/round-<n>/findings.yaml for
// ticket with n < round, oldest first, each with its round.
func probesBefore(st *store.Store, ticket string, round int) ([]ProbeRecord, error) {
	var probes []ProbeRecord
	for r := 1; r < round; r++ {
		ff, ok, err := readFindingsYAML(st, ticket, r)
		if err != nil {
			return nil, fmt.Errorf("verifydeliver: probes: read round %d findings.yaml: %w", r, err)
		}
		if !ok {
			continue
		}
		for _, p := range ff.Probes {
			p.Round = r
			probes = append(probes, p)
		}
	}
	return probes, nil
}

// probedEntries is each of probes, current or stale at the head under review,
// as review.json's probed list has them: empty, never nil, as the request read
// back from review.json has it. A probe is current while its file's blob is the
// one it recorded and no later round probed the same line: the latest round to
// probe a line says what it is now, so a survivor that a later round probed
// again after its fix does not still read as current.
func probedEntries(rf roundFiles, probes []ProbeRecord) ([]ProbedEntry, error) {
	var files []string
	latest := map[string]int{}
	for _, p := range probes {
		files = append(files, p.File)
		if at := fmt.Sprintf("%s:%d", p.File, p.Line); p.Round > latest[at] {
			latest[at] = p.Round
		}
	}
	blobs, err := rf.headBlobs(files)
	if err != nil {
		return nil, err
	}
	probed := []ProbedEntry{}
	for _, p := range probes {
		state := ProbeStale
		if b, ok := blobs[p.File]; ok && b == p.Blob && p.Round == latest[fmt.Sprintf("%s:%d", p.File, p.Line)] {
			state = ProbeCurrent
		}
		probed = append(probed, ProbedEntry{Round: p.Round, ResultProbe: p.result(), State: state})
	}
	return probed, nil
}

// publishProbes is what t's review notes say of the probes: every round's, up to
// the last, that are current at the reviewed head, read in the repository at
// dir, with files named as paths in the repo. The probes of the ticket's other
// repos are left out.
func publishProbes(st *store.Store, t *publishTarget, dir, head string) ([]ProbedEntry, error) {
	probes, err := probesBefore(st, t.ticket, t.lastRound+1)
	if err != nil {
		return nil, err
	}
	rf := roundFiles{qualified: t.qualified, repos: []roundRepo{{Name: t.repoName, Dir: dir, Head: head}}}
	var mine []ProbeRecord
	for _, p := range probes {
		if _, _, ok := rf.of(p.File); ok {
			mine = append(mine, p)
		}
	}
	probed, err := probedEntries(rf, mine)
	if err != nil {
		return nil, err
	}
	var current []ProbedEntry
	for _, pr := range probed {
		if pr.State == ProbeCurrent {
			_, pr.File, _ = rf.of(pr.File)
			current = append(current, pr)
		}
	}
	return current, nil
}
