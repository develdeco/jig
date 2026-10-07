package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// cmdGraduate implements `jig graduate <chart> [--store <path>] [--project <name>]`.
func cmdGraduate(e env, args []string, stdout io.Writer) int {
	chart, rest, err := requirePositional(args, "chart name")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet(e, "graduate")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	if chart == "" {
		return renderErr(stdout, &axi.Error{
			Msg:  "jig graduate requires a chart name",
			Code: "VALIDATION_ERROR",
		})
	}

	// Validate chart name: no path separator, not . or ..
	if err := validateChartName(chart); err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	// Sync the store first
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}

	// Read the chart, just to refuse early on a missing file or an empty
	// one: claimOneChartEntry re-reads it fresh on every attempt, since what
	// it must do next (which entry still needs an id, resolved against
	// whatever a rejected claim's pull brought in) can change between them.
	entries, err := st.ReadChart(chart)
	if err != nil {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("failed to read chart %q: %v", chart, err),
			Code: "VALIDATION_ERROR",
			Help: []string{
				fmt.Sprintf("Check that charts/%s/tickets.yaml exists and is valid YAML", chart),
			},
		})
	}
	if len(entries) == 0 {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("chart %q has no entries in charts/%s/tickets.yaml", chart, chart),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Add at least one ticket entry to charts/%s/tickets.yaml", chart)},
		})
	}

	// Claim one ticket at a time, each with its own commit (and, on a store
	// with an origin, its own push): a failure partway leaves every ticket
	// claimed so far recorded on the origin, and a re-run continues where it
	// stopped, so the end-of-run commit and push earlier revisions made here
	// are gone - every ticket is already pushed by the time this loop ends.
	var createdIDs []string
	created := map[int]bool{}
	for {
		id, pos, done, err := claimOneChartEntry(st, cfg.TicketFormat, chart)
		if err != nil {
			return renderErr(stdout, graduateFailure(chart, createdIDs, err))
		}
		if done {
			break
		}
		createdIDs = append(createdIDs, id)
		created[pos] = true
	}

	// An entry that already had an id is never changed by this run; compare
	// its resolved blockers (re-read fresh, now that every entry has an id)
	// against that ticket's own ticket.yaml (the source of truth after
	// graduation) and advise when they differ, rather than aborting: a
	// ticket.yaml that ReadTicketDeps cannot read - whether it fails to
	// parse or the read itself fails (permission denied, an EISDIR, and so
	// on) - is already `jig validate`'s own reported issue
	// (validateTicketDeps) - so it gets the same "could not be read"
	// advisory instead of a VALIDATION_ERROR.
	entries, err = st.ReadChart(chart)
	if err != nil {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("failed to read chart %q: %v", chart, err),
			Code: "VALIDATION_ERROR",
			Help: []string{
				fmt.Sprintf("Check that charts/%s/tickets.yaml exists and is valid YAML", chart),
			},
		})
	}
	refs, err := resolveChartEntryRefs(st, chart, entries)
	if err != nil {
		return renderErr(stdout, err)
	}
	var advisories []string
	for i, e := range entries {
		if created[i] {
			continue
		}
		resolved := toBlockedBy(refs[i])
		existing, err := st.ReadTicketDeps(e.ID)
		if err != nil {
			// A refusal that carries its own next steps (an unknown key:
			// upgrade jig or fix the key; a newer schema: upgrade jig) gives
			// them, as `jig validate` does: to edit a newer jig's record by
			// hand would be the wrong step. A read failure with none of its
			// own (permission denied, a directory in the file's place) can
			// only be pointed at the file.
			var ae *axi.Error
			if errors.As(err, &ae) && len(ae.Help) > 0 {
				advisories = append(advisories, fmt.Sprintf(
					"entry %d (%q): %s/ticket.yaml could not be read: %v", i+1, e.Title, e.ID, err,
				))
				advisories = append(advisories, ae.Help...)
			} else {
				advisories = append(advisories, fmt.Sprintf(
					"entry %d (%q): %s/ticket.yaml could not be read: %v; edit %s/ticket.yaml to fix it",
					i+1, e.Title, e.ID, err, e.ID,
				))
			}
			continue
		}
		if !blockersEqual(resolved, existing) {
			advisories = append(advisories, fmt.Sprintf(
				"entry %d (%q): %s/ticket.yaml no longer matches this chart entry's blockers; edit %s/ticket.yaml to match",
				i+1, e.Title, e.ID, e.ID,
			))
		}
	}

	// One row per entry: its position, id, title, and whether this run
	// created it or it already had an id.
	rows := make([][]string, len(entries))
	for i, e := range entries {
		status := "existing"
		if created[i] {
			status = "created"
		}
		rows[i] = []string{fmt.Sprintf("%d", i+1), e.ID, e.Title, status}
	}

	if len(createdIDs) == 0 {
		// A re-run over a fully graduated chart only reports the chart's
		// current state: this branch itself creates, commits and pushes
		// nothing. Store.Sync, called above before the chart was even read,
		// may already have committed (but never pushed) any state a store
		// with a remote found dirty on entry - that is Sync's own recovery
		// for a failed Push that left the store uncommitted, recovering it
		// is deferred to its own ticket, and it runs on every command, not
		// just this branch. What this branch must not do is add a second,
		// graduate-specific sweep on top of that.
		blocks := []string{
			axi.KV("graduate", [][2]string{{"chart", chart}, {"status", "fully graduated"}}),
			axi.Table("entries", []string{"position", "id", "title", "status"}, rows),
		}
		if len(advisories) > 0 {
			blocks = append(blocks, axi.Help(advisories...))
		} else {
			blocks = append(blocks, axi.Help("All entries in this chart already have ids"))
		}
		axi.Render(stdout, blocks...)
		return 0
	}

	helpLines := append([]string{}, advisories...)
	helpLines = append(helpLines, "Write each ticket's brief.md and slices.yaml (the intake skill drafts both), then run `jig validate <id>`")
	blocks := []string{
		axi.KV("graduate", [][2]string{{"chart", chart}, {"created", strconv.Itoa(len(createdIDs))}}),
		axi.Table("entries", []string{"position", "id", "title", "status"}, rows),
		axi.Help(helpLines...),
	}
	axi.Render(stdout, blocks...)
	return 0
}

