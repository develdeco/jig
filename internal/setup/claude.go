package setup

import (
	"bytes"
	"errors"
	"fmt"
)

// ClaudeHook is one Claude Code event jig listens to, and the tool matcher
// its entry carries (empty for none).
type ClaudeHook struct {
	Event   string
	Matcher string
}

// ClaudeHooks are the events jig adds an entry for, in the order it adds them.
// PostToolUse is limited to the Bash tool: a commit or a test run is a shell
// command, and nothing else the harness runs is jig's business.
func ClaudeHooks() []ClaudeHook {
	return []ClaudeHook{
		{Event: "SessionStart"},
		{Event: "UserPromptSubmit"},
		{Event: "PostToolUse", Matcher: "Bash"},
		{Event: "Stop"},
		{Event: "SessionEnd"},
	}
}

// ClaudeCommand is the command of every Claude Code entry: jig is the program
// part JigCommand gives, and the event arrives on stdin.
func ClaudeCommand(jig string) string {
	return jig + " observe --harness claude"
}

// parseSettings reads a settings document into its ordered form and the
// layout to write it back in. An empty document is an empty object.
func parseSettings(data []byte) (*object, layout, error) {
	l := detectLayout(data)
	body := bytes.TrimPrefix(data, utf8BOM)
	if len(bytes.TrimSpace(body)) == 0 {
		return newObject(), l, nil
	}
	v, err := parseJSON(body)
	if err != nil {
		return nil, l, fmt.Errorf("not valid JSON: %w", err)
	}
	root, ok := v.(*object)
	if !ok {
		return nil, l, errors.New("the top level is not a JSON object")
	}
	return root, l, nil
}

