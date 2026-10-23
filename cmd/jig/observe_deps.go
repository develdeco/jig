package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/store"
)

// observeDeps wires the observe core to the env of this run: the clock, the
// environment, the way a detached process is started, and the two things only
// cmd/jig knows, how a store is opened and what `jig here` says.
func observeDeps(e env, jigHome string) observe.Deps {
	d := observe.Deps{
		JigHome:   jigHome,
		Now:       e.now,
		Getenv:    e.getenv,
		Start:     e.spawn,
		OpenStore: openLocatedStore,
		Here: func(w locate.Where, st *store.Store) (string, error) {
			return hereText(e, w, jigHome, st)
		},
	}
	if cwd, err := e.getwd(); err == nil {
		d.Cwd = cwd
	}
	if exe, err := e.executable(); err == nil {
		d.Executable = exe
	}
	return d
}

// hereText is what `jig here` prints for an already located directory: the
// text a session is given as it starts. st is the store Locate named, nil when
// it is not on this machine.
func hereText(e env, w locate.Where, jigHome string, st *store.Store) (string, error) {
	hc := hereCtx{e: e, Where: w, Store: st, JigHome: jigHome}
	view, err := buildHereView(hc)
	if err != nil {
		return "", err
	}
	blocks, err := view.blocks(hc, hereSections())
	if err != nil {
		return "", err
	}
	blocks = append(blocks, axi.Help(view.help(hc)...))
	var buf bytes.Buffer
	axi.Render(&buf, blocks...)
	return buf.String(), nil
}

// observeLogCap is how large the observe log may grow before it is started
// over: it holds only what went wrong, one line each.
const observeLogCap = 512 << 10

// logObserve appends one line about a failed hook call to the log under the
// jig home, and starts the log over (keeping the old one beside it) when it
// has grown past observeLogCap. It is best effort: a hook that cannot write
// its log has nowhere left to complain to.
func logObserve(e env, jigHome, what string, err error) {
	if jigHome == "" || err == nil {
		return
	}
	dir := home.LogsDir(jigHome)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	path := filepath.Join(dir, "observe.log")
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > observeLogCap {
		_ = os.Rename(path, path+".1")
	}
	f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if openErr != nil {
		return
	}
	defer f.Close()
	msg := strings.Join(strings.Fields(err.Error()), " ")
	fmt.Fprintf(f, "%s %s: %s\n", e.now().UTC().Format(time.RFC3339), what, msg)
}
