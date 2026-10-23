package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/observe"
)

// The usage lines of `jig observe`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	observeHarnessUsage    = "harness whose hook payload arrives on stdin (claude, codex, gemini, cursor, copilot, opencode)"
	observeEventUsage      = "event kind (session-start, prompt, tool, stop, session-end, post-commit); with no --harness the event is built from these flags alone"
	observeSessionUsage    = "harness session id"
	observeAgentUsage      = "id of the agent speaking, when it is not the session's own"
	observeCwdUsage        = "directory the event happened in (default: the working directory)"
	observeToolUsage       = "name of the tool a tool event is about"
	observeCommandUsage    = "shell command a tool event ran"
	observeExitUsage       = "exit status of that command"
	observeFilesUsage      = "files a tool event touched, comma separated"
	observeModelUsage      = "model the session runs"
	observeTranscriptUsage = "path of the harness transcript of the session"
	observeStopActiveUsage = "this stop follows an earlier refusal to stop"
)

// maxHookPayload bounds how much of a hook payload is read: the output of a
// tool call can be large, and a payload that is cut parses as garbage and is
// ignored.
const maxHookPayload = 64 << 20

// init registers this file's verb with the registry (registry.go). It is
// hidden: the hooks that `jig hooks` installs run it, and a person has no
// reason to.
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"observe", "run by the hooks of a harness: record what the session does and answer it", []flagSpec{
			{"harness", observeHarnessUsage, false},
			{"event", observeEventUsage, false},
			{"session", observeSessionUsage, false},
			{"agent", observeAgentUsage, false},
			{"cwd", observeCwdUsage, false},
			{"tool", observeToolUsage, false},
			{"command", observeCommandUsage, false},
			{"exit", observeExitUsage, false},
			{"files", observeFilesUsage, false},
			{"model", observeModelUsage, false},
			{"transcript", observeTranscriptUsage, false},
			{"stop-active", observeStopActiveUsage, false},
		}, true},
		Order: 900,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdObserve(e, args, stdout, stdin)
		},
	})
}

// cmdObserve implements `jig observe --harness <h>` (the hook payload of
// harness h on stdin) and `jig observe --event <kind> [--session ...]` (the
// event from flags alone, for a script or a crew with no hooks). It does
// what jig does for the event and prints what the harness reads, in the
// harness's shape.
//
// A hook must never fail the harness: whatever goes wrong - a payload that is
// no payload, a flag that names nothing, a store that will not open, a panic -
// it exits 0 and prints nothing, since a harness may take the output as
// context or the exit status as a refusal. What went wrong goes to the log
// under the jig home (logs/observe.log), and, when a person is at a terminal
// to read it, to the screen.
func cmdObserve(e env, args []string, stdout io.Writer, stdin io.Reader) (code int) {
	jigHome, homeErr := e.jigHome()
	what := "observe"
	fail := func(err error) int {
		logObserve(e, jigHome, what, err)
		if e.stdinIsTerminal(stdin) {
			return renderErr(stdout, err)
		}
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			code = fail(fmt.Errorf("observe: panic: %v", r))
		}
	}()

	fs := newFlagSet(e, "observe")
	var f eventFlags
	var harness string
	fs.StringVar(&harness, "harness", "", observeHarnessUsage)
	fs.StringVar(&f.kind, "event", "", observeEventUsage)
	fs.StringVar(&f.session, "session", "", observeSessionUsage)
	fs.StringVar(&f.agent, "agent", "", observeAgentUsage)
	fs.StringVar(&f.cwd, "cwd", "", observeCwdUsage)
	fs.StringVar(&f.tool, "tool", "", observeToolUsage)
	fs.StringVar(&f.command, "command", "", observeCommandUsage)
	fs.StringVar(&f.exit, "exit", "", observeExitUsage)
	fs.StringVar(&f.files, "files", "", observeFilesUsage)
	fs.StringVar(&f.model, "model", "", observeModelUsage)
	fs.StringVar(&f.transcript, "transcript", "", observeTranscriptUsage)
	fs.BoolVar(&f.stopActive, "stop-active", false, observeStopActiveUsage)
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return fail(err)
	}
	if fs.NArg() > 0 {
		return fail(fmt.Errorf("observe: unexpected argument %q", fs.Arg(0)))
	}

	if homeErr != nil {
		return fail(homeErr)
	}
	a, havePayload, err := observeAdapter(harness, f.kind)
	if err != nil {
		return fail(err)
	}
	what = a.Name()

	var ev observe.Event
	ev.Harness = a.Name()
	if havePayload {
		if e.stdinIsTerminal(stdin) {
			return fail(fmt.Errorf("observe: --harness %s reads the hook payload on stdin, and stdin is a terminal", a.Name()))
		}
		data, err := io.ReadAll(io.LimitReader(stdin, maxHookPayload))
		if err != nil {
			return fail(err)
		}
		if ev, err = a.Parse(data); err != nil {
			return fail(err)
		}
	}
	if ev.Harness == "" {
		ev.Harness = a.Name()
	}
	if err := overlayEventFlags(&ev, f); err != nil {
		return fail(err)
	}
	if ev.Kind == "" {
		return 0 // an event of the harness that jig does not act on
	}
	if ev.Cwd, err = e.abs(ev.Cwd); err != nil {
		return fail(err)
	}
	what = a.Name() + " " + ev.Kind

	out, err := observe.RunEvent(observeDeps(e, jigHome), a, ev)
	if len(out) > 0 {
		stdout.Write(out)
	}
	if err != nil {
		return fail(err)
	}
	return 0
}
