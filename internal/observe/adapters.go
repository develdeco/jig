package observe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Adapter maps one harness's hook payloads onto Event and the core's Result
// back onto what that harness reads. It is the only place that knows the
// harness's shape; the core never does.
type Adapter interface {
	// Name is the harness's name, as `jig observe --harness` spells it.
	Name() string
	// Parse reads one payload. An event the harness sends that jig does not
	// act on gives an Event with no Kind and a nil error; a payload that is
	// not this harness's shape (not JSON, not an object) gives an error.
	Parse(data []byte) (Event, error)
	// Render writes what the harness reads from the hook's stdout for r, the
	// core's answer to ev. nil means print nothing.
	Render(ev Event, r Result) []byte
}

// The adapters, by name. Each adapters_*.go file adds its own in an init.
var adapters = map[string]Adapter{}

func registerAdapter(a Adapter) {
	if _, dup := adapters[a.Name()]; dup {
		panic(fmt.Sprintf("observe: adapter %q is already registered", a.Name()))
	}
	adapters[a.Name()] = a
}

// AdapterFor returns the adapter of the named harness.
func AdapterFor(name string) (Adapter, bool) {
	a, ok := adapters[name]
	return a, ok
}

// Harnesses lists the names of the harnesses with an adapter, sorted.
func Harnesses() []string {
	names := make([]string, 0, len(adapters))
	for n := range adapters {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// payload is a decoded JSON object, read by dotted path. Every accessor
// takes several paths and answers from the first that holds a value of the
// right type, so an adapter spells a harness's alternatives in one call. A
// path that is missing, or holds another type, gives the zero value: the
// payloads of hooks come from tools that change, and an adapter must read
// what is there rather than refuse what is not.
type payload map[string]any

// parsePayload decodes data as one JSON object.
func parsePayload(data []byte) (payload, error) {
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if len(data) == 0 {
		return nil, errors.New("observe: the payload is empty")
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("observe: the payload is not JSON: %w", err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("observe: the payload is not a JSON object")
	}
	return payload(obj), nil
}

// at walks one dotted path.
func (p payload) at(path string) (any, bool) {
	var cur any = map[string]any(p)
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[key]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// str is the first non-empty string among paths.
func (p payload) str(paths ...string) string {
	for _, path := range paths {
		if s, ok := p.at(path); ok {
			if str, ok := s.(string); ok && str != "" {
				return str
			}
		}
	}
	return ""
}

// flag is the first bool among paths.
func (p payload) flag(paths ...string) bool {
	for _, path := range paths {
		if v, ok := p.at(path); ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

// integer is the first whole number among paths.
func (p payload) integer(paths ...string) (int, bool) {
	for _, path := range paths {
		if v, ok := p.at(path); ok {
			if f, ok := v.(float64); ok && f == float64(int(f)) {
				return int(f), true
			}
		}
	}
	return 0, false
}

// strs is the strings of the first array among paths (anything else in it is
// skipped).
func (p payload) strs(paths ...string) []string {
	for _, path := range paths {
		v, ok := p.at(path)
		if !ok {
			continue
		}
		arr, ok := v.([]any)
		if !ok {
			continue
		}
		var out []string
		for _, e := range arr {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// object is the first object among paths. A string that holds a JSON object
// counts, since a harness may send its tool arguments that way (Copilot's
// toolArgs).
func (p payload) object(paths ...string) payload {
	for _, path := range paths {
		v, ok := p.at(path)
		if !ok {
			continue
		}
		switch x := v.(type) {
		case map[string]any:
			return payload(x)
		case string:
			var m map[string]any
			if json.Unmarshal([]byte(x), &m) == nil {
				return payload(m)
			}
		}
	}
	return nil
}

// exitStatus reads the exit status a tool result reports under the usual
// names, or none.
func (p payload) exitStatus(prefix string) *int {
	var paths []string
	for _, name := range []string{"exit_code", "exitCode", "returncode", "returnCode"} {
		paths = append(paths, under(prefix, name))
	}
	if n, ok := p.integer(paths...); ok {
		return &n
	}
	return nil
}

// editedFiles reads the files a tool input names under the usual keys.
func (p payload) editedFiles(prefix string) []string {
	var out []string
	for _, name := range []string{"file_path", "notebook_path", "filePath", "absolute_path", "path"} {
		if s := p.str(under(prefix, name)); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// under is the path of key below prefix; an empty prefix is the top.
func under(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// normalizeName folds an event name to lower case without separators, so
// "session-start", "SessionStart" and "session_start" are one name.
func normalizeName(s string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(s))
}

// jsonLine encodes v as one line of JSON without HTML escaping, the way a
// person reading a hook's output expects it, or nil when v is not encodable.
func jsonLine(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return nil
	}
	return buf.Bytes()
}

// joinText joins the non-empty parts with a blank line between them.
func joinText(parts ...string) string {
	var kept []string
	for _, s := range parts {
		if s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, "\n\n")
}