// errChartFullyGraduated is claimOneChartEntry's internal sentinel: a fresh
// read of the chart, inside store.Claim's own write (so it is current even
// after a rejected claim pulled in another clone's work), found no entry
// without an id. It never reaches the operator; claimOneChartEntry turns it
// into done=true instead.
var errChartFullyGraduated = errors.New("graduate: chart fully graduated")

// claimOneChartEntry claims exactly one ticket for chart through
// store.Claim. Its write re-reads the chart fresh on every attempt -
// including after a rejected claim pulls in whatever another clone pushed -
// so it always targets the first entry that still has no id, resolving that
// entry's blocked_by refs against the ids already on disk: by construction
// (resolveChartEntryRefs refuses a ref from a not-yet-created entry to a
// later one, "it must point at an earlier one") every entry before the one
// claimOneChartEntry is about to mint already has an id, so none of its refs
// ever comes back pending. done reports whether the chart had nothing left
// to claim; id and pos (the entry's 0-based position) are only meaningful
// when done is false and err is nil.
func claimOneChartEntry(st *store.Store, format, chart string) (id string, pos int, done bool, err error) {
	pos = -1
	write := func() (string, []string, error) {
		entries, rerr := st.ReadChart(chart)
		if rerr != nil {
			return "", nil, rerr
		}
		if rerr := validateChartEntries(st, entries, chart); rerr != nil {
			return "", nil, rerr
		}
		refs, rerr := resolveChartEntryRefs(st, chart, entries)
		if rerr != nil {
			return "", nil, rerr
		}
		i := firstMissingID(entries)
		if i < 0 {
			return "", nil, errChartFullyGraduated
		}
		rec := store.Ticket{Title: entries[i].Title, Body: entries[i].Body, BlockedBy: toBlockedBy(refs[i])}
		newID, merr := st.Mint(format, rec)
		if merr != nil {
			return "", nil, merr
		}
		entries[i].ID = newID
		if werr := st.WriteChart(chart, entries); werr != nil {
			return "", nil, &axi.Error{
				Msg:  fmt.Sprintf("ticket %s was created for entry %d but failed to write chart %q: %v", newID, i+1, chart, werr),
				Code: "VALIDATION_ERROR",
				Help: []string{
					fmt.Sprintf("Put `id: %s` on entry %d of charts/%s/tickets.yaml (or delete that ticket folder), then re-run", newID, i+1, chart),
				},
			}
		}
		pos = i
		return newID, []string{newID, "charts/" + chart + "/tickets.yaml"}, nil
	}
	msgFn := func(claimedID string) string { return fmt.Sprintf("chart %s: graduate %s", chart, claimedID) }

	id, err = st.Claim(write, msgFn)
	if errors.Is(err, errChartFullyGraduated) {
		return "", -1, true, nil
	}
	if err != nil {
		return "", -1, false, err
	}
	return id, pos, false, nil
}

