package journal

import (
	"fmt"
	"strings"
)

// shortSHA returns the first 7 characters of a commit sha, or the whole
// string if it is shorter.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// landed reports whether l records a slice's commit landing: a verified
// line, or, in a journal that predates verified lines (useVerified false), a
// green result line. A builder can claim green more than once per attempt
// (a red oracle run goes back to its session, ADR 0020), so only the commit
// that verified counts.
func landed(l Line, useVerified bool) bool {
	if useVerified {
		return l.Event == "verified"
	}
	return l.Event == "result" && l.Outcome == "green"
}

// hasVerified reports whether lines carry any verified line.
func hasVerified(lines []Line) bool {
	for _, l := range lines {
		if l.Event == "verified" {
			return true
		}
	}
	return false
}

// RenderChangelog renders the green slices landed in one workspace: a
// header followed by one bullet per landed commit (landed) whose slice maps
// to workspace in sliceWS. Pure and timestamp-free so it is golden-stable.
func RenderChangelog(lines []Line, workspace string, sliceWS map[string]string) string {
	useVerified := hasVerified(lines)
	var b strings.Builder
	fmt.Fprintf(&b, "# Changelog - %s\n", workspace)
	for _, l := range lines {
		if !landed(l, useVerified) {
			continue
		}
		if sliceWS[l.Slice] != workspace {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", l.Slice, shortSHA(l.Commit))
	}
	return b.String()
}

// RenderConsolidated renders every landed slice commit (landed) across all
// workspaces for a
// ticket, plus a summary of gate fix-slice rounds. The ticket name is taken
// from the first line. Pure and timestamp-free.
//
// Fix-slice rounds are grouped by the fix-slice line's Attempt field, which
// carries the gate round number the fix slice was appended for.
func RenderConsolidated(lines []Line) string {
	ticket := ""
	if len(lines) > 0 {
		ticket = lines[0].Ticket
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s - consolidated changelog\n", ticket)

	b.WriteString("\n## Slices\n")
	any := false
	useVerified := hasVerified(lines)
	for _, l := range lines {
		if !landed(l, useVerified) {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", l.Slice, shortSHA(l.Commit))
		any = true
	}
	if !any {
		b.WriteString("- none\n")
	}

	b.WriteString("\n## Fix rounds\n")
	any = false
	for _, l := range lines {
		if l.Event != "fix-slice" {
			continue
		}
		fmt.Fprintf(&b, "- round %d: fix-slice %s\n", l.Attempt, l.Slice)
		any = true
	}
	if !any {
		b.WriteString("- none\n")
	}

	return b.String()
}

// RenderDiffChangelog renders the slice commits landed (landed) between gate
// round
// round-1 (or the run start, if round-1 has no gate-round line) and gate
// round round. Gate round lines carry their round number in Attempt. Pure
// and timestamp-free.
func RenderDiffChangelog(lines []Line, round int) string {
	start := 0
	for i, l := range lines {
		if l.Event == "gate-round" && l.Attempt == round-1 {
			start = i + 1
		}
	}
	end := len(lines)
	for i, l := range lines {
		if l.Event == "gate-round" && l.Attempt == round {
			end = i
			break
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Diff changelog - round %d\n", round)
	useVerified := hasVerified(lines)
	for _, l := range lines[start:end] {
		if !landed(l, useVerified) {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", l.Slice, shortSHA(l.Commit))
	}
	return b.String()
}
