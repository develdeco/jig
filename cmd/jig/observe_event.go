package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/observe"
)

// observeAdapter picks the adapter of a call: the named harness's, which
// reads the payload on stdin; with only an event given, the neutral one, which
// reads nothing. havePayload says whether stdin carries a payload.
func observeAdapter(harness, kind string) (a observe.Adapter, havePayload bool, err error) {
	switch {
	case harness == "" && kind == "":
		return nil, false, fmt.Errorf("observe: say which harness sent the payload (--harness %s), or give the event (--event)", strings.Join(observe.Harnesses(), "|"))
	case harness == "":
		a, _ = observe.AdapterFor("neutral")
		return a, false, nil
	}
	a, ok := observe.AdapterFor(harness)
	if !ok {
		return nil, false, fmt.Errorf("observe: no adapter for harness %q (they are %s)", harness, strings.Join(observe.Harnesses(), ", "))
	}
	return a, true, nil
}

// eventFlags are the fields of an event as flags carry them.
type eventFlags struct {
	kind, session, agent, cwd, tool, command, exit, files, model, transcript string
	stopActive                                                               bool
}

// overlayEventFlags lays the flags that were given over the event parsed from
// the payload: a flag says what the call means, whatever the payload says.
func overlayEventFlags(ev *observe.Event, f eventFlags) error {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	if f.kind != "" {
		if !observe.ValidKind(f.kind) {
			return fmt.Errorf("observe: %q is not a kind of event (they are %s)", f.kind, strings.Join(observe.Kinds(), ", "))
		}
		ev.Kind = f.kind
	}
	set(&ev.Session, f.session)
	set(&ev.Agent, f.agent)
	set(&ev.Cwd, f.cwd)
	set(&ev.Tool, f.tool)
	set(&ev.Command, f.command)
	set(&ev.Model, f.model)
	set(&ev.Transcript, f.transcript)
	if f.exit != "" {
		n, err := strconv.Atoi(f.exit)
		if err != nil {
			return fmt.Errorf("observe: --exit %q is not a number", f.exit)
		}
		ev.Exit = &n
	}
	if f.files != "" {
		ev.Files = nil
		for _, p := range strings.Split(f.files, ",") {
			if p = strings.TrimSpace(p); p != "" {
				ev.Files = append(ev.Files, p)
			}
		}
	}
	if f.stopActive {
		ev.StopActive = true
	}
	return nil
}
