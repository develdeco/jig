package tui

import (
	"regexp"
	"strconv"
	"strings"
)

// A verb prints in jig's axi register (internal/axi): labelled blocks of
// key/value lines or CSV rows, a help block, and for a refusal an error and a
// code. The screens read what a verb printed through this reader, so a status
// or a refusal is data and not a string to match.

// axiDoc is one verb's output, read.
type axiDoc struct {
	// Scalars are the top-level `key: value` lines (`state: building`).
	Scalars map[string]string
	// KV are the blocks of indented `key: value` lines, by label.
	KV map[string]map[string]string
	// Tables are the `label[N]{cols}:` blocks, by label.
	Tables map[string]axiTable
	// Help is the lines of the help block.
	Help []string
	// Err is set when the output is a refusal: `error:` and `code:` lines.
	Err *axiErr
}

// axiTable is a block of CSV rows under their column names.
type axiTable struct {
	Cols []string
	Rows [][]string
}

// axiErr is a refusal: what went wrong, its code and the help lines.
type axiErr struct {
	Msg, Code string
	Help      []string
}

// Maps are the rows keyed by column name; a row shorter than the columns
// leaves the rest empty.
func (t axiTable) Maps() []map[string]string {
	out := make([]map[string]string, len(t.Rows))
	for i, row := range t.Rows {
		m := make(map[string]string, len(t.Cols))
		for j, c := range t.Cols {
			if j < len(row) {
				m[c] = row[j]
			} else {
				m[c] = ""
			}
		}
		out[i] = m
	}
	return out
}

var (
	axiTableHeader = regexp.MustCompile(`^([A-Za-z0-9_.\-]+)\[(\d+)\](?:\{([^}]*)\})?:$`)
	axiKVHeader    = regexp.MustCompile(`^([A-Za-z0-9_.\-]+):$`)
	axiScalar      = regexp.MustCompile(`^([A-Za-z0-9_.\-]+): (.*)$`)
)

// parseAxi reads out. It is lenient: a line it does not understand is left
// out, so a verb that prints a note before its blocks, or a block this reader
// has never seen, costs nothing.
func parseAxi(out string) axiDoc {
	doc := axiDoc{
		Scalars: map[string]string{},
		KV:      map[string]map[string]string{},
		Tables:  map[string]axiTable{},
	}
	type block struct {
		label string
		kind  byte // 'k' key/value, 't' table, 'l' list
		table axiTable
	}
	var cur *block
	flush := func() {
		if cur == nil {
			return
		}
		switch cur.kind {
		case 't':
			doc.Tables[cur.label] = cur.table
		case 'l':
			if cur.label == "help" {
				for _, r := range cur.table.Rows {
					doc.Help = append(doc.Help, strings.Join(r, ","))
				}
			}
		}
		cur = nil
	}

	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "  ") {
			if cur == nil {
				continue
			}
			line := raw[2:]
			switch cur.kind {
			case 'k':
				if k, v, ok := strings.Cut(line, ": "); ok {
					doc.KV[cur.label][k] = axiUnquote(v)
				}
			case 't':
				cur.table.Rows = append(cur.table.Rows, axiSplitRow(line))
			case 'l':
				cur.table.Rows = append(cur.table.Rows, []string{line})
			}
			continue
		}
		flush()
		if m := axiTableHeader.FindStringSubmatch(raw); m != nil {
			b := &block{label: m[1], kind: 'l'}
			if m[3] != "" {
				b.kind = 't'
				b.table.Cols = strings.Split(m[3], ",")
			}
			cur = b
			continue
		}
		if m := axiKVHeader.FindStringSubmatch(raw); m != nil {
			doc.KV[m[1]] = map[string]string{}
			cur = &block{label: m[1], kind: 'k'}
			continue
		}
		if m := axiScalar.FindStringSubmatch(raw); m != nil {
			doc.Scalars[m[1]] = axiUnquote(m[2])
		}
	}
	flush()

	if msg, ok := doc.Scalars["error"]; ok {
		doc.Err = &axiErr{Msg: msg, Code: doc.Scalars["code"], Help: doc.Help}
	}
	return doc
}

// axiUnquote undoes internal/axi's Quote: a value in double quotes has its
// escaped quotes and newlines restored. A value that is not quoted is as it
// is.
func axiUnquote(v string) string {
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return v
	}
	var b strings.Builder
	in := v[1 : len(v)-1]
	for i := 0; i < len(in); i++ {
		if in[i] == '\\' && i+1 < len(in) {
			switch in[i+1] {
			case '"':
				b.WriteByte('"')
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			}
		}
		b.WriteByte(in[i])
	}
	return b.String()
}

// axiSplitRow splits one CSV row of a table: commas outside double quotes
// separate cells, and each cell is unquoted.
func axiSplitRow(line string) []string {
	var cells []string
	var cell strings.Builder
	quoted := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && quoted && i+1 < len(line) && (line[i+1] == '"' || line[i+1] == 'n'):
			cell.WriteByte(c)
			cell.WriteByte(line[i+1])
			i++
		case c == '"':
			quoted = !quoted
			cell.WriteByte(c)
		case c == ',' && !quoted:
			cells = append(cells, axiUnquote(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(c)
		}
	}
	return append(cells, axiUnquote(cell.String()))
}

// atoi reads a count a verb printed; anything else is 0.
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
