package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// ChartRef is one blocked_by entry in a chart's tickets.yaml: a reference to
// another entry (in this or another chart's tickets.yaml) or an existing
// ticket id, plus an optional merge kind. An empty Kind means "merged".
type ChartRef struct {
	Ref  string `yaml:"ref"`
	Kind string `yaml:"kind,omitempty"`
}

// ChartEntry is one ticket entry in a chart's tickets.yaml.
type ChartEntry struct {
	ID        string     `yaml:"id,omitempty"`
	Key       string     `yaml:"key,omitempty"`
	Title     string     `yaml:"title"`
	Body      string     `yaml:"body,omitempty"`
	BlockedBy []ChartRef `yaml:"blocked_by,omitempty"`

	// asRead is the id, title and body exactly as ReadChart returned them,
	// carried along on the entry itself so a later WriteChart can tell a
	// concurrent edit at this position from the caller's own change: see
	// updateChartEntries. It is nil for an entry that was never read
	// (built by a caller from scratch), which skips that check, and it is
	// not part of the wire format.
	//
	// This unexported pointer also means two ChartEntry values with
	// identical wire content can compare unequal under == and
	// reflect.DeepEqual (one read, one built fresh, or two separate reads
	// of the same file); compare the exported fields, not the struct as a
	// whole.
	asRead *chartEntrySnapshot
}

// chartEntrySnapshot is the part of a ChartEntry that WriteChart's
// stale-read guard compares against the locked re-read.
type chartEntrySnapshot struct {
	id, title, body string
}

// ChartFile is the wire shape of charts/<name>/tickets.yaml.
type ChartFile struct {
	Tickets []ChartEntry `yaml:"tickets"`
}

// ReadChart reads charts/<name>/tickets.yaml. An absent file is an error.
func (s *Store) ReadChart(name string) ([]ChartEntry, error) {
	path := s.ChartFile(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cf ChartFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, err
	}
	for i := range cf.Tickets {
		cf.Tickets[i].asRead = &chartEntrySnapshot{id: cf.Tickets[i].ID, title: cf.Tickets[i].Title, body: cf.Tickets[i].Body}
	}
	return cf.Tickets, nil
}

// WriteChart writes entries back to charts/<name>/tickets.yaml under a locked,
// read-modify-write cycle. It preserves YAML comments and field order as much
// as possible by using a YAML node decoder and encoder.
func (s *Store) WriteChart(name string, entries []ChartEntry) error {
	path := s.ChartFile(name)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}

	// Update the YAML node with new entries, preserving comments
	if err := updateChartEntries(&node, entries); err != nil {
		return err
	}

	out, err := yaml.Marshal(&node)
	if err != nil {
		return err
	}
	return AtomicWrite(path, out)
}

