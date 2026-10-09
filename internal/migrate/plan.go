package migrate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// Rename is one ticket's move, old id to new id, with the title the new
// record carries.
type Rename struct {
	OldID string
	NewID string
	Title string
	Body  string
}

// FileOp is one file the migration would move, delete or rewrite, its path
// store-relative and slash-separated.
type FileOp struct {
	Action string // "move", "delete" or "rewrite"
	Path   string // the file's path after the op, slash-separated
	From   string // for "move": the path before the op; "" otherwise
}

// Plan is what `jig store migrate --map <file> --dry-run` reports and
// `jig store migrate --map <file>` carries out: every ticket's rename, in
// new-id order, and every file the migration touches.
type Plan struct {
	Renames []Rename
	Files   []FileOp
	// RenameOf is OldID -> NewID, for translating a blocked_by ref or a
	// chart entry's id.
	RenameOf map[string]string
	// Keys is the new area keys the migrated project.yaml declares (the
	// map's own keys:).
	Keys map[string]string
}

// chartIndexRefRE matches a chart blocked_by ref that names an entry by
// position ("#k" or "<chart>#k", cmd/jig's chartRefRE) rather than a ticket
// id: such a ref is never translated by the migration, since it names no
// ticket id at all.
var chartIndexRefRE = regexp.MustCompile(`^([^#]*)#(\d+)$`)

