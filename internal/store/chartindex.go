package store

import (
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// chartIndex keeps each chart's tickets.yaml as last decoded, so a command that
// asks which issue lists each of many tickets (a roster) decodes a chart once,
// not once for each ticket. An entry stands only while its file is as it was
// when decoded (its size and modification time), so a write by this command or
// another process is seen at the next ask.
type chartIndex struct {
	mu      sync.Mutex
	by      map[string]chartDecoded
	decodes int

	// pinned says the store is being read whole by one command that writes no
	// chart: the chart names and each chart's listing of tickets are then read
	// once and looked up, without a stat for each question (PinCharts).
	pinned    bool
	names     []string
	namesErr  error
	namesRead bool
	listings  map[string][]Listing
	// namesReads counts how many times the chart directory was listed.
	namesReads int
}

// chartDecoded is a chart's file as decoded, with the stat it was decoded at.
type chartDecoded struct {
	size int64
	mod  time.Time
	cf   ChartFile
	err  error
}

// read is the decoded charts/<name>/tickets.yaml: from the index while the file
// is as it was, else decoded again.
func (c *chartIndex) read(s *Store, name string) (ChartFile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readLocked(s, name)
}

// readLocked is read with c.mu held.
func (c *chartIndex) readLocked(s *Store, name string) (ChartFile, error) {
	path := s.ChartFile(name)
	if d, ok := c.by[name]; ok && c.pinned {
		return d.cf, d.err
	}
	info, statErr := os.Stat(path)
	if statErr == nil {
		if d, ok := c.by[name]; ok && d.size == info.Size() && d.mod.Equal(info.ModTime()) {
			return d.cf, d.err
		}
	}
	data, err := ReadFile(path)
	if err != nil {
		return ChartFile{}, err
	}
	c.decodes++
	var cf ChartFile
	err = yaml.Unmarshal(data, &cf)
	if err != nil {
		cf = ChartFile{}
	}
	if statErr == nil {
		if c.by == nil {
			c.by = map[string]chartDecoded{}
		}
		c.by[name] = chartDecoded{size: info.Size(), mod: info.ModTime(), cf: cf, err: err}
	}
	return cf, err
}

// PinCharts makes the chart index stand for as long as the caller reads the
// store: the chart names are listed once, each chart is decoded once, and which
// issue lists a ticket is a lookup, whatever the files do meanwhile. It is for
// a command that reads many tickets and writes no chart (a roster), so the cost
// of knowing a ticket's issue does not grow with the number of charts. The
// returned function unpins; it is safe to call more than once.
func (s *Store) PinCharts() (unpin func()) {
	c := &s.charts
	c.mu.Lock()
	c.pinned = true
	c.names, c.namesErr, c.namesRead, c.listings = nil, nil, false, nil
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.pinned = false
		c.names, c.namesErr, c.namesRead, c.listings = nil, nil, false, nil
		c.mu.Unlock()
	}
}

// pinnedNames is the chart names a pinned store listed once; ok is false when
// the index is not pinned.
func (c *chartIndex) pinnedNames(s *Store) (names []string, ok bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pinned {
		return nil, false, nil
	}
	c.listNamesLocked(s)
	return append([]string(nil), c.names...), true, c.namesErr
}

// listNamesLocked lists the chart directory once for a pinned index.
func (c *chartIndex) listNamesLocked(s *Store) {
	if !c.namesRead {
		c.names, c.namesErr = s.listCharts()
		c.namesRead = true
		c.namesReads++
	}
}

// pinnedListings is, for a pinned store, the model charts that list ticket as
// an entry's id, from a table of every chart built on first use; ok is false
// when the index is not pinned.
func (c *chartIndex) pinnedListings(s *Store, ticket string) (out []Listing, ok bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pinned {
		return nil, false, nil
	}
	if c.listings == nil {
		c.listNamesLocked(s)
		if c.namesErr != nil {
			return nil, true, c.namesErr
		}
		c.listings = map[string][]Listing{}
		for _, name := range c.names {
			cf, err := c.readLocked(s, name)
			if err != nil || cf.Issue == "" {
				continue
			}
			seen := map[string]bool{}
			for i, e := range cf.Tickets {
				if e.ID == "" || seen[e.ID] {
					continue
				}
				seen[e.ID] = true
				c.listings[e.ID] = append(c.listings[e.ID], Listing{Chart: name, Issue: cf.Issue, Position: i + 1, Entry: e})
			}
		}
	}
	return append([]Listing(nil), c.listings[ticket]...), true, nil
}