// updateChartEntries updates the tickets array in the YAML node with the new
// entries, preserving comments and structure.
func updateChartEntries(node *yaml.Node, entries []ChartEntry) error {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return fmt.Errorf("invalid chart file structure")
	}

	rootNode := node.Content[0]
	if rootNode.Kind != yaml.MappingNode {
		return fmt.Errorf("chart file root must be a mapping")
	}

	var ticketsNode *yaml.Node
	for i := 0; i < len(rootNode.Content); i += 2 {
		keyNode := rootNode.Content[i]
		if keyNode.Value == "tickets" {
			ticketsNode = rootNode.Content[i+1]
			break
		}
	}

	if ticketsNode == nil {
		return fmt.Errorf("tickets key not found in chart file")
	}

	if ticketsNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("tickets must be a sequence")
	}

	// This write is prepared against exactly len(entries) entries. If the
	// locked re-read shows fewer, some entry the caller means to update was
	// removed (or the file was rewritten) since it read the chart unlocked;
	// updating by position would then land on the wrong entry or fabricate
	// one, so refuse rather than guess. Any entry beyond len(entries) is left
	// untouched below (never truncated), so a concurrent append survives
	// this write.
	if len(ticketsNode.Content) < len(entries) {
		return fmt.Errorf("chart file has %d entries, fewer than the %d this write is based on; re-read the chart and retry", len(ticketsNode.Content), len(entries))
	}

	// The count check above catches a shrink but not an insert or a
	// reorder: either can hold the count at or above len(entries) while
	// putting a different entry under a position this write means to
	// update, and updating by position would then land on the wrong entry.
	// The same by-position update would also silently revert a concurrent
	// hand-edit to a title or body at a position nothing moved. Both are
	// the same hazard - the file no longer matches what this write is
	// based on at some position - so refuse the whole write if any entry
	// that was read (asRead != nil) no longer matches the locked re-read.
	for i, entry := range entries {
		if entry.asRead == nil {
			continue
		}
		existingNode := ticketsNode.Content[i]
		if existingNode.Kind != yaml.MappingNode {
			continue
		}
		var current struct {
			ID    string `yaml:"id"`
			Title string `yaml:"title"`
			Body  string `yaml:"body"`
		}
		if err := existingNode.Decode(&current); err != nil {
			continue
		}
		if current.Title != entry.asRead.title || current.Body != entry.asRead.body {
			return fmt.Errorf("chart file entry %d no longer matches what this write is based on (its title or body changed since it was read); re-read the chart and retry", i+1)
		}
		// id is the one field graduate actually writes back, so a stale
		// write must not clobber an id recorded by a concurrent run since
		// this entry was read. Compare against both what was read and what
		// this write is placing: the latter keeps graduate's own repeated
		// per-mint write-backs allowed, since by the second and later of
		// those the file's id already equals entry.ID.
		if current.ID != entry.asRead.id && current.ID != entry.ID {
			return fmt.Errorf("chart file entry %d's id changed since it was read (now %q, read as %q); re-read the chart and retry", i+1, current.ID, entry.asRead.id)
		}
	}

	// Update the entries in place, preserving comments; anything at or past
	// len(entries) is left as-is.
	for i, entry := range entries {
		existingNode := ticketsNode.Content[i]
		if existingNode.Kind != yaml.MappingNode {
			// Recreate as a mapping
			entryNode := &yaml.Node{Kind: yaml.MappingNode}
			setChartEntryFields(entryNode, entry)
			ticketsNode.Content[i] = entryNode
		} else {
			updateChartEntryInNode(existingNode, entry)
		}
	}

	return nil
}

// setChartEntryFields sets all fields of a chart entry node.
func setChartEntryFields(node *yaml.Node, entry ChartEntry) {
	node.Content = nil

	if entry.ID != "" {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "id"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: entry.ID},
		)
	}

	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "title"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: entry.Title},
	)

	if entry.Body != "" {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "body"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: entry.Body, Tag: "!!str", Style: yaml.LiteralStyle},
		)
	}

	if len(entry.BlockedBy) > 0 {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "blocked_by"},
			blockedByNode(entry.BlockedBy),
		)
	}
}

// blockedByNode builds the blocked_by sequence node for refs: one mapping
// per ref, with "kind" omitted when it is empty (the file's spelling of the
// default, "merged").
func blockedByNode(refs []ChartRef) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode}
	for _, ref := range refs {
		node.Content = append(node.Content, blockedByItem(ref))
	}
	return node
}

// blockedByItem builds a single blocked_by mapping node for ref, fresh (no
// comments, no keys beyond ref/kind).
func blockedByItem(ref ChartRef) *yaml.Node {
	item := &yaml.Node{Kind: yaml.MappingNode}
	item.Content = append(item.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "ref"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: ref.Ref},
	)
	if ref.Kind != "" {
		item.Content = append(item.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "kind"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: ref.Kind},
		)
	}
	return item
}

