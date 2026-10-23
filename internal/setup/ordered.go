package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// A harness settings file is the person's own: their keys, in the order they
// wrote them, next to the few entries jig adds. Reading it into a Go map
// would sort the keys and rewrite the file in an order nobody chose, so the
// merge works on an ordered form instead: parseJSON reads a document into
// *object (a JSON object that remembers its key order), []any, string,
// json.Number (the number exactly as written), bool and nil, and
// marshalJSON writes that form back out.

// object is a JSON object whose keys keep the order they were read or set in.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

// get returns the value under key.
func (o *object) get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// set puts v under key: in place when the key exists, at the end when it does
// not.
func (o *object) set(key string, v any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// del removes key and its value.
func (o *object) del(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *object) len() int { return len(o.keys) }

// parseJSON reads exactly one JSON value from data. Numbers keep their
// written form. A key that appears twice in one object keeps the later value,
// at the place of the first, the way a reader of the file resolves it.
func parseJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := readValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("more than one value in the document")
		}
		return nil, err
	}
	return v, nil
}

func readValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil // string, json.Number, bool or nil
	}
	switch delim {
	case '{':
		o := newObject()
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, fmt.Errorf("object key %v is not a string", keyTok)
			}
			v, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			o.set(key, v)
		}
		if _, err := dec.Token(); err != nil { // the closing brace
			return nil, err
		}
		return o, nil
	case '[':
		items := []any{}
		for dec.More() {
			v, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		if _, err := dec.Token(); err != nil { // the closing bracket
			return nil, err
		}
		return items, nil
	}
	return nil, fmt.Errorf("unexpected %q", delim)
}

// layout is how a file was laid out, so the file written back looks like the
// one that was read: its indent, its line ending, whether it began with a byte
// order mark and whether it ended with a newline.
type layout struct {
	indent       string
	eol          string
	bom          bool
	finalNewline bool
}

var utf8BOM = []byte("\xef\xbb\xbf")

// detectLayout reads data's layout; a file with nothing to go by (empty, or
// all on one line) gets two spaces, LF and a final newline.
func detectLayout(data []byte) layout {
	l := layout{indent: "  ", eol: "\n", finalNewline: true}
	if bytes.HasPrefix(data, utf8BOM) {
		l.bom = true
		data = data[len(utf8BOM):]
	}
	if len(data) == 0 {
		return l
	}
	if i := bytes.IndexByte(data, '\n'); i > 0 && data[i-1] == '\r' {
		l.eol = "\r\n"
	}
	l.finalNewline = data[len(data)-1] == '\n'
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		lead := line[:len(line)-len(trimmed)]
		if lead == "" || trimmed == "" || trimmed == "\r" {
			continue
		}
		if strings.HasPrefix(lead, "\t") {
			l.indent = "\t"
		} else {
			l.indent = lead
		}
		break
	}
	return l
}

// marshalJSON writes v, which parseJSON produced (or code built from the same
// kinds), in layout l.
func marshalJSON(v any, l layout) ([]byte, error) {
	var b bytes.Buffer
	if l.bom {
		b.Write(utf8BOM)
	}
	if err := writeValue(&b, v, l, 0); err != nil {
		return nil, err
	}
	if l.finalNewline {
		b.WriteString(l.eol)
	}
	return b.Bytes(), nil
}

func writeValue(b *bytes.Buffer, v any, l layout, depth int) error {
	newline := func(d int) {
		b.WriteString(l.eol)
		for i := 0; i < d; i++ {
			b.WriteString(l.indent)
		}
	}
	switch t := v.(type) {
	case *object:
		if t.len() == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(depth + 1)
			if err := writeString(b, k); err != nil {
				return err
			}
			b.WriteString(": ")
			if err := writeValue(b, t.vals[k], l, depth+1); err != nil {
				return err
			}
		}
		newline(depth)
		b.WriteByte('}')
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(depth + 1)
			if err := writeValue(b, item, l, depth+1); err != nil {
				return err
			}
		}
		newline(depth)
		b.WriteByte(']')
	case string:
		return writeString(b, t)
	case json.Number:
		b.WriteString(t.String())
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	default:
		return fmt.Errorf("cannot write a %T as JSON", v)
	}
	return nil
}

// writeString writes s as a JSON string without HTML escaping, so an "&" or a
// "<" in a person's command stays as they wrote it.
func writeString(b *bytes.Buffer, s string) error {
	var enc bytes.Buffer
	e := json.NewEncoder(&enc)
	e.SetEscapeHTML(false)
	if err := e.Encode(s); err != nil {
		return err
	}
	b.Write(bytes.TrimSuffix(enc.Bytes(), []byte("\n")))
	return nil
}
