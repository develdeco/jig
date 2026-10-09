package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// ticketIDPattern matches a tickets/ folder name that is an id: a key (1
// to 10 uppercase ASCII letters and digits, starting with a letter - the
// loose shape of any ticket folder, under any key, declared or not, since a
// key removed from project.yaml only stops new mints under it) a "-", and a
// number counted from 1 with no leading zero.
var ticketIDPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]{0,9})-([1-9][0-9]*)$`)

// ticketIDEntry is one id-shaped store-root folder, its key and number
// pulled out of its name.
type ticketIDEntry struct {
	id  string
	key string
	n   int
}

// scanTicketIDs reads root's tickets/ dir and returns every id-shaped
// directory among its entries, in no particular order. A non-directory entry
// whose name happens to match is skipped, so a plain file left in the way of
// a ticket folder never shifts a key's count. A root with no tickets/ dir
// yet (a fresh store, before anything is minted) reads as no tickets, rather
// than an error.
func scanTicketIDs(root string) ([]ticketIDEntry, error) {
	entries, err := os.ReadDir(filepath.Join(root, ticketsDirName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: list tickets: read %s: %w", ticketsDirName, err)
	}
	var out []ticketIDEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := ticketIDPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		out = append(out, ticketIDEntry{id: e.Name(), key: m[1], n: n})
	}
	return out, nil
}

// TicketIDs lists every id-shaped folder under tickets/, under any key -
// declared in project.yaml or not - sorted by key, then by number: the one
// function minting's per-key counter (nextID), A6's mirror and every other
// listing that shows many tickets read tickets through, and nothing else
// lists them.
func (s *Store) TicketIDs() ([]string, error) {
	recs, err := scanTicketIDs(s.Root)
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].key != recs[j].key {
			return recs[i].key < recs[j].key
		}
		return recs[i].n < recs[j].n
	})
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.id
	}
	return out, nil
}

// nextID scans the store root (the same scan TicketIDs runs) and returns one
// past the highest number among the ids and the aliases already minted under
// key, formatted as "<key>-<n>": the next number under a key is one past the
// highest number among the ids - and the aliases, which carry an alias
// collision no further than any other read of a ticket.yaml - that carry it,
// worked out from the store rather than stored anywhere. An alias counts
// even when it is also claimed by some other ticket's own id or another
// ticket's alias: nextID only cares that the number was once minted under
// key, not who owns it now - Store.ResolveTicket is where a collision itself
// is refused.
func (s *Store) nextID(key string) (string, error) {
	recs, err := scanTicketIDs(s.Root)
	if err != nil {
		return "", err
	}
	maxN := 0
	for _, r := range recs {
		if r.key == key && r.n > maxN {
			maxN = r.n
		}
		// A ticket.yaml that cannot be read or decoded (a hand-broken file,
		// an unsupported schema_version) is this ticket's own problem, which
		// jig validate already reports; Mint must still be able to compute
		// the next number from the folder names alone, so a bad record here
		// only costs its own aliases' contribution to the count, never the
		// mint itself.
		rec, err := s.ReadTicket(r.id)
		if err != nil {
			continue
		}
		for _, alias := range rec.Aliases {
			m := ticketIDPattern.FindStringSubmatch(alias)
			if m == nil || m[1] != key {
				continue
			}
			n, err := strconv.Atoi(m[2])
			if err != nil {
				continue
			}
			if n > maxN {
				maxN = n
			}
		}
	}
	return fmt.Sprintf("%s-%d", key, maxN+1), nil
}

// Mint computes the next id under key (<key>-<n>, one past the highest
// number already minted under it) and creates that ticket's folder and its
// first ticket.yaml, holding rec - the one way jig ticket new and jig
// graduate mint a ticket, whatever project.yaml says about trackers. The
// scan and the record's write both run under the store's mint lock, so two
// mints against the same clone never compute the same id. key is trusted as
// already checked against project.yaml's declared keys (project.Config.
// ResolveKey): an undeclared key is refused there, before Mint is ever
// called.
//
// An id jig cannot use (pool.CheckTicket: a reserved lease suffix, or
// something that is not a single directory name) is refused before anything
// is written under it. Any other failure creating the record - most likely
// something already sitting where the ticket's folder must go - is also
// refused before anything else is written; either way Mint returns an empty
// id, since nothing was claimed under it.
func (s *Store) Mint(key string, rec Ticket) (string, error) {
	release, _, err := Lock(s.mintLockPath(), mintLockTimeout)
	if err != nil {
		return "", err
	}
	defer release()

	id, err := s.nextID(key)
	if err != nil {
		return "", err
	}
	if err := pool.CheckTicket(id); err != nil {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("key %q mints %s, which jig cannot use: %v", key, id, err),
			Code: "VALIDATION_ERROR",
			Help: []string{"Change the key in project.yaml, then mint again"},
		}
	}
	if err := s.CreateTicketRecord(id, rec); err != nil {
		return "", fmt.Errorf("store: mint %s: %w", id, err)
	}
	return id, nil
}