// mergeBlockedByNode updates node's existing sequence items to match refs,
// matching by position rather than rebuilding the sequence from scratch: a
// surviving item has only its ref/kind scalars rewritten (when they
// actually differ), so any comment or per-item key the store does not
// model stays attached to it. Items beyond len(refs) are dropped; refs
// beyond the existing items are appended fresh.
func mergeBlockedByNode(node *yaml.Node, refs []ChartRef) {
	if node.Kind != yaml.SequenceNode {
		// node is being converted from some other kind (most likely a
		// scalar written as a mistaken value, e.g. `blocked_by: "oops"`);
		// its Value/Tag/Style belong to that old kind and must not survive
		// onto the sequence, or yaml.v3 re-emits them alongside the new
		// items.
		node.Kind = yaml.SequenceNode
		node.Value = ""
		node.Tag = ""
		node.Style = 0
		node.Content = nil
	}
	for i, ref := range refs {
		if i < len(node.Content) {
			mergeBlockedByItem(node.Content[i], ref)
		} else {
			node.Content = append(node.Content, blockedByItem(ref))
		}
	}
	if len(refs) < len(node.Content) {
		node.Content = node.Content[:len(refs)]
	}
}

// mergeBlockedByItem rewrites item's ref and kind scalars to match ref,
// leaving any other key on the mapping (and its comments) untouched. item
// is replaced outright only if it is not a mapping to begin with.
func mergeBlockedByItem(item *yaml.Node, ref ChartRef) {
	if item.Kind != yaml.MappingNode {
		*item = *blockedByItem(ref)
		return
	}
	sawRef := false
	sawKind := false
	for i := 0; i < len(item.Content); i += 2 {
		switch item.Content[i].Value {
		case "ref":
			sawRef = true
			setScalarValue(item.Content[i+1], ref.Ref)
		case "kind":
			sawKind = true
			if ref.Kind == "" {
				item.Content = append(item.Content[:i], item.Content[i+2:]...)
				i -= 2
			} else {
				setScalarValue(item.Content[i+1], ref.Kind)
			}
		}
	}
	if !sawRef {
		item.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "ref"},
			{Kind: yaml.ScalarNode, Value: ref.Ref},
		}, item.Content...)
	}
	if !sawKind && ref.Kind != "" {
		item.Content = append(item.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "kind"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: ref.Kind},
		)
	}
}

// setScalarValue overwrites node's value in place. A node's Tag records how
// yaml.v3 resolved its old value - implicitly, from the value itself (an
// empty key is !!null, an unquoted `123` is !!int, an unquoted `2026-09-27`
// is !!timestamp) or explicitly, because the author wrote it out (`!!null`,
// `!!int 7`, `!mytype foo`); leaving that Tag in place while replacing Value
// with a string the tag can't parse produces an unparseable `!!<tag> <value>`
// once yaml.v3 re-emits it. Any tag is therefore cleared to let yaml.v3
// re-resolve it from the new value - except !!str, which is left alone since
// an explicit `!!str 123` is the one case where the old tag still parses the
// new value (as the string it already names) and clearing it would instead
// let yaml.v3 re-resolve the new value to some other type. Style is left
// alone: clearing it would restyle every scalar this touches (for example
// dropping a title's author-chosen quotes), and for a quoted "null"-ish
// value it would make yaml.v3 resolve the now-unquoted scalar as an actual
// null instead of the string it was written as.
//
// A node that is not a scalar to begin with (most likely a mapping or
// sequence written by mistake, e.g. `title: {oops: true}`) is converted
// first, the same way mergeBlockedByNode converts a non-sequence node:
// otherwise the encoder ignores Value on anything but a scalar, so the
// write would silently keep the old, wrong content.
func setScalarValue(node *yaml.Node, value string) {
	if node.Kind != yaml.ScalarNode {
		node.Kind = yaml.ScalarNode
		node.Tag = ""
		node.Style = 0
		node.Content = nil
	}
	node.Value = value
	if node.Tag != "!!str" {
		node.Tag = ""
	}
}

