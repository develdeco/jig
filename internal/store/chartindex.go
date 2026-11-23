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
	path := s.ChartFile(name)
	info, statErr := os.Stat(path)
	c.mu.Lock()
	defer c.mu.Unlock()
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
