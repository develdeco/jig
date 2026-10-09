package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/pool"
)

// mintLockTimeout bounds Mint's hold on the store's mint lock, matching
// every other store writer's lock timeout (internal/store/slice_state.go,
// internal/store/question.go, internal/journal/journal.go).
const mintLockTimeout = 30 * time.Second

// mintLockPath is the sidecar lock Mint holds across its scan of the store
// root and the ticket folder and record it then creates, so two mints
// against the same clone never compute the same next id. It names no real
// file of its own, only the ".lock" sidecar Lock makes from it.
func (s *Store) mintLockPath() string {
	return filepath.Join(s.Root, ".jig-mint")
}

// ticketFormatPattern builds the regexp that recognizes a store-root folder
// minted by format, a project.yaml ticket_format such as "JIG-{n}"
// ("^JIG-(\d+)$").
func ticketFormatPattern(format string) (*regexp.Regexp, error) {
	parts := strings.SplitN(format, "{n}", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("store: ticket_format %q has no {n} placeholder", format)
	}
	pattern := "^" + regexp.QuoteMeta(parts[0]) + `(\d+)` + regexp.QuoteMeta(parts[1]) + "$"
	return regexp.Compile(pattern)
}

// nextID scans the store root for folders matching format and returns one
// past the highest number among them, formatted by format: the next id is
// one past the highest number among the store-root folders the format
// matches, as the local tracker counted before minting moved here. A
// non-directory entry whose name happens to match is skipped, so a plain
// file left in the way of a ticket folder never shifts the count.
func (s *Store) nextID(format string) (string, error) {
	re, err := ticketFormatPattern(format)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return "", fmt.Errorf("store: mint: read store root: %w", err)
	}
	maxN := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > maxN {
			maxN = n
		}
	}
	return strings.ReplaceAll(format, "{n}", strconv.Itoa(maxN+1)), nil
}

// TicketIDs scans the store root for folders format mints (the same scan
// nextID runs) and returns their ids in ascending numeric order: the order
// the GitHub mirror syncs tickets in. Unlike nextID it is not called under
// the mint lock - a caller reading the store to decide what to sync does not
// race a concurrent Mint the way computing the next id would.
func (s *Store) TicketIDs(format string) ([]string, error) {
	re, err := ticketFormatPattern(format)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, fmt.Errorf("store: list tickets: read store root: %w", err)
	}
	type numbered struct {
		id string
		n  int
	}
	var ids []numbered
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := re.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		ids = append(ids, numbered{id: e.Name(), n: n})
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].n < ids[j].n })
	out := make([]string, len(ids))
	for i, nb := range ids {
		out[i] = nb.id
	}
	return out, nil
}

// Mint computes the next id per format (project.yaml's ticket_format) and
// creates that ticket's folder and its first ticket.yaml, holding rec - the
// one way jig ticket new and jig graduate mint a ticket, whatever
// project.yaml says about trackers. The scan and the record's write both run
// under the store's mint lock, so two mints against the same clone never
// compute the same id.
//
// An id jig cannot use (pool.CheckTicket: a reserved lease suffix, or
// something that is not a single directory name) is refused before anything
// is written under it. Any other failure creating the record - most likely
// something already sitting where the ticket's folder must go - is also
// refused before anything else is written; either way Mint returns an empty
// id, since nothing was claimed under it.
func (s *Store) Mint(format string, rec Ticket) (string, error) {
	release, _, err := Lock(s.mintLockPath(), mintLockTimeout)
	if err != nil {
		return "", err
	}
	defer release()

	id, err := s.nextID(format)
	if err != nil {
		return "", err
	}
	if err := pool.CheckTicket(id); err != nil {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("ticket_format %q mints %s, which jig cannot use: %v", format, id, err),
			Code: "VALIDATION_ERROR",
			Help: []string{"Change ticket_format in project.yaml, then mint again"},
		}
	}
	if err := s.CreateTicketRecord(id, rec); err != nil {
		return "", fmt.Errorf("store: mint %s: %w", id, err)
	}
	return id, nil
}