// bodyNodeMatches reports whether node already decodes to body, so a write
// that does not change an entry's body can leave the node (and any comment
// attached to its key) alone instead of dropping it.
func bodyNodeMatches(node *yaml.Node, body string) bool {
	var existing string
	if err := node.Decode(&existing); err != nil {
		return false
	}
	return existing == body
}

// blockedByNodeMatches reports whether node already decodes to refs, so a
// write that does not change an entry's blockers can leave the node (and any
// comments attached to its items) alone instead of rebuilding it from scratch.
func blockedByNodeMatches(node *yaml.Node, refs []ChartRef) bool {
	var existing []ChartRef
	if err := node.Decode(&existing); err != nil {
		return false
	}
	if len(existing) != len(refs) {
		return false
	}
	for i, r := range refs {
		if existing[i].Ref != r.Ref || existing[i].Kind != r.Kind {
			return false
		}
	}
	return true
}

// updateChartEntryInNode updates an existing entry node with new values.
func updateChartEntryInNode(node *yaml.Node, entry ChartEntry) {
	// Track which keys we've updated
	updated := make(map[string]bool)

	// Update existing keys or add new ones
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		key := keyNode.Value

		switch key {
		case "id":
			if entry.ID != "" {
				setScalarValue(node.Content[i+1], entry.ID)
			} else {
				// Remove the id field if empty
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				i -= 2
			}
			updated["id"] = true
		case "title":
			setScalarValue(node.Content[i+1], entry.Title)
			updated["title"] = true
		case "body":
			switch {
			case bodyNodeMatches(node.Content[i+1], entry.Body):
				// The body is unchanged, including "still empty" - leave the
				// existing node (and any comment attached to its key) alone.
			case entry.Body == "":
				// The caller means to clear the body; drop the key, matching
				// id, rather than blank it in place.
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				i -= 2
			default:
				setScalarValue(node.Content[i+1], entry.Body)
			}
			updated["body"] = true
		case "blocked_by":
			switch {
			case blockedByNodeMatches(node.Content[i+1], entry.BlockedBy):
				// The refs are unchanged, including "still none to write" -
				// leave the existing node (and any comments attached to its
				// items) untouched rather than rebuilding it.
			case len(entry.BlockedBy) == 0:
				// The caller means to clear every blocker; drop the key so
				// the file matches the write, rather than leaving the old
				// refs behind under a key the caller believes is now empty.
				node.Content = append(node.Content[:i], node.Content[i+2:]...)
				i -= 2
			default:
				mergeBlockedByNode(node.Content[i+1], entry.BlockedBy)
			}
			updated["blocked_by"] = true
		}
	}

	// Add missing fields. id goes first, matching the file's documented
	// shape (brief.md:46) and the only place it stays readable once a body
	// is multi-line; the rest are appended in read order.
	if !updated["id"] && entry.ID != "" {
		node.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "id"},
			{Kind: yaml.ScalarNode, Value: entry.ID},
		}, node.Content...)
	}
	if !updated["title"] {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "title"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: entry.Title},
		)
	}
	if !updated["body"] && entry.Body != "" {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "body"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: entry.Body, Tag: "!!str", Style: yaml.LiteralStyle},
		)
	}
	if !updated["blocked_by"] && len(entry.BlockedBy) > 0 {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "blocked_by"},
			blockedByNode(entry.BlockedBy),
		)
	}
}

// ChartFile returns the path to charts/<name>/tickets.yaml.
func (s *Store) ChartFile(name string) string {
	return filepath.Join(s.Root, "charts", name, "tickets.yaml")
}

// ChartMapFile returns the path to charts/<name>/map.md.
func (s *Store) ChartMapFile(name string) string {
	return filepath.Join(s.Root, "charts", name, "map.md")
}

// ChartNames lists the store's chart names, in alphabetical order: the
// order the GitHub mirror syncs charts in, after every ticket. A chart is a
// subdirectory of charts/ that has a tickets.yaml; an absent charts/
// directory lists no charts.
func (s *Store) ChartNames() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "charts"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: list charts: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(s.ChartFile(e.Name())); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
