package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// HookSpec is one event of a harness that jig listens to, and the tool matcher
// its entry carries (empty for none).
type HookSpec struct {
	Event   string
	Matcher string
}

// ClaudeHook is a HookSpec under the name the Claude Code installer first gave
// it.
type ClaudeHook = HookSpec

// ClaudeHooks are the events jig adds an entry for, in the order it adds them.
// PostToolUse and PostToolUseFailure are limited to the shell tools, Bash and,
// on Windows, PowerShell: a commit or a test run is a shell command, and
// nothing else the harness runs is jig's business. PostToolUseFailure carries
// a shell call that exited non-zero, so a commit followed by a failing
// command in one call is still seen at once.
func ClaudeHooks() []ClaudeHook {
	return []ClaudeHook{
		{Event: "SessionStart"},
		{Event: "UserPromptSubmit"},
		{Event: "PostToolUse", Matcher: claudeShellTools},
		{Event: "PostToolUseFailure", Matcher: claudeShellTools},
		{Event: "Stop"},
		{Event: "SessionEnd"},
	}
}

// claudeShellTools matches Claude Code's shell tools by name.
const claudeShellTools = "Bash|PowerShell"

// ClaudeCommand is the command of every Claude Code entry: jig is the program
// part JigCommand gives, and the event arrives on stdin.
func ClaudeCommand(jig string) string {
	return ObserveCommand(jig, "claude")
}

// claudeFormat is Claude Code's settings file: grouped entries of type
// command.
var claudeFormat = groupedFormat{
	harness: "claude",
	hooks:   ClaudeHooks(),
	entry: func(command string) *object {
		e := newObject()
		e.set("type", "command")
		e.set("command", command)
		return e
	},
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
	return claudeFormat.merge(data, jig)
}

// groupedFormat is a harness whose hook file nests its entries in groups, as
// Claude Code's does: hooks.<Event> is a list of groups, each an object with an
// optional matcher and a list of hooks. Codex and the Gemini CLI use the same
// shape, with other event names and other fields on the entry.
type groupedFormat struct {
	harness string
	hooks   []HookSpec
	// entry builds the hook entry that runs command.
	entry func(command string) *object
}

// merge is MergeClaude for any grouped harness: data with an entry running
// the harness's observe command through jig for each of its events.
func (f groupedFormat) merge(data []byte, jig string) (out []byte, changed bool, err error) {
	root, l, err := parseSettings(data)
	if err != nil {
		return nil, false, err
	}
	changed, err = f.mergeHooks(root, ObserveCommand(jig, f.harness))
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

func (f groupedFormat) mergeHooks(root *object, command string) (bool, error) {
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
	for _, h := range f.hooks {
		var groups []any
		if v, present := hooks.get(h.Event); present && v != nil {
			list, ok := v.([]any)
			if !ok {
				return false, fmt.Errorf("\"hooks.%s\" is not a list", h.Event)
			}
			groups = list
		}
		next, c := f.ensureEntry(groups, h, command)
		if c {
			hooks.set(h.Event, next)
			changed = true
		}
	}
	return changed, nil
}

// ensureEntry returns groups with exactly one owned entry for h running
// command, and whether that changed anything.
func (f groupedFormat) ensureEntry(groups []any, h HookSpec, command string) ([]any, bool) {
	changed, found := false, false
	var moved []any
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
			if groupMatcher(grp) != h.Matcher {
				// An entry an older jig installed under another matcher: a
				// group of jig's alone takes the new one; from a group it
				// shares, jig's entry moves to a group of its own, so the
				// person's hooks keep their matcher.
				if len(inner) == 1 {
					setMatcher(grp, h.Matcher)
				} else {
					moved = append(moved, newGroup(h, ent))
					dropped = true
				}
				changed = true
				if len(inner) != 1 {
					continue
				}
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
	out = append(out, moved...)
	if !found {
		out = append(out, newGroup(h, f.entry(command)))
		changed = true
	}
	return out, changed
}

// groupMatcher is a hook group's matcher, "" when it has none.
func groupMatcher(grp *object) string {
	v, ok := grp.get("matcher")
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// setMatcher sets a hook group's matcher, removing it for "".
func setMatcher(grp *object, matcher string) {
	if matcher == "" {
		grp.del("matcher")
		return
	}
	grp.set("matcher", matcher)
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

func newGroup(h HookSpec, entry *object) *object {
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
	// Delete says the file would be removed: what jig wrote was all there was
	// in it (a hook script of its own, a file that held only its entries).
	Delete bool
}

// Changed reports whether applying the plan would write anything: the bytes
// differ, the file would be created, or it would be removed.
func (p FilePlan) Changed() bool {
	if p.Delete {
		return p.Exists
	}
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
// its directory made if need be; or removed, for a plan that deletes it.
func (p FilePlan) Apply() error {
	if p.Delete {
		if err := os.Remove(p.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeFile(p.Path, p.After, 0o644, true)
}

// PlanClaude works out installing jig's Claude Code entries into the settings
// file at path, or taking them out when uninstall is set. A file that is not
// there is an empty document to install into and nothing to uninstall from.
func PlanClaude(path, jig string, uninstall bool) (FilePlan, error) {
	return planFile(path, jig, uninstall, MergeClaude, UnmergeClaude)
}

// planFile works out a change to the file at path with a harness's merge and
// unmerge. An unmerge that returns nothing to write, with a change, says the
// file was all jig's: the plan then deletes it.
func planFile(path, jig string, uninstall bool, merge func([]byte, string) ([]byte, bool, error), unmerge func([]byte) ([]byte, bool, error)) (FilePlan, error) {
	data, exists, err := readIfExists(path)
	if err != nil {
		return FilePlan{}, err
	}
	plan := FilePlan{Path: path, Exists: exists, Before: data}
	if uninstall {
		var changed bool
		plan.After, changed, err = unmerge(data)
		if err == nil && changed && plan.After == nil {
			plan.Delete = true
		}
	} else {
		plan.After, _, err = merge(data, jig)
	}
	if err != nil {
		return FilePlan{}, fmt.Errorf("%s: %w", path, err)
	}
	return plan, nil
}
