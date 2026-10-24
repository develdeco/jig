package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
)

// The `jig session` verbs keep the machine ledger's session records (see
// internal/ledger/sessions.go): start registers a harness session, touch
// says it is still working, end closes it, list reads them. Each write is a
// ledger line and nothing else, so a session started by one process is listed
// by another. The hook verb (`jig observe`) writes the same lines for a
// harness that fires hooks; these are for a crew that does not, a person, and
// a session that wants an id of its own.

// The flag usage the verbs share. A verb's help row and the flag it registers
// must say the same (TestCommandTableFlagsMatchRegistration), so both read
// these.
const (
	sessionUsageStart  = "session id (default $JIG_SESSION, else a new one is minted)"
	sessionUsageOthers = "session id (default $JIG_SESSION)"
	sessionUsageCwd    = "directory the session works in (default the working directory)"
	sessionUsageAgent  = "id of the agent starting it, when it is not the session's own"
)

// init registers this file's verbs with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"session start", "register a harness session in the machine ledger", []flagSpec{
			{"session", sessionUsageStart, false},
			{"harness", "harness name (claude, codex, ...)", false},
			{"agent", sessionUsageAgent, false},
			{"model", "model the session runs", false},
			{"effort", "reasoning effort the session runs at", false},
			{"cwd", sessionUsageCwd, false},
		}, false},
		Order: 340,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSessionStart(e, args, stdout)
		},
	})
	registerVerb(verb{
		Spec: cmdSpec{"session touch", "mark a session as still working", []flagSpec{
			{"session", sessionUsageOthers, false},
		}, false},
		Order: 341,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSessionTouch(e, args, stdout)
		},
	})
	registerVerb(verb{
		Spec: cmdSpec{"session end", "mark a session as ended", []flagSpec{
			{"session", sessionUsageOthers, false},
		}, false},
		Order: 342,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSessionEnd(e, args, stdout)
		},
	})
	registerVerb(verb{
		Spec: cmdSpec{"session list", "list the harness sessions on this machine", []flagSpec{
			{"all", "include sessions that have ended", false},
			{"json", "print the sessions as a JSON array", false},
		}, false},
		Order: 343,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSessionList(e, args, stdout)
		},
	})
}

// cmdSessionStart implements `jig session start [--session <id>] [--harness
// <h>] [--agent <id>] [--model <m>] [--effort <e>] [--cwd <dir>]`: one
// session-start line. The id is the flag, else $JIG_SESSION, else a minted
// one; starting an id that is in the ledger already resumes it.
func cmdSessionStart(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "session start")
	sessionFlag := fs.String("session", "", sessionUsageStart)
	harness := fs.String("harness", "", "harness name (claude, codex, ...)")
	agent := fs.String("agent", "", sessionUsageAgent)
	model := fs.String("model", "", "model the session runs")
	effort := fs.String("effort", "", "reasoning effort the session runs at")
	cwdFlag := fs.String("cwd", "", sessionUsageCwd)
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if err := sessionNoArgs(fs); err != nil {
		return renderErr(stdout, err)
	}

	root, err := e.jigHome()
	if err != nil {
		return renderErr(stdout, err)
	}
	var cwd string
	if *cwdFlag != "" {
		cwd, err = e.abs(*cwdFlag)
	} else {
		cwd, err = e.getwd()
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	id := sessionVerbArg(e, *sessionFlag)
	if id == "" {
		id = ledger.MintID()
	}

	at := e.now()
	if err := ledger.StartSession(root, at, ledger.Registration{
		ID:      id,
		Harness: *harness,
		Agent:   *agent,
		Cwd:     cwd,
		Model:   *model,
		Effort:  *effort,
	}); err != nil {
		return renderErr(stdout, err)
	}

	pairs := [][2]string{{"id", id}}
	for _, kv := range [][2]string{{"harness", *harness}, {"agent", *agent}, {"model", *model}, {"effort", *effort}, {"cwd", cwd}} {
		if kv[1] != "" {
			pairs = append(pairs, kv)
		}
	}
	pairs = append(pairs, [2]string{"started", ledger.Stamp(at)})
	axi.Render(stdout,
		axi.KV("session", pairs),
		axi.Help(fmt.Sprintf("Pass `--session %s` to the jig commands this session runs (or set JIG_SESSION), and run `jig session end --session %s` when it is over", id, id)),
	)
	return 0
}

// cmdSessionTouch implements `jig session touch [--session <id>]`: one
// session-touch line for a session that is in the ledger and has not ended.
func cmdSessionTouch(e env, args []string, stdout io.Writer) int {
	t, code, ok := sessionVerbTarget(e, "session touch", args, stdout)
	if !ok {
		return code
	}
	if !t.session.Live() {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("session %s ended at %s", t.id, t.session.Ended),
			Code: "SESSION_ENDED",
			Help: []string{fmt.Sprintf("Run `jig session start --session %s` to resume it", t.id)},
		})
	}
	at := e.now()
	if err := ledger.TouchSession(t.root, at, ledger.Registration{ID: t.id}); err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("session", [][2]string{{"id", t.id}, {"last_seen", ledger.Stamp(at)}}),
		axi.Help("Run `jig session list` to see the sessions on this machine"),
	)
	return 0
}

