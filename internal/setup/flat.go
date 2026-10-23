package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// flatFormat is a harness whose hook file lists its entries directly under
// each event, with no groups: {"version": 1, "hooks": {"<event>": [{...}]}}.
// Cursor's hooks.json and Copilot's hook files are this shape.
type flatFormat struct {
	harness string
	// events are the vendor's event names jig adds an entry for, in order.
	events []string
	// ownerKey is the entry key whose string value is the command ownership is
	// read from (OwnedCommand).
	ownerKey string
	// build makes the entry that runs command.
	build func(command string) *object
	// commandKeys are the keys of that entry that hold the command, so an
	// entry already there is brought up to date when jig moves.
	commandKeys []string
	// version is the top-level version number the harness requires (0 for
	// none).
	version int
}

// setFirst puts v under key at the front of the object (in place when the key
// is there already).
func (o *object) setFirst(key string, v any) {
	if _, ok := o.vals[key]; ok {
		o.vals[key] = v
		return
	}
	o.keys = append([]string{key}, o.keys...)
	o.vals[key] = v
}

// jsonInt is n as a JSON number in the ordered form.
func jsonInt(n int) json.Number { return json.Number(fmt.Sprint(n)) }

func (f flatFormat) merge(data []byte, jig string) (out []byte, changed bool, err error) {
	root, l, err := parseSettings(data)
	if err != nil {
		return nil, false, err
	}
	command := ObserveCommand(jig, f.harness)
	if f.version != 0 {
		if _, ok := root.get("version"); !ok {
			root.setFirst("version", jsonInt(f.version))
			changed = true
		}
	}
	var hooks *object
	if v, present := root.get("hooks"); !present || v == nil {
		hooks = newObject()
		root.set("hooks", hooks)
		changed = true
	} else if o, ok := v.(*object); ok {
		hooks = o
	} else {
		return nil, false, errors.New("\"hooks\" is not an object")
	}
	for _, event := range f.events {
		var entries []any
		if v, present := hooks.get(event); present && v != nil {
			list, ok := v.([]any)
			if !ok {
				return nil, false, fmt.Errorf("\"hooks.%s\" is not a list", event)
			}
			entries = list
		}
		next, c := f.ensureEntry(entries, command)
		if c {
			hooks.set(event, next)
			changed = true
		}
	}
	if !changed {
		return data, false, nil
	}
	out, err = marshalJSON(root, l)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// ensureEntry returns entries with exactly one owned entry running command,
// and whether that changed anything.
func (f flatFormat) ensureEntry(entries []any, command string) ([]any, bool) {
	changed, found := false, false
	out := make([]any, 0, len(entries)+1)
	for _, e := range entries {
		ent, owned := f.owned(e)
		if !owned {
			out = append(out, e)
			continue
		}
		if found {
			changed = true
			continue
		}
		found = true
		fresh := f.build(command)
		for _, key := range f.commandKeys {
			want, ok := fresh.get(key)
			if !ok {
				continue
			}
			if have, present := ent.get(key); !present || have != want {
				ent.set(key, want)
				changed = true
			}
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, f.build(command))
		changed = true
	}
	return out, changed
}

// owned reads e as an entry running a command that is jig's.
func (f flatFormat) owned(e any) (*object, bool) {
	ent, ok := e.(*object)
	if !ok {
		return nil, false
	}
	v, present := ent.get(f.ownerKey)
	if !present {
		return nil, false
	}
	command, ok := v.(string)
	if !ok || !OwnedCommand(command) {
		return nil, false
	}
	return ent, true
}

func (f flatFormat) unmerge(data []byte) (out []byte, changed bool, err error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return data, false, nil
	}
	root, l, err := parseSettings(data)
	if err != nil {
		return nil, false, err
	}
	v, present := root.get("hooks")
	hooks, ok := v.(*object)
	if !present || !ok {
		return data, false, nil
	}
	for _, event := range append([]string(nil), hooks.keys...) {
		entries, ok := hooks.vals[event].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(entries))
		for _, e := range entries {
			if _, owned := f.owned(e); !owned {
				kept = append(kept, e)
			}
		}
		if len(kept) == len(entries) {
			continue
		}
		changed = true
		if len(kept) == 0 {
			hooks.del(event)
		} else {
			hooks.set(event, kept)
		}
	}
	if !changed {
		return data, false, nil
	}
	if hooks.len() == 0 {
		root.del("hooks")
	}
	out, err = marshalJSON(root, l)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// installed lists the events of the document in data that carry an entry of
// jig's, in the order the document has them.
func (f flatFormat) installed(data []byte) ([]string, error) {
	root, _, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	v, present := root.get("hooks")
	if !present {
		return nil, nil
	}
	hooks, ok := v.(*object)
	if !ok {
		return nil, errors.New("\"hooks\" is not an object")
	}
	var events []string
	for _, event := range hooks.keys {
		entries, _ := hooks.vals[event].([]any)
		for _, e := range entries {
			if _, owned := f.owned(e); owned {
				events = append(events, event)
				break
			}
		}
	}
	return events, nil
}