// MergeClaude adds jig's entries to the Claude Code settings document in
// data (nil or empty for none yet) and returns the document to write. jig is
// the program part of the command (JigCommand).
//
// The merge is idempotent and touches nothing that is not jig's: every key,
// every other hook and their order stay as they were, and the entries jig adds
// go at the end of their event's list. For each event jig wants there is
// exactly one owned entry afterwards: one that is there already is kept in
// place (its command is brought up to date if it differs, so a jig that moved
// is followed), an extra one is dropped, and a missing one is added as a group
// of its own. When nothing needs doing, changed is false and out is data
// itself, byte for byte.
//
// A document that is not a JSON object, or whose hooks are not the shape
// Claude Code reads, is an error and nothing is returned to write: jig does
// not guess at a file it cannot read.
func MergeClaude(data []byte, jig string) (out []byte, changed bool, err error) {
	root, l, err := parseSettings(data)
	if err != nil {
		return nil, false, err
	}
	changed, err = mergeClaudeHooks(root, ClaudeCommand(jig))
	if err != nil {
		return nil, false, err
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

func mergeClaudeHooks(root *object, command string) (bool, error) {
	changed := false
	var hooks *object
	v, present := root.get("hooks")
	if !present || v == nil {
		hooks = newObject()
		root.set("hooks", hooks)
		changed = true
	} else {
		o, ok := v.(*object)
		if !ok {
			return false, errors.New("\"hooks\" is not an object")
		}
		hooks = o
	}
	for _, h := range ClaudeHooks() {
		var groups []any
		if v, present := hooks.get(h.Event); present && v != nil {
			list, ok := v.([]any)
			if !ok {
				return false, fmt.Errorf("\"hooks.%s\" is not a list", h.Event)
			}
			groups = list
		}
		next, c := ensureClaudeEntry(groups, h, command)
		if c {
			hooks.set(h.Event, next)
			changed = true
		}
	}
	return changed, nil
}

// ensureClaudeEntry returns groups with exactly one owned entry for h running
// command, and whether that changed anything.
func ensureClaudeEntry(groups []any, h ClaudeHook, command string) ([]any, bool) {
	changed, found := false, false
	out := make([]any, 0, len(groups)+1)
	for _, g := range groups {
		grp, inner, ok := claudeGroup(g)
		if !ok {
			out = append(out, g)
			continue
		}
		kept := make([]any, 0, len(inner))
		dropped := false
		for _, e := range inner {
			ent, cmd, owned := ownedClaudeEntry(e)
			if !owned {
				kept = append(kept, e)
				continue
			}
			if found {
				dropped, changed = true, true
				continue
			}
			found = true
			if cmd != command {
				ent.set("command", command)
				changed = true
			}
			kept = append(kept, e)
		}
		if dropped {
			if len(kept) == 0 {
				continue
			}
			grp.set("hooks", kept)
		}
		out = append(out, g)
	}
	if !found {
		out = append(out, newClaudeGroup(h, command))
		changed = true
	}
	return out, changed
}

// claudeGroup reads g as a hook group: an object with a list under hooks.
func claudeGroup(g any) (grp *object, inner []any, ok bool) {
	grp, ok = g.(*object)
	if !ok {
		return nil, nil, false
	}
	v, present := grp.get("hooks")
	if !present {
		return nil, nil, false
	}
	inner, ok = v.([]any)
	return grp, inner, ok
}

// ownedClaudeEntry reads e as a hook entry running a command that is jig's.
func ownedClaudeEntry(e any) (ent *object, command string, owned bool) {
	ent, ok := e.(*object)
	if !ok {
		return nil, "", false
	}
	v, present := ent.get("command")
	if !present {
		return nil, "", false
	}
	command, ok = v.(string)
	if !ok || !OwnedCommand(command) {
		return nil, "", false
	}
	return ent, command, true
}

func newClaudeGroup(h ClaudeHook, command string) *object {
	entry := newObject()
	entry.set("type", "command")
	entry.set("command", command)
	g := newObject()
	if h.Matcher != "" {
		g.set("matcher", h.Matcher)
	}
	g.set("hooks", []any{entry})
	return g
}

// UnmergeClaude removes jig's entries from the settings document in data and
// returns the document to write. Only entries OwnedCommand recognizes go, in
// every event, so one an older jig wrote for an event this one no longer uses
// goes too. A group left with no hooks goes with them, then an event left
// with no groups, then hooks itself if jig emptied it; everything else, the
// person's hooks and keys and their order, stays. When there was nothing to
// remove, changed is false and out is data itself.
func UnmergeClaude(data []byte) (out []byte, changed bool, err error) {
	if len(bytes.TrimSpace(bytes.TrimPrefix(data, utf8BOM))) == 0 {
		return data, false, nil
	}
	root, l, err := parseSettings(data)
	if err != nil {
		return nil, false, err
	}
	if !unmergeClaudeHooks(root) {
		return data, false, nil
	}
	out, err = marshalJSON(root, l)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func unmergeClaudeHooks(root *object) bool {
	v, present := root.get("hooks")
	if !present {
		return false
	}
	hooks, ok := v.(*object)
	if !ok {
		return false
	}
	changed := false
	for _, event := range append([]string(nil), hooks.keys...) {
		groups, ok := hooks.vals[event].([]any)
		if !ok {
			continue
		}
		out := make([]any, 0, len(groups))
		removed := false
		for _, g := range groups {
			grp, inner, ok := claudeGroup(g)
			if !ok {
				out = append(out, g)
				continue
			}
			kept := make([]any, 0, len(inner))
			for _, e := range inner {
				if _, _, owned := ownedClaudeEntry(e); !owned {
					kept = append(kept, e)
				}
			}
			if len(kept) == len(inner) {
				out = append(out, g)
				continue
			}
			removed = true
			if len(kept) > 0 {
				grp.set("hooks", kept)
				out = append(out, g)
			}
		}
		if !removed {
			continue
		}
		changed = true
		if len(out) == 0 {
			hooks.del(event)
		} else {
			hooks.set(event, out)
		}
	}
	if changed && hooks.len() == 0 {
		root.del("hooks")
	}
	return changed
}

// ClaudeEvents lists the events of the settings document in data that carry
// an entry of jig's, in the order the document has them. A document that is
// empty has none; one that cannot be read is an error.
func ClaudeEvents(data []byte) ([]string, error) {
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
		groups, ok := hooks.vals[event].([]any)
		if ok && groupsHaveOwnedEntry(groups) {
			events = append(events, event)
		}
	}
	return events, nil
}

func groupsHaveOwnedEntry(groups []any) bool {
	for _, g := range groups {
		_, inner, ok := claudeGroup(g)
		if !ok {
			continue
		}
		for _, e := range inner {
			if _, _, owned := ownedClaudeEntry(e); owned {
				return true
			}
		}
	}
	return false
}

// FilePlan is a change to one settings file, worked out and not yet made: the
// file as it is and as it would be. A caller shows the plan, asks, and then
// applies it.
type FilePlan struct {
	Path string
	// Exists is whether the file is there now.
	Exists bool
	// Before is the file as it is (nil when it is not there), After as it
	// would be. They are the same bytes when there is nothing to do.
	Before, After []byte
}

// Changed reports whether applying the plan would write anything: the bytes
// differ, or the file would be created.
func (p FilePlan) Changed() bool {
	return !bytes.Equal(p.Before, p.After) || (!p.Exists && p.After != nil)
}

// LeavesEmptyObject reports whether the file, once the plan is applied, is a
// JSON object with nothing in it: what a settings file jig created is after
// jig's entries are taken out.
func (p FilePlan) LeavesEmptyObject() bool {
	root, _, err := parseSettings(p.After)
	return err == nil && root.len() == 0
}

// Apply writes the plan: the file replaced atomically, its permissions kept,
// its directory made if need be.
func (p FilePlan) Apply() error {
	return writeFile(p.Path, p.After, 0o644, true)
}

// PlanClaude works out installing jig's Claude Code entries into the settings
// file at path, or taking them out when uninstall is set. A file that is not
// there is an empty document to install into and nothing to uninstall from.
func PlanClaude(path, jig string, uninstall bool) (FilePlan, error) {
	data, exists, err := readIfExists(path)
	if err != nil {
		return FilePlan{}, err
	}
	plan := FilePlan{Path: path, Exists: exists, Before: data}
	if uninstall {
		plan.After, _, err = UnmergeClaude(data)
	} else {
		plan.After, _, err = MergeClaude(data, jig)
	}
	if err != nil {
		return FilePlan{}, fmt.Errorf("%s: %w", path, err)
	}
	return plan, nil
}