// firstMissingID returns the 0-based position of the first entry with no
// id, or -1 when every entry already has one.
func firstMissingID(entries []store.ChartEntry) int {
	for i, e := range entries {
		if e.ID == "" {
			return i
		}
	}
	return -1
}

// toBlockedBy converts refs - every one of them knownID, by
// claimOneChartEntry's own invariant (see its doc comment) - into the
// store.TicketBlockedBy list a ticket.yaml record holds.
func toBlockedBy(refs []entryRef) []store.TicketBlockedBy {
	if len(refs) == 0 {
		return nil
	}
	out := make([]store.TicketBlockedBy, len(refs))
	for i, r := range refs {
		out[i] = store.TicketBlockedBy{Ticket: r.knownID, Kind: r.kind}
	}
	return out
}

// graduateFailure turns a claimOneChartEntry error into one the operator can
// recover from, naming every ticket this run already claimed on the origin
// so far (createdIDs): each was committed, and - on a store with an origin -
// pushed, by its own store.Claim call, so nothing about it is still pending
// the way an orphaned mint once was; a re-run simply continues with the
// entries that still have no id. An error that already carries its own Help
// (an id jig cannot use, a chart write failure, store.Claim's own
// ID_NOT_CLAIMED) keeps its code and Help, gaining only this context; any
// other failure is raw and would otherwise reach renderErr as a bare
// "code: ERROR".
func graduateFailure(chart string, createdIDs []string, err error) error {
	msg := fmt.Sprintf("chart %q: claiming a ticket for the next entry failed: %v", chart, err)
	if len(createdIDs) > 0 {
		msg = fmt.Sprintf("chart %q: %s already claimed on the origin; claiming the next ticket failed: %v", chart, strings.Join(createdIDs, ", "), err)
	}

	var ae *axi.Error
	if errors.As(err, &ae) {
		// A copy: the caller's error is not this function's to change.
		withMsg := *ae
		withMsg.Msg = msg
		return &withMsg
	}

	return &axi.Error{
		Msg:  msg,
		Code: "VALIDATION_ERROR",
		Help: []string{fmt.Sprintf("Re-run `jig graduate %s`; every ticket already claimed stays recorded on the origin and is not re-created", chart)},
	}
}

// validateChartName checks that name is a valid chart name.
func validateChartName(name string) error {
	if name == "" {
		return &axi.Error{
			Msg:  "chart name cannot be empty",
			Code: "VALIDATION_ERROR",
		}
	}
	if name == "." || name == ".." {
		return &axi.Error{
			Msg:  fmt.Sprintf("chart name cannot be %q", name),
			Code: "VALIDATION_ERROR",
		}
	}
	for _, r := range name {
		if r == '/' || r == '\\' {
			return &axi.Error{
				Msg:  "chart name cannot contain path separators",
				Code: "VALIDATION_ERROR",
			}
		}
	}
	return nil
}

