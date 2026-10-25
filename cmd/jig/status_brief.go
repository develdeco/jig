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
		pairs = append(pairs, [2]string{"approved_by", approval.By}, [2]string{"approved_at", approval.At})
		if approval.Provenance != "" {
			pairs = append(pairs, [2]string{"provenance", approval.Provenance})
		}
	}
	blocks := []string{axi.KV("brief", pairs)}

	var rows [][]string
	for _, sec := range store.BriefSections(md) {
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
		rows = append(rows, []string{sec.Heading, cited})
	}
	if len(rows) > 0 {
		blocks = append(blocks, axi.Table("brief_sections", []string{"heading", "slices"}, rows))
	}
	return blocks, nil
}
