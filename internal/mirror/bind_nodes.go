package mirror

import (
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// nodeOwners maps the node id of every issue the tracker tree holds a record
// for to the ticket, or "chart <name>", that owns it: the issues a blocked-by
// edge of the store's own can name.
func nodeOwners(st *store.Store) (map[string]string, error) {
	recs, err := st.TrackerRecords()
	if err != nil {
		return nil, err
	}
	owners := map[string]string{}
	for _, rec := range recs {
		if rec.Err != nil || rec.Entry.Provider != store.ProviderGitHub {
			continue
		}
		fields, derr := decodeRecord(rec.Fields)
		if derr != nil || fields.NodeID == "" {
			continue
		}
		if rec.Chart != "" {
			owners[fields.NodeID] = "chart " + rec.Chart
		} else if rec.Ticket != "" {
			owners[fields.NodeID] = rec.Ticket
		}
	}
	return owners, nil
}

// CheckItemUnbound refuses TRACKER_ENTRY_TAKEN for an item the tracker tree
// already binds, naming the issue and its chart. A caller runs it before any
// check of its own that could refuse the same bind for a lesser reason.
func CheckItemUnbound(st *store.Store, cfg project.Config, ref ItemRef) error {
	if err := CheckItemRepo(ref, cfg); err != nil {
		return err
	}
	st.ReloadTrackerTree()
	return checkEntryUnbound(st, githubEntry(cfg.GitHub.Repo, ref.Number), ref.Number)
}
