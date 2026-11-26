package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 50, Name: "brief", Blocks: briefBlocks})
}

// The words the brief block's approval line uses.
const (
	briefApprovalNone    = "none"
	briefApprovalCurrent = "current"
	briefApprovalChanged = "changed"
)

// The brief block also carries the bare identity ("identity"), so a reader that
// words an approval itself (the TUI, through phase.Approver) never words the
// already-worded approved_by a second time.

// approvedByWords is who the brief block says approved: the identity for a
// person, "an agent (as <identity>)" when an agent recorded the approval, so
// an agent's never reads as a person's.
func approvedByWords(a store.Approval) string {
	if a.Provenance == "agent" {
		return "an agent (as " + a.By + ")"
	}
	return a.By
}

// briefBlocks is the brief's part of `jig status`: whether the brief as it is
// now is the one approved, who approved it and when, and its "## " sections,
// each with the slices that cite it (their from_brief hashes). A ticket with
// no brief.md has no block.
func briefBlocks(st *store.Store, ticket string) ([]string, error) {
	md, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return nil, err
	}
	state, approval, err := briefApprovalOf(st, ticket)
	if err != nil {
		return nil, err
	}
	pairs := [][2]string{{"approval", state}}
	if state != briefApprovalNone {
		pairs = append(pairs, [2]string{"approved_by", approvedByWords(approval)}, [2]string{"identity", approval.By}, [2]string{"approved_at", approval.At})
		if approval.Provenance != "" {
			pairs = append(pairs, [2]string{"provenance", approval.Provenance})
		}
	}
	sections := store.BriefSections(md)
	since, removed := sectionsSince(state, approval, sections)
	if state != briefApprovalNone {
		illustration, err := illustrationSince(st, ticket, approval)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, [2]string{"illustration_since_approval", illustration})
	}
	blocks := []string{axi.KV("brief", pairs)}

	var rows [][]string
	for i, sec := range sections {
		var citing []string
		for _, sl := range slices {
			for _, h := range sl.FromBrief {
				if strings.EqualFold(h, sec.Hash) {
					citing = append(citing, sl.ID)
					break
				}
			}
		}
		cited := "-"
		if len(citing) > 0 {
			cited = joinPlus(citing)
		}
		rows = append(rows, []string{sec.Heading, cited, since[i]})
	}
	if len(rows) > 0 {
		blocks = append(blocks, axi.Table("brief_sections", []string{"heading", "slices", "since_approval"}, rows))
	}
	if len(removed) > 0 {
		var rr [][]string
		for _, h := range removed {
			rr = append(rr, []string{h})
		}
		blocks = append(blocks, axi.Table("brief_removed", []string{"heading"}, rr))
	}
	return blocks, nil
}

// The words since_approval and illustration_since_approval use.
const (
	sinceSame    = "same"
	sinceChanged = "changed"
	sinceUnknown = "-"
)

// sectionsSince says, for each section of the brief as it is, whether its text
// is as the approval recorded it (same) or was edited or is new (changed), and
// lists the approved headings the brief no longer has. Without an approval, or
// with one that recorded no sections (made before they were), nothing is said:
// every section is "-" and none is removed, so a screen never invents a change.
// A section is told by heading and hash together, so a heading that repeats is
// matched once each.
func sectionsSince(state string, a store.Approval, sections []store.BriefSection) (since, removed []string) {
	since = make([]string, len(sections))
	if state == briefApprovalNone || len(a.Sections) == 0 {
		for i := range since {
			since[i] = sinceUnknown
		}
		return since, nil
	}
	unmatched := make([]bool, len(a.Sections))
	for i := range unmatched {
		unmatched[i] = true
	}
	for i, sec := range sections {
		since[i] = sinceChanged
		for j, ap := range a.Sections {
			if unmatched[j] && ap.Heading == sec.Heading && strings.EqualFold(ap.Hash, sec.Hash) {
				unmatched[j] = false
				since[i] = sinceSame
				break
			}
		}
	}
	// An approved section is removed when no section of the brief now bears
	// its heading; an edited one reads "changed" above, not removed.
	for j, ap := range a.Sections {
		if !unmatched[j] {
			continue
		}
		kept := false
		for _, sec := range sections {
			if sec.Heading == ap.Heading {
				kept = true
				break
			}
		}
		if !kept {
			removed = append(removed, ap.Heading)
		}
	}
	return since, removed
}

// illustrationSince says how the illustration stands against the approval:
// none (neither then nor now), same, changed, added, removed, or "-" when the
// approval recorded no sections and so nothing of what it covered.
func illustrationSince(st *store.Store, ticket string, a store.Approval) (string, error) {
	if len(a.Sections) == 0 {
		return sinceUnknown, nil
	}
	_, now, err := st.BriefRecord(ticket)
	if err != nil {
		return "", err
	}
	switch {
	case now == "" && a.IllustrationSHA256 == "":
		return "none", nil
	case now == "":
		return "removed", nil
	case a.IllustrationSHA256 == "":
		return "added", nil
	case strings.EqualFold(now, a.IllustrationSHA256):
		return sinceSame, nil
	}
	return sinceChanged, nil
}
