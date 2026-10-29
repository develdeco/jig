package verifydeliver

// The probe ledger (ADR 0036): every round the reviewer probes each term of
// the critical paths' changed code - mutates it in the round's worktree, runs
// the narrowest test command that covers it, puts the file back - unless an
// earlier round's probe of it is current, and sets aside with a skip the code
// on no critical path. jig keeps each probe and skip in the round's
// findings.yaml with the git blob of its file at the round's head, and hands
// the next round every earlier probe as current or stale and every hunk of the
// whole change with what covers it. Which terms matter and whether a survivor
// is a problem stay the reviewer's judgment (ADR 0007): jig checks shape and
// compares blobs.

import (
	"fmt"
	"sort"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// ProbeRecord is one probe as findings.yaml keeps it: what the reviewer
// reported, and the git blob of its file at the round's head, which says
// whether a later head still has the code it probed. Round is the round that
// made it, set when the ledger is read back and never written.
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

// SkipRecord is one skip as findings.yaml keeps it, with its file's blob at
// the round's head; Round as for a ProbeRecord.
type SkipRecord struct {
	Round int    `yaml:"-"`
	File  string `yaml:"file"`
	From  int    `yaml:"from,omitempty"`
	To    int    `yaml:"to,omitempty"`
	Why   string `yaml:"why"`
	Blob  string `yaml:"blob"`
}

// result is the skip as the reviewer reported it.
func (s SkipRecord) result() ResultSkip {
	return ResultSkip{File: s.File, From: s.From, To: s.To, Why: s.Why}
}

// normalizeLedger normalizes the file of each probe and skip, once the result
// has passed validateReviewResult: what findings.yaml keeps is the file as the
// round names it.
func normalizeLedger(probes []ResultProbe, skips []ResultSkip) ([]ResultProbe, []ResultSkip) {
	var outP []ResultProbe
	for _, p := range probes {
		if norm, err := normalizeRepoRelPath(p.File); err == nil {
			p.File = norm
		}
		outP = append(outP, p)
	}
	var outS []ResultSkip
	for _, s := range skips {
		if norm, err := normalizeRepoRelPath(s.File); err == nil {
			s.File = norm
		}
		outS = append(outS, s)
	}
	return outP, outS
}

// placeLedgerFile refuses the file of a drafted probe or skip when it names
// no repo of the round; whether it is present at head is checked when the
// round is done, as a finding's file is.
func (rf roundFiles) placeLedgerFile(file string) error {
	norm, err := normalizeRepoRelPath(file)
	if err != nil {
		return reviewInvalid(fmt.Sprintf("file %q: %v", file, err))
	}
	if _, _, ok := rf.of(norm); !ok {
		return reviewInvalid(rf.unknownRepoFile(file))
	}
	return nil
}

// UnprobedAfter is the hunks of the open round p that are still unprobed once
// its drafted probes and skips are counted with the earlier rounds' current
// ones: what is left of the sweep.
func UnprobedAfter(p Prepared, drafts []Draft) []HunkEntry {
	res := DraftResult(drafts, nil, "")
	probes, skips := normalizeLedger(res.Probes, res.Skipped)
	return unprobedHunks(coverHunks(p.Request.Hunks, probes, skips))
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

// recordLedger is a round's probes and skips as findings.yaml keeps them: each
// with its file's blob at the head the round reviewed.
func recordLedger(rf roundFiles, probes []ResultProbe, skips []ResultSkip) ([]ProbeRecord, []SkipRecord, error) {
	if len(probes) == 0 && len(skips) == 0 {
		return nil, nil, nil
	}
	var files []string
	for _, p := range probes {
		files = append(files, p.File)
	}
	for _, s := range skips {
		files = append(files, s.File)
	}
	blobs, err := rf.headBlobs(files)
	if err != nil {
		return nil, nil, err
	}
	var outP []ProbeRecord
	for _, p := range probes {
		outP = append(outP, ProbeRecord{
			Seam: p.Seam, File: p.File, Line: p.Line, Mutation: p.Mutation, Command: p.Command, Outcome: p.Outcome,
			Blob: blobs[p.File],
		})
	}
	var outS []SkipRecord
	for _, s := range skips {
		outS = append(outS, SkipRecord{File: s.File, From: s.From, To: s.To, Why: s.Why, Blob: blobs[s.File]})
	}
	return outP, outS, nil
}

// ledgerBefore reads the probes and skips of every gate/round-<n>/findings.yaml
// for ticket with n < round, oldest first, each with its round.
func ledgerBefore(st *store.Store, ticket string, round int) ([]ProbeRecord, []SkipRecord, error) {
	var probes []ProbeRecord
	var skips []SkipRecord
	for r := 1; r < round; r++ {
		ff, ok, err := readFindingsYAML(st, ticket, r)
		if err != nil {
			return nil, nil, fmt.Errorf("verifydeliver: probes: read round %d findings.yaml: %w", r, err)
		}
		if !ok {
			continue
		}
		for _, p := range ff.Probes {
			p.Round = r
			probes = append(probes, p)
		}
		for _, s := range ff.Skipped {
			s.Round = r
			skips = append(skips, s)
		}
	}
	return probes, skips, nil
}

// currentLedger splits the ledger by its files' blobs at the head under
// review: probed is every probe, current or stale, and current and skips are
// the probes and skips that are current.
func currentLedger(rf roundFiles, probes []ProbeRecord, skips []SkipRecord) (probed []ProbedEntry, current []ResultProbe, currentSkips []ResultSkip, err error) {
	var files []string
	for _, p := range probes {
		files = append(files, p.File)
	}
	for _, s := range skips {
		files = append(files, s.File)
	}
	blobs, err := rf.headBlobs(files)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, p := range probes {
		state := ProbeStale
		if b, ok := blobs[p.File]; ok && b == p.Blob {
			state = ProbeCurrent
			current = append(current, p.result())
		}
		probed = append(probed, ProbedEntry{Round: p.Round, ResultProbe: p.result(), State: state})
	}
	for _, s := range skips {
		if b, ok := blobs[s.File]; ok && b == s.Blob {
			currentSkips = append(currentSkips, s.result())
		}
	}
	return probed, current, currentSkips, nil
}

// changeHunks is every hunk of the whole change at the head under review in
// each repo of rf, from bases[repo], the base a full round of the ticket uses:
// the files named as the round names them.
func changeHunks(rf roundFiles, bases map[string]string) ([]HunkEntry, error) {
	var hunks []HunkEntry
	for _, r := range rf.repos {
		got, err := gitx.DiffHunks(r.Dir, bases[r.Name], r.Head)
		if err != nil {
			return nil, inRepo(rf.qualified, r.Name, fmt.Errorf("verifydeliver: probes: diff the change's hunks: %w", err))
		}
		for _, h := range got {
			hunks = append(hunks, HunkEntry{File: rf.qualify(r.Name, h.Path), From: h.From, To: h.To})
		}
	}
	return hunks, nil
}

// coverHunks is hunks with probes and skips added: each hunk counts the probes
// of its file whose line falls inside it, and takes the reason of the first
// skip that covers it when it has none yet.
func coverHunks(hunks []HunkEntry, probes []ResultProbe, skips []ResultSkip) []HunkEntry {
	out := make([]HunkEntry, len(hunks))
	for i, h := range hunks {
		for _, p := range probes {
			if p.File == h.File && h.From <= p.Line && p.Line <= h.To {
				h.Probes++
			}
		}
		for _, s := range skips {
			if h.Skip == "" && s.covers(h.File, h.From, h.To) {
				h.Skip = s.Why
			}
		}
		out[i] = h
	}
	return out
}

// unprobedHunks is the hunks no probe falls inside and no skip covers.
func unprobedHunks(hunks []HunkEntry) []HunkEntry {
	var out []HunkEntry
	for _, h := range hunks {
		if h.Unprobed() {
			out = append(out, h)
		}
	}
	return out
}

// behaviorCoverage is what a repo's review notes say of the behavior its
// review tested (ADR 0036): the probes current at the reviewed head, the skips
// current there, and the hunks of the change neither covers, with files named
// as paths in the repo.
type behaviorCoverage struct {
	Probes   []ProbedEntry
	Skips    []ResultSkip
	Unprobed []HunkEntry
}

// publishCoverage is t's behavior coverage at head, read in the lease at dir:
// every round's probes and skips up to the last, current by their files' blobs
// at head, against the hunks of the change from its merge base with the
// target. The probes and skips of the ticket's other repos are left out.
func publishCoverage(st *store.Store, t *publishTarget, dir, head string) (behaviorCoverage, error) {
	probes, skips, err := ledgerBefore(st, t.ticket, t.lastRound+1)
	if err != nil {
		return behaviorCoverage{}, err
	}
	rf := roundFiles{qualified: t.qualified, repos: []roundRepo{{Name: t.repoName, Dir: dir, Head: head}}}
	var mineP []ProbeRecord
	for _, p := range probes {
		if _, _, ok := rf.of(p.File); ok {
			mineP = append(mineP, p)
		}
	}
	var mineS []SkipRecord
	for _, s := range skips {
		if _, _, ok := rf.of(s.File); ok {
			mineS = append(mineS, s)
		}
	}
	probed, current, currentSkips, err := currentLedger(rf, mineP, mineS)
	if err != nil {
		return behaviorCoverage{}, err
	}
	base, err := gitx.MergeBase(dir, "origin/"+t.target, head)
	if err != nil {
		return behaviorCoverage{}, fmt.Errorf("verifydeliver: publish: merge-base origin/%s %s: %w", t.target, head, err)
	}
	hunks, err := changeHunks(rf, map[string]string{t.repoName: base})
	if err != nil {
		return behaviorCoverage{}, err
	}
	rel := func(file string) string {
		_, r, _ := rf.of(file)
		return r
	}
	var cov behaviorCoverage
	for _, pr := range probed {
		if pr.State == ProbeCurrent {
			pr.File = rel(pr.File)
			cov.Probes = append(cov.Probes, pr)
		}
	}
	for _, s := range currentSkips {
		s.File = rel(s.File)
		cov.Skips = append(cov.Skips, s)
	}
	for _, h := range unprobedHunks(coverHunks(hunks, current, currentSkips)) {
		h.File = rel(h.File)
		cov.Unprobed = append(cov.Unprobed, h)
	}
	return cov, nil
}

// requestLedger works out review.json's probed and hunks lists for a round
// whose repos rf reads at the heads under review: the earlier rounds' probes,
// current or stale, and the hunks of the whole change from fullBases with the
// current probes and skips that cover them.
func requestLedger(rf roundFiles, fullBases map[string]string, probes []ProbeRecord, skips []SkipRecord) ([]ProbedEntry, []HunkEntry, error) {
	probed, current, currentSkips, err := currentLedger(rf, probes, skips)
	if err != nil {
		return nil, nil, err
	}
	hunks, err := changeHunks(rf, fullBases)
	if err != nil {
		return nil, nil, err
	}
	// Both lists are empty, never nil, as the request read back from
	// review.json has them.
	if probed == nil {
		probed = []ProbedEntry{}
	}
	return probed, coverHunks(hunks, current, currentSkips), nil
}
