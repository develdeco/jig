package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/develdeco/jig/internal/observe"
)

// Instructions tells a model what the tools are, once, at initialize.
const Instructions = "These tools run the jig command of the same name in the directory the server was started in, " +
	"and return what it printed. Start with here to see where the work is, next for what is ready, " +
	"context for what a slice needs, and claim before building a slice. Ask the person with ask; " +
	"the answer shows under questions in context."

// Runner runs one jig command in this process. argv starts with the command
// words ("claim") and holds the rest as the command line would; stdin is what
// the command reads. It returns what the command printed and its exit code.
type Runner func(ctx context.Context, argv []string, stdin string) (stdout string, code int)

// Options is what the tools need from the program that serves them.
type Options struct {
	// Run executes a command.
	Run Runner
	// Pin returns the flags that hold a command to the store or project the
	// server was started with; an error stops the call. nil pins nothing.
	Pin func(verb string) ([]string, error)
}

// Argument types of a tool, as JSON Schema names them.
const (
	typeString  = "string"
	typeBoolean = "boolean"
	typeInteger = "integer"
	typeArray   = "array"
)

// ParamKind is where an argument goes on the command line.
type ParamKind int

const (
	// Flag: --name value, or --name alone for a boolean that is true.
	Flag ParamKind = iota
	// Positional: a word before the flags.
	Positional
	// Text: free text after a bare "--", so it may begin with a dash.
	Text
)

// Param is one argument of a tool.
type Param struct {
	Name     string
	Type     string // one of the type constants; an array holds strings
	Desc     string
	Required bool
	Enum     []string
	Kind     ParamKind
	// Flag is the flag a Flag param sets, without dashes; empty means Name
	// with its underscores as dashes.
	Flag string
}

func (p Param) flag() string {
	if p.Flag != "" {
		return p.Flag
	}
	return strings.ReplaceAll(p.Name, "_", "-")
}

// Args are the arguments of a tool call after they have been checked against
// the tool Params: a string, bool, int64 or []string by the param type. A
// param the caller left out, or left blank, is not in the map.
type Args map[string]any

// Str returns the string argument name, "" if there is none.
func (a Args) Str(name string) string {
	s, _ := a[name].(string)
	return s
}

// Tool is one MCP tool: a jig command with the arguments a model may give it.
type Tool struct {
	Name        string
	Description string
	// Verb is the command the tool runs.
	Verb   string
	Params []Param
	// ReadOnly marks a tool that changes nothing the person keeps (the
	// bookkeeping of jig itself aside), which a client may run without asking.
	ReadOnly bool
	// Handle runs the tool. Tools builds the default, which runs Verb once
	// with the arguments.
	Handle func(ctx context.Context, c Client, a Args) (Result, error)
}

// describe is the tool as tools/list lists it.
func (t Tool) describe() map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range t.Params {
		prop := map[string]any{"type": p.Type, "description": p.Desc}
		if p.Type == typeArray {
			prop["items"] = map[string]any{"type": typeString}
		}
		if len(p.Enum) > 0 {
			prop["enum"] = p.Enum
		}
		props[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return map[string]any{
		"name":        t.Name,
		"description": t.Description,
		"inputSchema": schema,
		"annotations": map[string]any{
			"readOnlyHint":    t.ReadOnly,
			"destructiveHint": false,
			"openWorldHint":   false,
		},
	}
}

func (t Tool) param(name string) (Param, bool) {
	for _, p := range t.Params {
		if p.Name == name {
			return p, true
		}
	}
	return Param{}, false
}

// decode checks the arguments of a call against the params and returns them.
// Its errors say what to change, because the model reads them.
func (t Tool) decode(raw json.RawMessage) (Args, error) {
	args := Args{}
	var given map[string]json.RawMessage
	if s := strings.TrimSpace(string(raw)); s != "" && s != "null" {
		if err := json.Unmarshal(raw, &given); err != nil {
			return nil, fmt.Errorf("the arguments of %s must be a JSON object", t.Name)
		}
	}
	names := make([]string, 0, len(given))
	for name := range given {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := given[name]
		p, ok := t.param(name)
		if !ok {
			return nil, fmt.Errorf("%s has no argument %q; its arguments are %s", t.Name, name, t.paramNames())
		}
		if string(v) == "null" {
			continue
		}
		val, err := decodeValue(p, v)
		if err != nil {
			return nil, fmt.Errorf("argument %q of %s: %v", name, t.Name, err)
		}
		if val != nil {
			args[name] = val
		}
	}
	for _, p := range t.Params {
		if _, ok := args[p.Name]; p.Required && !ok {
			return nil, fmt.Errorf("%s needs the argument %q: %s", t.Name, p.Name, p.Desc)
		}
	}
	return args, nil
}

// decodeValue reads one argument as its param type. A blank string and an
// empty array count as not given (nil).
func decodeValue(p Param, raw json.RawMessage) (any, error) {
	switch p.Type {
	case typeString:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("must be a string")
		}
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		if len(p.Enum) > 0 && !contains(p.Enum, s) {
			return nil, fmt.Errorf("must be one of %s", strings.Join(p.Enum, ", "))
		}
		return s, nil
	case typeBoolean:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("must be true or false")
		}
		return b, nil
	case typeInteger:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("must be an integer")
		}
		return n, nil
	case typeArray:
		var a []string
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("must be an array of strings")
		}
		if len(a) == 0 {
			return nil, nil
		}
		return a, nil
	}
	return nil, fmt.Errorf("has the unknown type %q", p.Type)
}