// validateChartEntries checks all entries are valid before any are created.
func validateChartEntries(st *store.Store, entries []store.ChartEntry, chart string) error {
	// Check each entry has a title
	for i, e := range entries {
		if e.Title == "" {
			return &axi.Error{
				Msg:  fmt.Sprintf("entry %d in chart %q has no title", i+1, chart),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Add a title to entry %d in charts/%s/tickets.yaml", i+1, chart)},
			}
		}
	}

	// Check no duplicate IDs
	seen := make(map[string]int)
	for i, e := range entries {
		if e.ID != "" {
			if prev, exists := seen[e.ID]; exists {
				return &axi.Error{
					Msg:  fmt.Sprintf("entry %d and entry %d both have id %q", prev+1, i+1, e.ID),
					Code: "VALIDATION_ERROR",
					Help: []string{fmt.Sprintf("Give each entry a unique id in charts/%s/tickets.yaml", chart)},
				}
			}
			seen[e.ID] = i

			// Check the store has a folder for this ID
			if err := requireTicket(st, e.ID); err != nil {
				return err
			}
		}
	}

	return nil
}

// entryRef is one already-validated blocked_by ref for a chart entry,
// resolved as far as possible before any ticket in the chart is created.
// knownID is set when the target ticket's id is already known (an existing
// ticket, or an entry - in this chart or another - that already has an id);
// otherwise pending names the 1-based position of the entry in this chart's
// own tickets.yaml whose id is not yet known.
type entryRef struct {
	knownID string
	pending int
	kind    string
}

// chartRefRE matches a blocked_by ref that names an entry by position:
// "#k" (this chart) or "<chart>#k" (another chart, or this one by name).
var chartRefRE = regexp.MustCompile(`^([^#]*)#(\d+)$`)

