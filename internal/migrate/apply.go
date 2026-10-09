package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// Apply carries out plan against st, as the dry run only described: moves
// every ticket folder, writes its migrated ticket.yaml, deletes its local
// tracker files, rewrites project.yaml and every affected chart, and
// commits everything once on the current branch. It does not push or sync;
// the caller's own refusals (CheckSchemaVersion, CheckClean,
// CheckLevelWithOrigin, CheckNoLeases) must already have passed.
//
// A failure partway through (after the first ticket folder has moved, and
// before the one commit at the end) leaves the store half-migrated on disk,
// uncommitted. Every such failure is wrapped in one *axi.Error naming the
// recovery: `git reset --hard && git clean -fd` undoes it safely, exactly
// because CheckClean and CheckLevelWithOrigin already passed before Apply
// ran, so the store had nothing uncommitted and was level with origin to
// begin with.
func Apply(st *store.Store, plan Plan) error {
	if err := applyUncommitted(st, plan); err != nil {
		return &axi.Error{
			Msg:  fmt.Sprintf("jig store migrate failed partway through, leaving the store at %s half-migrated: %v", st.Root, err),
			Code: "STORE_MIGRATE_APPLY_FAILED",
			Help: []string{
				fmt.Sprintf("Restore the store to what it was before the migration: run `git reset --hard && git clean -fd` in %s, then retry", st.Root),
			},
		}
	}
	return nil
}

// applyUncommitted is Apply's actual work, returning its failures plain, for
// Apply to wrap once in a single *axi.Error.
func applyUncommitted(st *store.Store, plan Plan) error {
	for _, r := range plan.Renames {
		if err := moveTicket(st, r.OldID, r.NewID); err != nil {
			return err
		}
	}
	for _, r := range plan.Renames {
		if err := rewriteTicketRecord(st, r, plan.RenameOf); err != nil {
			return err
		}
	}
	if err := rewriteCharts(st, plan.RenameOf); err != nil {
		return err
	}
	if err := rewriteProjectYAMLFile(st, plan); err != nil {
		return err
	}
	if _, err := gitx.Run(st.Root, "add", "-A"); err != nil {
		return err
	}
	if _, err := gitx.Run(st.Root, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", "jig: migrate store to schema_version 2"); err != nil {
		return err
	}
	return nil
}

// moveTicket moves oldID's whole ticket folder to newID's, then deletes the
// local tracker files the migration retires (brief.md#The migration step
// 5): tracker/ticket.md, tracker/subtasks.yaml and tracker/comments/,
// keeping tracker/github.yaml.
func moveTicket(st *store.Store, oldID, newID string) error {
	oldDir, newDir := oldTicketDir(st.Root, oldID), st.TicketDir(newID)
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		return fmt.Errorf("migrate: create %s: %w", filepath.Dir(newDir), err)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("migrate: move %s to %s: %w", oldDir, newDir, err)
	}
	trackerDir := filepath.Join(newDir, "tracker")
	for _, name := range []string{"ticket.md", "subtasks.yaml"} {
		if err := os.Remove(filepath.Join(trackerDir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("migrate: delete %s: %w", filepath.Join(trackerDir, name), err)
		}
	}
	if err := os.RemoveAll(filepath.Join(trackerDir, "comments")); err != nil {
		return fmt.Errorf("migrate: delete %s: %w", filepath.Join(trackerDir, "comments"), err)
	}
	return nil
}

// rewriteTicketRecord writes r.NewID's migrated ticket.yaml: its old id as
// an alias, the title and description resolveTitleBody already resolved on
// the v1 store (plan.BuildPlan ran before any folder moved), and its
// blocked_by refs translated through renameOf.
func rewriteTicketRecord(st *store.Store, r Rename, renameOf map[string]string) error {
	rec, err := st.ReadTicket(r.NewID)
	if err != nil {
		return err
	}
	translated := make([]store.TicketBlockedBy, len(rec.BlockedBy))
	for i, b := range rec.BlockedBy {
		translated[i] = b
		if newID, ok := renameOf[b.Ticket]; ok {
			translated[i].Ticket = newID
		}
	}
	return st.SetMigratedTicket(r.NewID, r.OldID, r.Title, r.Body, translated)
}

// rewriteCharts rewrites every chart whose entries' ids or blocked_by refs
// name a renamed ticket.
func rewriteCharts(st *store.Store, renameOf map[string]string) error {
	names, err := st.ChartNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		entries, err := st.ReadChart(name)
		if err != nil {
			return err
		}
		if !chartAffected(entries, renameOf) {
			continue
		}
		for i := range entries {
			if newID, ok := renameOf[entries[i].ID]; ok {
				entries[i].ID = newID
			}
			for j, b := range entries[i].BlockedBy {
				if chartIndexRefRE.MatchString(b.Ref) {
					continue
				}
				if newID, ok := renameOf[b.Ref]; ok {
					entries[i].BlockedBy[j].Ref = newID
				}
			}
		}
		if err := st.WriteChart(name, entries); err != nil {
			return err
		}
	}
	return nil
}

// rewriteProjectYAMLFile reads the store's project.yaml, rewrites it onto
// schema 2 with plan's keys (RewriteProjectYAML), and writes it back.
func rewriteProjectYAMLFile(st *store.Store, plan Plan) error {
	path := filepath.Join(st.Root, "project.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := RewriteProjectYAML(data, plan.Keys)
	if err != nil {
		return &axi.Error{Msg: err.Error(), Code: "VALIDATION_ERROR"}
	}
	return store.AtomicWrite(path, out)
}