func (t Tool) paramNames() string {
	names := make([]string, len(t.Params))
	for i, p := range t.Params {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// invocation is one run of a command, in the order every jig command parses
// its line: positionals, then flags, then a bare "--" and free text.
type invocation struct {
	verb  string
	pos   []string
	flags []string
	text  []string
}

// invocation turns checked arguments into a command line.
func (t Tool) invocation(a Args) (invocation, error) {
	inv := invocation{verb: t.Verb}
	for _, p := range t.Params {
		v, ok := a[p.Name]
		if !ok {
			continue
		}
		switch p.Kind {
		case Positional:
			s := v.(string)
			if strings.HasPrefix(s, "-") {
				return invocation{}, fmt.Errorf("argument %q of %s must not begin with a dash", p.Name, t.Name)
			}
			inv.pos = append(inv.pos, s)
		case Text:
			inv.text = append(inv.text, v.(string))
		default:
			flag := "--" + p.flag()
			switch x := v.(type) {
			case string:
				inv.flags = append(inv.flags, flag, x)
			case bool:
				if x {
					inv.flags = append(inv.flags, flag)
				}
			case int64:
				inv.flags = append(inv.flags, flag, strconv.FormatInt(x, 10))
			case []string:
				inv.flags = append(inv.flags, flag, strings.Join(x, ","))
			}
		}
	}
	return inv, nil
}

// toolkit runs invocations. Commands run one at a time: they are written for
// separate processes, whose locks do not keep two goroutines of one process
// apart.
type toolkit struct {
	run Runner
	pin func(verb string) ([]string, error)
	mu  sync.Mutex
}

// exec runs inv and returns what it printed and its exit code.
func (tk *toolkit) exec(ctx context.Context, inv invocation, stdin string) (string, int, error) {
	var pins []string
	if tk.pin != nil {
		var err error
		if pins, err = tk.pin(inv.verb); err != nil {
			return "", 0, err
		}
	}
	argv := append(strings.Fields(inv.verb), inv.pos...)
	argv = append(argv, inv.flags...)
	argv = append(argv, pins...)
	if len(inv.text) > 0 {
		argv = append(argv, "--")
		argv = append(argv, inv.text...)
	}
	tk.mu.Lock()
	defer tk.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", 0, err // cancelled while it waited its turn: do not start it
	}
	out, code := tk.run(ctx, argv, stdin)
	return out, code, nil
}

// runVerb is the handler of a tool that runs its command once.
func (tk *toolkit) runVerb(t Tool) func(context.Context, Client, Args) (Result, error) {
	return func(ctx context.Context, _ Client, a Args) (Result, error) {
		inv, err := t.invocation(a)
		if err != nil {
			return Result{}, err
		}
		out, code, err := tk.exec(ctx, inv, "")
		if err != nil {
			return Result{}, err
		}
		return Result{Text: orNothing(out), IsError: code != 0}, nil
	}
}

// orNothing says so when a command printed nothing, which an empty text
// would not.
func orNothing(out string) string {
	if strings.TrimSpace(out) == "" {
		return "(no output)"
	}
	return out
}

// Shared params. Each tool takes the ones its command has a flag for; the
// parity test of cmd/jig holds them to the command flags.
var (
	ticketParam = Param{Name: "ticket", Type: typeString, Kind: Positional,
		Desc: "ticket id such as JIG-1 (default: the ticket the branch of the working directory is on)"}
	sliceParam = Param{Name: "slice", Type: typeString,
		Desc: "slice the work is on (default: JIG_SLICE, else the session's latest claim)"}
	sessionParam = Param{Name: "session", Type: typeString,
		Desc: "harness session id (default: JIG_SESSION)"}
	jsonParam = Param{Name: "json", Type: typeBoolean,
		Desc: "return one JSON object instead of text blocks"}
)

// Tools returns the tools the server offers, in the order they are listed:
// here, next, context, claim, ask, answer, note, observe and status. Each
// runs the command of its name through o.Run; ask also puts its question to
// the person when the client can take one.
func Tools(o Options) []Tool {
	tk := &toolkit{run: o.Run, pin: o.Pin}
	required := func(p Param) Param { p.Required = true; return p }
	tools := []Tool{
		{
			Name:        "here",
			Description: "Name where the work is: the project, store, repo, branch, ticket and slices of the working directory. Prints nothing outside a jig project.",
			Verb:        "here", ReadOnly: true,
			Params: []Param{ticketParam, sliceParam, sessionParam,
				{Name: "explain", Type: typeBoolean, Desc: "also say how each part was found"}, jsonParam},
		},
		{
			Name:        "next",
			Description: "Name what is ready to build. With a ticket: its ready slices by repo and the branch to create. Without one: the tickets of the store in order, what blocks each and which are ready.",
			Verb:        "next", ReadOnly: true,
			Params: []Param{ticketParam,
				{Name: "topology", Type: typeBoolean, Desc: "with a ticket: also list every slice with its repo, wave and blockers"}, jsonParam},
		},
		{
			Name:        "context",
			Description: "Print what a slice needs to start: the brief sections it is cut from, the earlier slices and their files, the oracle, failed attempts, questions with their answers, and notes.",
			Verb:        "context", ReadOnly: true,
			Params: []Param{ticketParam, sliceParam, jsonParam},
		},
		{
			Name:        "claim",
			Description: "Claim a slice before building it, or let it go with release. The ticket must be ready (blockers merged, stacked parent clean, brief approved): an unattended claim is refused when it is not, an attended one is warned.",
			Verb:        "claim",
			Params: []Param{ticketParam, sliceParam, sessionParam,
				{Name: "unattended", Type: typeBoolean, Desc: "no person is watching: a condition the claim does not meet refuses it instead of warning"},
				{Name: "take", Type: typeBoolean, Desc: "take the slice over from another session that holds it"},
				{Name: "release", Type: typeBoolean, Desc: "let the slice go: queued again unless green"}},
		},
		{
			Name:        "ask",
			Description: "Ask the person a question about a slice; the slice waits on it until answered. The person is asked in the client when it can ask them, and the answer comes back in the result. Otherwise the question is recorded for the person to answer, and the answer shows under questions in context.",
			Verb:        "ask",
			Params: []Param{ticketParam, sliceParam, sessionParam,
				required(Param{Name: "question", Type: typeString, Kind: Text, Desc: "the question for the person"})},
		},
		{
			Name:        "answer",
			Description: "Answer an open question; the slice it parked goes back to work. The answer is recorded as an agent's, not the person's.",
			Verb:        "answer",
			Params: []Param{ticketParam,
				required(Param{Name: "question_id", Type: typeString, Kind: Positional, Desc: "id of the question, such as q-001"}),
				required(Param{Name: "text", Type: typeString, Kind: Text, Desc: "the answer"})},
		},
		{
			Name:        "note",
			Description: "Leave a note, at most 2000 bytes, on a slice (or on the ticket when no slice is named or claimed) for whoever builds it next.",
			Verb:        "note",
			Params: []Param{ticketParam, sliceParam, sessionParam,
				required(Param{Name: "text", Type: typeString, Kind: Text, Desc: "the note"})},
		},
		{
			Name:        "observe",
			Description: "Report what the session is doing to jig: a session starting or ending, a prompt, a tool call, a stop, or a commit. For a harness whose hooks are not installed. Returns what jig has to say back as JSON with context, message and block, or nothing.",
			Verb:        "observe",
			Params: []Param{
				required(Param{Name: "event", Type: typeString, Enum: observe.Kinds(), Desc: "what happened"}),
				{Name: "session", Type: typeString, Desc: "harness session id"},
				{Name: "agent", Type: typeString, Desc: "id of the agent speaking, when it is not the session's own"},
				{Name: "cwd", Type: typeString, Desc: "directory the event happened in (default: the working directory)"},
				{Name: "tool", Type: typeString, Desc: "name of the tool a tool event is about"},
				{Name: "command", Type: typeString, Desc: "shell command a tool event ran"},
				{Name: "exit", Type: typeInteger, Desc: "exit status of that command"},
				{Name: "files", Type: typeArray, Desc: "files a tool event touched"},
				{Name: "model", Type: typeString, Desc: "model the session runs"},
				{Name: "transcript", Type: typeString, Desc: "path of the harness transcript of the session"},
				{Name: "stop_active", Type: typeBoolean, Desc: "this stop follows an earlier refusal to stop"},
			},
		},
		{
			Name:        "status",
			Description: "Print the state of a ticket: its slices, its questions, what is parked on a person and what is stalled.",
			Verb:        "status", ReadOnly: true,
			Params: []Param{required(ticketParam)},
		},
	}
	for i := range tools {
		switch tools[i].Name {
		case "ask":
			tools[i].Handle = tk.ask(tools[i])
		default:
			tools[i].Handle = tk.runVerb(tools[i])
		}
	}
	return tools
}
