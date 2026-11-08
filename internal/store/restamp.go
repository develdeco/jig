package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// RestampSlices replaces the from_brief of the slices stamps names, under the
// same lock as AppendSlices, in one read-modify-write: stamps maps a slice id
// to the from_brief it now carries. Nothing else in a slice row changes, and
// the file is written once, so a stamp for a slice that is not there refuses
// the whole call and leaves the file as it was.
func (s *Store) RestampSlices(ticket string, stamps map[string][]string) error {
	path := s.slicesFile(ticket)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	data, err := ReadFile(path)
	if err != nil {
		return err
	}
	var sf SliceFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return err
	}
	found := map[string]bool{}
	for i := range sf.Slices {
		if stamp, ok := stamps[sf.Slices[i].ID]; ok {
			sf.Slices[i].FromBrief = slices.Clone(stamp)
			found[sf.Slices[i].ID] = true
		}
	}
	for id := range stamps {
		if !found[id] {
			return &axi.Error{
				Msg:  fmt.Sprintf("slice %q is not in %s, so it cannot be restamped", id, path),
				Code: "VALIDATION_ERROR",
			}
		}
	}
	out, err := yaml.Marshal(sf)
	if err != nil {
		return err
	}
	return AtomicWrite(path, out)
}

// briefHistoryLimit bounds how many revisions of brief.md BriefHeadings walks.
const briefHistoryLimit = 200

// BriefHeadings finds, for each hash in hashes, the heading of the section it
// was the hash of: it walks the store's committed revisions of the ticket's
// brief.md, newest first, and takes the first revision with a section that
// hashes to it. A hash no committed revision holds (one that was never
// committed, or is older than the bound) is absent from the result, as is
// every hash when the store has no history to read.
func (s *Store) BriefHeadings(ticket string, hashes []string) map[string]string {
	want := map[string]bool{}
	for _, h := range hashes {
		want[h] = true
	}
	found := map[string]string{}
	rel := filepath.ToSlash(filepath.Join(s.TicketRelDir(ticket), "brief.md"))
	log, err := gitx.PathLog(s.Root, "HEAD", briefHistoryLimit, rel)
	if err != nil {
		return found
	}
	for _, entry := range log {
		if len(found) == len(want) {
			break
		}
		blob, ok, err := gitx.BlobAt(s.Root, entry.SHA, rel)
		if err != nil || !ok {
			continue
		}
		for _, sec := range BriefSections(blob) {
			if _, done := found[sec.Hash]; want[sec.Hash] && !done {
				found[sec.Hash] = sec.Heading
			}
		}
	}
	return found
}