// parseRef splits ref into a chart name (empty for a bare "#k") and a
// 1-based position, reporting ok=false when ref names a ticket id instead.
func parseRef(ref string) (chartName string, pos int, ok bool) {
	m := chartRefRE.FindStringSubmatch(ref)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// sameChartFile reports whether refChart names chart: an exact match always,
// or - when charts/<refChart>/tickets.yaml and charts/<chart>/tickets.yaml
// resolve to the same file on this filesystem - a differently-spelled alias
// of it. A chart has no identity beyond that file (nothing else in this
// package tracks one), so a spelling that opens the same file still names
// this chart, not some other one with a similar name. Chart identity follows
// the mount, not the OS: this is correct on a case-sensitive volume (where a
// differently-cased ref opens a different file, or none) and on a
// case-insensitive one (where it opens this same file) alike. When the ref's
// file does not exist, this reports false and lets the caller fall through
// to the cross-chart branch, whose own error names the missing tickets.yaml.
func sameChartFile(st *store.Store, refChart, chart string) bool {
	if refChart == chart {
		return true
	}
	refInfo, err := os.Stat(st.ChartFile(refChart))
	if err != nil {
		return false
	}
	chartInfo, err := os.Stat(st.ChartFile(chart))
	if err != nil {
		return false
	}
	return os.SameFile(refInfo, chartInfo)
}

// normalizeKind validates kind and spells out its default: absent means
// "merged".
func normalizeKind(kind string) (string, error) {
	switch kind {
	case "":
		return "merged", nil
	case "merged", "stacked":
		return kind, nil
	default:
		return "", fmt.Errorf("has kind %q; must be \"merged\" or \"stacked\"", kind)
	}
}

// resolveChartEntryRefs checks every blocked_by ref in entries and resolves
// each to an entryRef, in entry order. It refuses the whole run - returning
// an error naming the failing entry and ref - on the first ref that:
//   - has a kind other than absent, "merged" or "stacked";
//   - is a "#k" (or "<chart>#k" naming this chart) out of range, or naming
//     the entry itself;
//   - is a "#k" (or self-named "<chart>#k") naming another entry being
//     created, from an entry also being created, at a later position;
//   - is a "<chart>#k" naming a chart with no tickets.yaml, an entry out of
//     that chart's range, or an entry with no id yet;
//   - is a ticket id the store has no folder for;
//   - repeats a ticket already listed by an earlier ref in the same entry.
func resolveChartEntryRefs(st *store.Store, chart string, entries []store.ChartEntry) ([][]entryRef, error) {
	out := make([][]entryRef, len(entries))
	otherCharts := map[string][]store.ChartEntry{}

	for i := range entries {
		pos := i + 1
		seen := map[string]bool{}
		for _, r := range entries[i].BlockedBy {
			kind, err := normalizeKind(r.Kind)
			if err != nil {
				return nil, refErr(chart, entries, i, r.Ref, err.Error())
			}

			var er entryRef
			refChart, k, isIndexRef := parseRef(r.Ref)
			switch {
			case !isIndexRef:
				if err := requireTicket(st, r.Ref); err != nil {
					return nil, refErr(chart, entries, i, r.Ref, err.Error())
				}
				er = entryRef{knownID: r.Ref, kind: kind}

			case refChart == "" || sameChartFile(st, refChart, chart):
				if k < 1 || k > len(entries) {
					return nil, refErr(chart, entries, i, r.Ref, "is out of range")
				}
				if k == pos {
					return nil, refErr(chart, entries, i, r.Ref, "names its own entry")
				}
				target := entries[k-1]
				if target.ID != "" {
					er = entryRef{knownID: target.ID, kind: kind}
				} else {
					if entries[i].ID == "" && k > pos {
						return nil, refErr(chart, entries, i, r.Ref, "names a later entry that is also being created; it must point at an earlier one")
					}
					er = entryRef{pending: k, kind: kind}
				}

			default:
				if err := validateChartName(refChart); err != nil {
					return nil, refErr(chart, entries, i, r.Ref, fmt.Sprintf("names an invalid chart: %v", err))
				}
				other, err := readOtherChart(st, otherCharts, refChart)
				if err != nil {
					reason := fmt.Sprintf("names chart %q, which has no charts/%s/tickets.yaml", refChart, refChart)
					if !os.IsNotExist(err) {
						reason = fmt.Sprintf("names chart %q, whose charts/%s/tickets.yaml could not be read: %v", refChart, refChart, err)
					}
					return nil, refErr(chart, entries, i, r.Ref, reason)
				}
				if k < 1 || k > len(other) {
					return nil, refErr(chart, entries, i, r.Ref, fmt.Sprintf("is out of range in chart %q", refChart))
				}
				target := other[k-1]
				if target.ID == "" {
					return nil, refErr(chart, entries, i, r.Ref, fmt.Sprintf("names entry %d of chart %q, which has no id yet; graduate %q first", k, refChart, refChart))
				}
				er = entryRef{knownID: target.ID, kind: kind}
			}

			key := er.knownID
			if key == "" {
				key = fmt.Sprintf("#%d", er.pending)
			}
			if seen[key] {
				return nil, refErr(chart, entries, i, r.Ref, "is already listed in this entry's blocked_by")
			}
			seen[key] = true
			out[i] = append(out[i], er)
		}
	}
	return out, nil
}

// readOtherChart reads charts/<name>/tickets.yaml, caching the result in
// cache so a chart referenced by several refs is only read from disk once.
func readOtherChart(st *store.Store, cache map[string][]store.ChartEntry, name string) ([]store.ChartEntry, error) {
	if es, ok := cache[name]; ok {
		return es, nil
	}
	es, err := st.ReadChart(name)
	if err != nil {
		return nil, err
	}
	cache[name] = es
	return es, nil
}

// refErr reports a blocked_by ref failure, naming the failing entry by
// position and title, and the ref that failed.
func refErr(chart string, entries []store.ChartEntry, i int, ref, reason string) error {
	pos := i + 1
	return &axi.Error{
		Msg:  fmt.Sprintf("chart %q entry %d (%q): blocked_by ref %q %s", chart, pos, entries[i].Title, ref, reason),
		Code: "VALIDATION_ERROR",
		Help: []string{fmt.Sprintf("Fix blocked_by ref %q on entry %d (%q) in charts/%s/tickets.yaml", ref, pos, entries[i].Title, chart)},
	}
}

// blockersEqual reports whether a and b name the same (ticket, kind) pairs,
// regardless of order.
func blockersEqual(a, b []store.TicketBlockedBy) bool {
	if len(a) != len(b) {
		return false
	}
	ak, bk := blockerKeys(a), blockerKeys(b)
	sort.Strings(ak)
	sort.Strings(bk)
	for i := range ak {
		if ak[i] != bk[i] {
			return false
		}
	}
	return true
}

// blockerKeys renders each blocker as a "<ticket>|<kind>" string for
// order-insensitive comparison.
func blockerKeys(deps []store.TicketBlockedBy) []string {
	out := make([]string, len(deps))
	for i, d := range deps {
		out[i] = d.Ticket + "|" + d.Kind
	}
	return out
}
