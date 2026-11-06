package observe

import (
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/phase"
)

// A session that edits a file in the checkout of the ticket it works on, with
// no slice of the ticket claimed, hears what to do first: claim a slice, or,
// while the brief has no approval, finish the brief. It is advice, never
// enforced: a person may have chosen to edit first. The Ref names the ticket
// and which of the two, so each is said once.

// unclaimedEditNotes is the note for an edit event whose files lie inside the
// checkout, nil for anything else.
func (h *handler) unclaimedEditNotes(mem sessionMemory) []Note {
	if h.ev.Kind != KindTool || len(h.ev.Files) == 0 || h.d.Derive == nil || h.w.Ticket == "" {
		return nil
	}
	ticket := h.w.Ticket
	briefRef, claimRef := "unclaimed-edit:"+ticket+":brief", "unclaimed-edit:"+ticket+":claim"
	if mem.delivered[briefRef] && mem.delivered[claimRef] {
		return nil
	}
	if !h.editsInCheckout() {
		return nil
	}
	t, err := h.d.Derive(h.st, ticket)
	if err != nil {
		h.fail(fmt.Errorf("phase of %s: %w", ticket, err))
		return nil
	}
	if t.Code == phase.CodeDone || t.Code == phase.CodeClosed {
		return nil
	}
	if claimedSlice(t) {
		return nil
	}
	switch t.Current {
	case phase.Brief:
		return []Note{{
			Ref: briefRef, Source: "unclaimed-edit",
			Text: fmt.Sprintf("%s has no approved brief yet: finish the brief with the intake skill (`jig skill intake`) and `jig validate %s` before changing code.",
				ticket, ticket),
		}}
	case phase.Build:
		var ready []string
		for _, s := range t.Of(phase.Build).Subs {
			id, ok := strings.CutPrefix(s.Key, "slice:")
			if !ok {
				continue
			}
			if s.State == phase.SubPending {
				ready = append(ready, id)
			}
		}
		text := fmt.Sprintf("No slice of %s is claimed: claim one before changing code (`jig claim %s --slice <slice>`", ticket, ticket)
		if len(ready) > 0 {
			text += "; ready: " + strings.Join(ready, ", ")
		}
		return []Note{{Ref: claimRef, Source: "unclaimed-edit", Text: text + ")."}}
	}
	return nil
}

// claimedSlice reports whether a slice of the ticket holds a claim: building,
// parked for an answer or stalled, or parked on its environment. The build
// phase's slices are listed while the brief is current too.
func claimedSlice(t phase.Ticket) bool {
	for _, s := range t.Of(phase.Build).Subs {
		if !strings.HasPrefix(s.Key, "slice:") {
			continue
		}
		switch s.State {
		case phase.SubActive, phase.SubNeedsYou, phase.SubFailed:
			return true
		case phase.SubWaiting:
			if s.Detail == phase.WaitsForEnvironment {
				return true
			}
		}
	}
	return false
}

// editsInCheckout reports whether an edited file of the event lies inside the
// checkout the event happened in (the git top level of its directory, else the
// directory itself). Either slash separates a path, and a relative file is
// relative to the directory.
func (h *handler) editsInCheckout() bool {
	cwd := h.w.Cwd
	if cwd == "" {
		return false
	}
	root := cwd
	if top, err := gitx.TopLevel(cwd); err == nil {
		root = top
	}
	root = pathKey(root)
	for _, f := range h.ev.Files {
		if f == "" {
			continue
		}
		if !filepath.IsAbs(f) && !strings.HasPrefix(f, "/") && !strings.HasPrefix(f, `\`) {
			f = filepath.Join(cwd, f)
		}
		if k := pathKey(f); k == root || strings.HasPrefix(k, root+"/") {
			return true
		}
	}
	return false
}

// pathKey is p cleaned with forward slashes, lower case on Windows, to
// compare two paths that may be written either way.
func pathKey(p string) string {
	k := path.Clean(strings.ReplaceAll(p, `\`, "/"))
	if runtime.GOOS == "windows" {
		k = strings.ToLower(k)
	}
	return k
}
