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
	"github.com/develdeco/jig/internal/tracker"
)

// cmdGraduate implements `jig graduate <chart> [--store <path>] [--project <name>]`.
func cmdGraduate(args []string, stdout io.Writer) int {
	chart, rest, err := requirePositional(args, "chart name")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("graduate")
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

	st, cfg, _, _, err := resolveStoreForProject(*projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	// Sync the store first
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}

	// Read the chart
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

	// Validate all entries before creating anything
	if err := validateChartEntries(st, entries, chart); err != nil {
		return renderErr(stdout, err)
	}

	// Resolve and validate every blocked_by ref before creating anything: any
	// failure refuses the whole run with nothing created.
	refs, err := resolveChartEntryRefs(st, chart, entries)
	if err != nil {
		return renderErr(stdout, err)
	}

	// Find entries without IDs
	var toCreate []int
	for i, e := range entries {
		if e.ID == "" {
			toCreate = append(toCreate, i)
		}
	}
	created := make(map[int]bool, len(toCreate))
	for _, i := range toCreate {
		created[i] = true
	}

	var createdIDs []string
	if len(toCreate) > 0 {
		// Map a file position (0-based) being created to its position in the
		// draft list (0-based), so a same-chart "#k" ref that points at another
		// entry being created can be rewritten in the draft list's own numbering.
		filePosToDraftIdx := make(map[int]int, len(toCreate))
		for di, entryIdx := range toCreate {
			filePosToDraftIdx[entryIdx] = di
		}

		adapter, err := tracker.New(cfg, st)
		if err != nil {
			return renderErr(stdout, err)
		}

		drafts := make([]tracker.Draft, len(toCreate))
		for di, entryIdx := range toCreate {
			e := entries[entryIdx]
			var blockers []tracker.Blocker
			for _, r := range refs[entryIdx] {
				if r.knownID != "" {
					blockers = append(blockers, tracker.Blocker{Ref: r.knownID, Kind: r.kind})
					continue
				}
				targetDraftIdx := filePosToDraftIdx[r.pending-1]
				blockers = append(blockers, tracker.Blocker{Ref: fmt.Sprintf("#%d", targetDraftIdx+1), Kind: r.kind})
			}
			drafts[di] = tracker.Draft{Title: e.Title, Body: e.Body, BlockedBy: blockers}
		}

		// Write the newly minted id into its chart entry as soon as that
		// ticket exists, before the next one is minted: a failure partway
		// through then leaves every already-created ticket recorded in the
		// file, and a re-run continues where it stopped instead of minting
		// duplicates. recorded[di] is set only once that write-back lands, so
		// graduateFailure can tell a ticket tracker.Graduate minted but this
		// call never got to record (recorded[di] still false) from one that
		// is actually safe to re-run over.
		recorded := make([]bool, len(toCreate))
		onMinted := func(di int, id string) error {
			entryIdx := toCreate[di]
			entries[entryIdx].ID = id
			createdIDs = append(createdIDs, id)
			if err := st.WriteChart(chart, entries); err != nil {
				return &axi.Error{
					Msg:  fmt.Sprintf("ticket %s was created for entry %d but failed to write chart %q: %v", id, entryIdx+1, chart, err),
					Code: "VALIDATION_ERROR",
					Help: []string{
						fmt.Sprintf("Put `id: %s` on entry %d of charts/%s/tickets.yaml (or delete that ticket folder), then re-run", id, entryIdx+1, chart),
					},
				}
			}
			recorded[di] = true
			return nil
		}

		g := tracker.Graduation{Chart: chart, Tickets: drafts}
		if ids, err := tracker.Graduate(adapter, st, g, onMinted); err != nil {
			return renderErr(stdout, graduateFailure(st, adapter.Name(), chart, toCreate, ids, recorded, err))
		}
	}

	// An entry that already had an id is never changed by this run; compare
	// its resolved blockers against that ticket's own ticket.yaml (the source
	// of truth after graduation) and advise when they differ. This runs
	// after every new ticket was minted and recorded in the chart but before
	// Push, so a problem here must stay advisory (naming the file, not
	// aborting): a hard return at this point would leave those new tickets
	// created and recorded on disk but never committed, with the operator
	// told about neither. A ticket.yaml that ReadTicketDeps cannot read -
	// whether it fails to parse or the read itself fails (permission denied,
	// an EISDIR, and so on) - is exactly such a problem: it is already `jig
	// validate`'s own reported issue (validateTicketDeps) - so it gets the
	// same "could not be read" advisory instead of a VALIDATION_ERROR.
	finalIDs := make([]string, len(entries))
	for i, e := range entries {
		finalIDs[i] = e.ID
	}
	var advisories []string
	for i, e := range entries {
		if created[i] {
			continue
		}
		resolved := finalizeRefs(refs[i], finalIDs)
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

	if len(toCreate) == 0 {
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

	// Commit the store
	commitMsg := fmt.Sprintf("chart %s: graduate %s", chart, strings.Join(createdIDs, ", "))
	if err := st.Push(commitMsg); err != nil {
		return renderErr(stdout, pushFailure(st.Root, chart, createdIDs, commitMsg, err))
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

// graduateFailure turns a tracker.Graduate error into one the operator can
// recover from. An error that already carries its own Help keeps it and
// its code, and gains the entries this run already created and recorded:
// onMinted's own failure (a WriteChart error) names the one entry it
// orphaned, and an id jig cannot use (tracker.CheckMinted) names the tracker
// ticket to close, and neither says what came before it. Any other failure -
// Mint, the store-folder MkdirAll, or the ticket.yaml write, none of which
// onMinted ever saw - is raw and would otherwise reach renderErr as a bare
// "code: ERROR".
//
// A minted id that collides with a ticket.yaml already in the store
// (store.ErrTicketRecordExists) is the one such failure whose folder is not
// this run's to delete: it holds another ticket's record. It gets the
// message and help jig ticket new gives the same collision
// (mintedIDCollision), not the orphan help below.
//
// ids and recorded are tracker.Graduate's return value and this call's own
// write-back bookkeeping, both indexed like toCreate: ids[di] is set for
// every entry that reached a successful Mint, but recorded[di] is set only
// once onMinted's write-back for it actually landed in the chart file.
// tracker.Graduate calls onMinted for entry di before minting entry di+1, so
// at most one entry - the last one with a non-empty id - can have a ticket
// minted but not recorded; that one is not safe to re-run over; every other
// non-empty id is.
func graduateFailure(st *store.Store, trackerName, chart string, toCreate []int, ids []string, recorded []bool, err error) error {
	var done []string
	orphanDi := -1
	for di, id := range ids {
		if id == "" {
			continue
		}
		if recorded[di] {
			done = append(done, fmt.Sprintf("entry %d (%s)", toCreate[di]+1, id))
		} else {
			orphanDi = di
		}
	}
	// The entries this run already created and recorded, for a failure that
	// strikes after some of them: a re-run continues after them.
	alreadyCreated := func(msg string) string {
		if len(done) == 0 {
			return msg
		}
		return fmt.Sprintf("%s (already created: %s)", msg, strings.Join(done, ", "))
	}

	var ae *axi.Error
	if errors.As(err, &ae) {
		// A copy: the caller's error is not this function's to change.
		withDone := *ae
		withDone.Msg = alreadyCreated(ae.Msg)
		return &withDone
	}

	if orphanDi >= 0 && errors.Is(err, store.ErrTicketRecordExists) {
		id := ids[orphanDi]
		collision := mintedIDCollision(trackerName, id, st.TicketFilePath(id), fmt.Sprintf(
			"entry %d of charts/%s/tickets.yaml has no id, so re-running `jig graduate %s` before this is settled mints a second ticket for it",
			toCreate[orphanDi]+1, chart, chart,
		))
		collision.Msg = alreadyCreated(collision.Msg)
		return collision
	}

	msg := alreadyCreated(err.Error())

	if orphanDi >= 0 {
		id := ids[orphanDi]
		entryIdx := toCreate[orphanDi]
		msg = fmt.Sprintf("%s (ticket %s was created for entry %d but not yet recorded in charts/%s/tickets.yaml)", msg, id, entryIdx+1, chart)
		return &axi.Error{
			Msg:  msg,
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Put `id: %s` on entry %d of charts/%s/tickets.yaml (or delete that ticket folder), then re-run", id, entryIdx+1, chart)},
		}
	}

	return &axi.Error{
		Msg:  msg,
		Code: "VALIDATION_ERROR",
		Help: []string{fmt.Sprintf("Re-run `jig graduate %s`; it creates only the entries in charts/%s/tickets.yaml that still have no id", chart, chart)},
	}
}

// pushFailure wraps a Store.Push failure - one that refused outright (a
// mid-rebase or mid-merge, internal/store/store.go:204) or one that could
// not even be retried with pull --rebase - with what Store itself has no
// way to know: every id in createdIDs is already written into
// charts/<chart>/tickets.yaml and its ticket folder already exists on disk,
// so nothing from this run is lost - only committing it is still pending.
// The only caller, cmdGraduate's Store.Push call (reached only when at
// least one entry was created), reaches this after at least one mint has
// already appended to createdIDs, so it is never empty here. err's own
// message and Help (Push's
// STORE_CONFLICT already names the store's exact git state and how to
// resolve it there) are kept, not discarded, since only Store can describe
// that state; this only adds the graduate-specific context plus the commit
// the operator can make by hand with commitMsg, the message Push itself
// would have used.
func pushFailure(storeRoot, chart string, createdIDs []string, commitMsg string, err error) error {
	msg := fmt.Sprintf(
		"chart %q: every ticket from this run already exists with its id recorded in charts/%s/tickets.yaml (%s), but committing that failed: %v",
		chart, chart, strings.Join(createdIDs, ", "), err,
	)

	var ae *axi.Error
	code := "VALIDATION_ERROR"
	var help []string
	if errors.As(err, &ae) {
		code = ae.Code
		help = append(help, ae.Help...)
	}
	help = append(help, fmt.Sprintf(
		"Nothing is lost: commit it yourself with `git -C %s add -A && git -C %s commit -m %q` (a new ticket folder is untracked, so `-a` alone would miss it), or re-run `jig graduate %s` once the store's git state above is resolved - it will only need to commit, not re-create anything",
		storeRoot, storeRoot, commitMsg, chart,
	))

	return &axi.Error{Msg: msg, Code: code, Help: help}
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

// finalizeRefs resolves every entryRef in refs to a concrete ticket id,
// using finalIDs (indexed by 0-based chart position) for any ref still
// pending at validation time - valid once every entry that will be created
// this run has been created.
func finalizeRefs(refs []entryRef, finalIDs []string) []store.TicketBlockedBy {
	if len(refs) == 0 {
		return nil
	}
	out := make([]store.TicketBlockedBy, len(refs))
	for i, r := range refs {
		id := r.knownID
		if id == "" {
			id = finalIDs[r.pending-1]
		}
		out[i] = store.TicketBlockedBy{Ticket: id, Kind: r.kind}
	}
	return out
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