// cmdSessionEnd implements `jig session end [--session <id>]`: one
// session-end line for a session that is in the ledger. Ending a session that
// has ended already writes nothing and says when it ended.
func cmdSessionEnd(e env, args []string, stdout io.Writer) int {
	t, code, ok := sessionVerbTarget(e, "session end", args, stdout)
	if !ok {
		return code
	}
	ended := t.session.Ended
	if t.session.Live() {
		at := e.now()
		if err := ledger.EndSession(t.root, at, t.id); err != nil {
			return renderErr(stdout, err)
		}
		ended = ledger.Stamp(at)
	}
	axi.Render(stdout,
		axi.KV("session", [][2]string{{"id", t.id}, {"ended", ended}}),
		axi.Help("Run `jig session list --all` to see the sessions on this machine, ended ones too"),
	)
	return 0
}

// sessionTarget is the session a touch or end acts on: the jig home root it
// was found under, its id, and what the ledger records of it.
type sessionTarget struct {
	root    string
	id      string
	session ledger.Session
}

// sessionVerbTarget is the start of touch and end. It parses the verb's one
// flag, takes the session id from it or from $JIG_SESSION, and looks the
// session up in the ledger. ok false means the verb is done and code is its
// exit code: help printed, or the error rendered.
func sessionVerbTarget(e env, name string, args []string, stdout io.Writer) (t sessionTarget, code int, ok bool) {
	fs := newFlagSet(e, name)
	sessionFlag := fs.String("session", "", sessionUsageOthers)
	if handled, err := parseFlags(stdout, fs, args); handled {
		return t, 0, false
	} else if err != nil {
		return t, renderErr(stdout, err), false
	}
	if err := sessionNoArgs(fs); err != nil {
		return t, renderErr(stdout, err), false
	}
	t.id = sessionVerbArg(e, *sessionFlag)
	if t.id == "" {
		return t, renderErr(stdout, &axi.Error{
			Msg:  "missing required session id",
			Code: "VALIDATION_ERROR",
			Help: []string{"Pass --session <id>, or set JIG_SESSION"},
		}), false
	}
	var err error
	if t.root, err = e.jigHome(); err != nil {
		return t, renderErr(stdout, err), false
	}
	sessions, err := ledger.Sessions(t.root)
	if err != nil {
		return t, renderErr(stdout, err), false
	}
	s, known := sessions[t.id]
	if !known {
		return t, renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("no session %s in the ledger", t.id),
			Code: "SESSION_NOT_FOUND",
			Help: []string{
				"Run `jig session list --all` to see the sessions on this machine",
				fmt.Sprintf("Run `jig session start --session %s` to register it", t.id),
			},
		}), false
	}
	t.session = s
	return t, 0, true
}

// cmdSessionList implements `jig session list [--all] [--json]`: the
// sessions in the ledger that have not ended (with --all, every one), the
// most recently seen first.
func cmdSessionList(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "session list")
	all := fs.Bool("all", false, "include sessions that have ended")
	asJSON := fs.Bool("json", false, "print the sessions as a JSON array")
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if err := sessionNoArgs(fs); err != nil {
		return renderErr(stdout, err)
	}

	root, err := e.jigHome()
	if err != nil {
		return renderErr(stdout, err)
	}
	sessions, err := ledger.Sessions(root)
	if err != nil {
		return renderErr(stdout, err)
	}
	shown := []ledger.Session{}
	for _, s := range ledger.Recent(sessions) {
		if *all || s.Live() {
			shown = append(shown, s)
		}
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(shown); err != nil {
			return renderErr(stdout, err)
		}
		return 0
	}

	rows := make([][]string, len(shown))
	for i, s := range shown {
		rows[i] = []string{s.ID, s.Harness, s.Ticket, s.Slice, s.LastSeen, s.Ended, s.Cwd}
	}
	next := "Run `jig session start` to register one"
	if len(shown) > 0 {
		next = fmt.Sprintf("Run `jig session end --session %s` to close one", shown[0].ID)
	}
	axi.Render(stdout,
		axi.Table("sessions", []string{"id", "harness", "ticket", "slice", "last_seen", "ended", "cwd"}, rows),
		axi.Help(next),
	)
	return 0
}

// sessionVerbArg is the session a `jig session` verb acts on: the --session
// flag, else the session the environment names (envSession), else "".
func sessionVerbArg(e env, flagValue string) string {
	if s := strings.TrimSpace(flagValue); s != "" {
		return s
	}
	return envSession(e)
}

// envSession is the session the environment names: $JIG_SESSION, else the id
// the harness sets in its agent's commands (locate.HarnessSessionEnv), else
// "". It is how locate reads it, for the verbs that take a session without
// locating a directory.
func envSession(e env) string {
	if s := strings.TrimSpace(e.getenv(locate.SessionEnv)); s != "" {
		return s
	}
	for _, name := range locate.HarnessSessionEnv {
		if s := strings.TrimSpace(e.getenv(name)); s != "" {
			return s
		}
	}
	return ""
}

// sessionNoArgs refuses a positional argument after the flags: these verbs
// take none, and `jig session end s1` would otherwise end the session
// $JIG_SESSION names, or none.
func sessionNoArgs(fs *flag.FlagSet) error {
	if fs.NArg() == 0 {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("unexpected argument %q: jig %s takes flags only", fs.Arg(0), fs.Name()),
		Code: "VALIDATION_ERROR",
		Help: []string{fmt.Sprintf("Run `jig %s -h` to see them; the session id is --session <id>", fs.Name())},
	}
}