// BuildPlan validates m against st's current tickets and builds the Plan:
// every ticket present and declared exactly once, every number assigned in
// old-id order per key, and the title each renamed ticket will carry.
func BuildPlan(st *store.Store, m Map) (Plan, error) {
	oldIDs, err := OldTicketIDs(st.Root)
	if err != nil {
		return Plan{}, err
	}
	have := map[string]bool{}
	for _, id := range oldIDs {
		have[id] = true
	}

	var leftOut []string
	for _, id := range oldIDs {
		if _, ok := m.Tickets[id]; !ok {
			leftOut = append(leftOut, id)
		}
	}
	if len(leftOut) > 0 {
		sort.Strings(leftOut)
		return Plan{}, &axi.Error{
			Msg:  fmt.Sprintf("the rename map leaves out %d ticket(s) the store has: %s", len(leftOut), strings.Join(leftOut, ", ")),
			Code: "VALIDATION_ERROR",
			Help: []string{"Add every one of them to the rename map's tickets:"},
		}
	}

	var unknown []string
	for _, id := range sortedKeys(m.Tickets) {
		if !have[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return Plan{}, &axi.Error{
			Msg:  fmt.Sprintf("the rename map names %d ticket(s) the store does not have: %s", len(unknown), strings.Join(unknown, ", ")),
			Code: "VALIDATION_ERROR",
			Help: []string{"Remove them from the rename map's tickets:, or check the ticket id"},
		}
	}

	var undeclared []string
	for _, id := range sortedKeys(m.Tickets) {
		key := m.Tickets[id]
		if _, ok := m.Keys[key]; !ok {
			undeclared = append(undeclared, fmt.Sprintf("%s -> %s", id, key))
		}
	}
	if len(undeclared) > 0 {
		return Plan{}, &axi.Error{
			Msg:  fmt.Sprintf("the rename map assigns %d ticket(s) to a key the map's keys: does not declare: %s", len(undeclared), strings.Join(undeclared, ", ")),
			Code: "VALIDATION_ERROR",
			Help: []string{"Add the key to the rename map's keys:, or fix the ticket's assignment"},
		}
	}

	oldKeys := map[string]bool{}
	for _, id := range oldIDs {
		if key, _, ok := store.ParseTicketID(id); ok {
			oldKeys[key] = true
		}
	}
	var clashing []string
	for _, key := range sortedKeys(m.Keys) {
		if oldKeys[key] {
			clashing = append(clashing, key)
		}
	}
	if len(clashing) > 0 {
		return Plan{}, &axi.Error{
			Msg:  fmt.Sprintf("the rename map's keys: declares %s, which the store's old ids already carry", strings.Join(clashing, ", ")),
			Code: "VALIDATION_ERROR",
			Help: []string{"Every old id stays claimed as an alias: pick a key none of them already carries"},
		}
	}

	renames, err := assignNewIDs(oldIDs, m)
	if err != nil {
		return Plan{}, err
	}

	renameOf := make(map[string]string, len(renames))
	for i := range renames {
		title, body, err := resolveTitleBody(st, renames[i].OldID)
		if err != nil {
			return Plan{}, err
		}
		renames[i].Title = title
		renames[i].Body = body
		renameOf[renames[i].OldID] = renames[i].NewID
	}

	files, err := planFiles(st, renames)
	if err != nil {
		return Plan{}, err
	}

	return Plan{Renames: renames, Files: files, RenameOf: renameOf, Keys: m.Keys}, nil
}

// assignNewIDs numbers every ticket of each key in old-id order, by the old
// id's own number: the lowest old id under a key becomes <key>-1
// (brief.md#The rename map). Keys are assigned in sorted order so the
// result (and so the plan's printed order) is deterministic.
func assignNewIDs(oldIDs []string, m Map) ([]Rename, error) {
	byKey := map[string][]string{}
	for _, id := range oldIDs {
		key := m.Tickets[id]
		byKey[key] = append(byKey[key], id)
	}

	var renames []Rename
	for _, key := range sortedKeys(m.Keys) {
		ids := byKey[key]
		sort.Slice(ids, func(i, j int) bool {
			_, ni, _ := store.ParseTicketID(ids[i])
			_, nj, _ := store.ParseTicketID(ids[j])
			return ni < nj
		})
		for i, old := range ids {
			renames = append(renames, Rename{OldID: old, NewID: fmt.Sprintf("%s-%d", key, i+1)})
		}
	}
	return renames, nil
}

// trackerDeletedPaths are the tracker/ sub-paths (relative to a ticket
// folder, slash-separated) `jig store migrate` deletes outright rather than
// moving: tracker/github.yaml is the one tracker file it keeps
// (brief.md#The migration step 5).
func trackerDeletedPrefixes(relSlash string) bool {
	return relSlash == "tracker/ticket.md" ||
		relSlash == "tracker/subtasks.yaml" ||
		strings.HasPrefix(relSlash, "tracker/comments/")
}

// planFiles walks each renamed ticket's folder on disk and reports what the
// migration would do to every file in it, plus project.yaml and every
// chart's tickets.yaml the renames touch.
func planFiles(st *store.Store, renames []Rename) ([]FileOp, error) {
	var ops []FileOp
	for _, r := range renames {
		ticketOps, err := planTicketFiles(st, r.OldID, r.NewID)
		if err != nil {
			return nil, err
		}
		ops = append(ops, ticketOps...)
	}
	ops = append(ops, FileOp{Action: "rewrite", Path: "project.yaml"})

	names, err := st.ChartNames()
	if err != nil {
		return nil, err
	}
	renameOf := make(map[string]string, len(renames))
	for _, r := range renames {
		renameOf[r.OldID] = r.NewID
	}
	for _, name := range names {
		entries, err := st.ReadChart(name)
		if err != nil {
			return nil, err
		}
		if chartAffected(entries, renameOf) {
			ops = append(ops, FileOp{Action: "rewrite", Path: filepath.ToSlash(filepath.Join("charts", name, "tickets.yaml"))})
		}
	}
	return ops, nil
}

// chartAffected reports whether any entry id or blocked_by ref in entries
// names an old id renameOf is renaming.
func chartAffected(entries []store.ChartEntry, renameOf map[string]string) bool {
	for _, e := range entries {
		if _, ok := renameOf[e.ID]; ok {
			return true
		}
		for _, b := range e.BlockedBy {
			if chartIndexRefRE.MatchString(b.Ref) {
				continue
			}
			if _, ok := renameOf[b.Ref]; ok {
				return true
			}
		}
	}
	return false
}

// planTicketFiles reports every file under oldID's ticket folder: a move to
// newID's folder at the same relative path, except ticket.yaml itself
// (always a rewrite, at the new path, since its title, body, aliases and
// blocked_by all change) and the tracker files the migration deletes
// outright (brief.md#The migration step 5).
func planTicketFiles(st *store.Store, oldID, newID string) ([]FileOp, error) {
	root := oldTicketDir(st.Root, oldID)
	var ops []FileOp
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		switch {
		case relSlash == "ticket.yaml":
			ops = append(ops, FileOp{Action: "rewrite", Path: filepath.ToSlash(filepath.Join(st.TicketRelDir(newID), relSlash))})
		case trackerDeletedPrefixes(relSlash):
			ops = append(ops, FileOp{Action: "delete", Path: filepath.ToSlash(filepath.Join(oldID, relSlash))})
		default:
			ops = append(ops, FileOp{
				Action: "move",
				From:   filepath.ToSlash(filepath.Join(oldID, relSlash)),
				Path:   filepath.ToSlash(filepath.Join(st.TicketRelDir(newID), relSlash)),
			})
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ops, nil
}
