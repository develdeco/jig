package locate

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// Item is a tracker item a word named: what ResolveItem found of it in the
// tracker tree, from the store alone.
type Item struct {
	// Ref is the item's bare number, the one ref form jig prints.
	Ref string
	// Number is Ref as an integer.
	Number int
	// Repo is the repo the item is in, owner/name: the project's tracker repo
	// as project.yaml spells it, or the lower-case repo the ref carried.
	Repo string
	// Other is set when the ref names another repo than the project's tracker.
	Other bool
	// TrackerRepo is the project's tracker repo.
	TrackerRepo string
	// Bound is set when the tree holds a record for the item; Ticket is then
	// the owner's id and Kind its kind (store.KindIssue ...).
	Bound  bool
	Ticket string
	Kind   string
	// Shadow is, for a bare number that also names a chart or an earlier
	// ticket id, one advisory line saying what it hides and how to reach it.
	Shadow string
}

// RefShaped reports whether word could be a tracker item's ref: all digits, or
// containing # or ://. No other word is read as one: a ticket id is KEY-N and
// a chart name has neither # nor a slash.
func RefShaped(word string) bool {
	if word == "" {
		return false
	}
	if strings.Contains(word, "#") || strings.Contains(word, "://") {
		return true
	}
	for _, r := range word {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ResolveItem reads word as a tracker item's ref, the one resolver `jig next`
// and Locate share. A word that is not ref-shaped is nil with no error. A
// ref-shaped word the parser refuses is the parser's refusal as it is. The
// item is looked up in the tracker tree by its entry, github/<repo>/<n>; the
// record's owner is the ticket the item is bound to. Only the tree is read:
// nothing is asked of the host, and nothing is written.
//
// A project with no GitHub tracker is refused here. An item of another repo
// than the project's tracker is returned, with Other set: it can be read as
// input (mirror.CheckItemRepo refuses it and a project with no tracker alike,
// so the split is made here, in the caller's comparison).
func ResolveItem(st *store.Store, cfg project.Config, word string) (*Item, error) {
	if !RefShaped(word) {
		return nil, nil
	}
	ref, err := mirror.ParseItemRef(word)
	if err != nil {
		return nil, err
	}
	if cfg.GitHub == nil {
		// A ref with no repo cannot hit the other-repo refusal: this is the
		// no-tracker one.
		return nil, mirror.CheckItemRepo(mirror.ItemRef{Number: ref.Number}, cfg)
	}
	it := &Item{Ref: strconv.Itoa(ref.Number), Number: ref.Number, Repo: cfg.GitHub.Repo, TrackerRepo: cfg.GitHub.Repo}
	if ref.Repo != "" && !strings.EqualFold(ref.Repo, cfg.GitHub.Repo) {
		it.Repo, it.Other = ref.Repo, true
	}
	entry := store.TrackerEntry{Provider: store.ProviderGitHub, Scope: strings.ToLower(it.Repo), ID: it.Ref}
	rec, found, err := st.TrackerRecordAt(entry)
	if err != nil {
		return nil, err
	}
	if found {
		if err := bindOwner(st, rec, it); err != nil {
			return nil, err
		}
	}
	if word == it.Ref {
		it.Shadow = shadowOf(st, word)
	}
	return it, nil
}

// bindOwner reads the owner of the item's record into it: an error naming the
// record when it cannot be read, is a chart's (the old key), or names a ticket
// the store does not hold. Nothing is guessed.
func bindOwner(st *store.Store, rec store.TrackerRecord, it *Item) error {
	path := filepath.Join(st.Root, filepath.FromSlash(rec.Rel))
	fail := func(why string) error {
		return &axi.Error{
			Msg:  fmt.Sprintf("tracker item %s of %s: its record %s %s", it.Ref, it.Repo, path, why),
			Code: "VALIDATION_ERROR",
			Help: []string{"Run `jig validate` to see what is wrong with the tracker tree"},
		}
	}
	switch {
	case rec.Err != nil:
		return fail("cannot be read: " + rec.Err.Error())
	case rec.Ticket == "":
		return fail("names no ticket (a chart's record is the migration's to move, not read here)")
	case !st.TicketExists(rec.Ticket):
		return fail(fmt.Sprintf("names ticket %s, which the store does not hold", rec.Ticket))
	}
	t, err := st.ReadTicket(rec.Ticket)
	if err != nil {
		return fail(fmt.Sprintf("names ticket %s, which cannot be read: %v", rec.Ticket, err))
	}
	it.Bound, it.Ticket, it.Kind = true, rec.Ticket, t.KindOrDefault()
	return nil
}

// shadowOf is the advisory for a bare number that is also a chart's name or an
// earlier id a ticket carried: the ref wins, and the line says how to reach
// what it hides.
func shadowOf(st *store.Store, word string) string {
	if id, err := st.ResolveTicket(word); err == nil && id != word && st.TicketExists(id) {
		return fmt.Sprintf("%s is also an earlier id of ticket %s: it is read as the tracker item; reach the ticket as %s", word, id, id)
	}
	if _, err := os.Stat(st.ChartFile(word)); err == nil {
		via := "through its tickets' ids"
		if issue, _ := st.ChartIssue(word); issue != "" {
			via = "through its issue " + issue
		}
		return fmt.Sprintf("%s is also the name of a chart: it is read as the tracker item; reach the chart %s", word, via)
	}
	return ""
}
