package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// Run runs the screens on in and out until the person quits, ctx ends or the
// program fails. Ending by quitting or by ctx is not an error.
//
// With screen set the screens take over the terminal (the alternate screen,
// raw keys, colors the terminal supports). Without it, for a pipe or a file or
// a test, nothing is drawn over: each screen is printed whole, one after the
// other, each followed by a line holding a form feed, and keys are read from in as
// they come, so a script can drive the screens and read what they showed.
func Run(ctx context.Context, cfg Config, in io.Reader, out io.Writer, screen bool) error {
	if cfg.Context == nil {
		cfg.Context = ctx
	}
	if cfg.Renderer == nil {
		cfg.Renderer = lipgloss.NewRenderer(out)
	}
	m := New(cfg)
	defer m.Stop()

	opts := []tea.ProgramOption{tea.WithInput(programInput(in)), tea.WithOutput(out)}
	var model tea.Model = m
	if screen {
		opts = append(opts, tea.WithAltScreen())
		// What anything in the process writes to stderr meanwhile (a verb's
		// warning a seam does not reach) waits until the screens give the
		// terminal back: a line written under them scrolls it, and every
		// later frame is drawn a row off.
		defer hold(&os.Stderr)()
	} else {
		opts = append(opts, tea.WithoutRenderer())
		model = &framed{inner: m, out: out}
	}
	prog := tea.NewProgram(model, opts...)
	// The context ends the program through the program's own Quit, the way a
	// q does, and not through tea.WithContext: bubbletea v1.3.4 stops handling
	// commands the moment that context ends, and an Update in flight then
	// blocks for ever handing its command over (a hang a loaded machine shows
	// about once in two thousand runs). A Quit is taken in turn by the event
	// loop, so nothing is left waiting.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			prog.Quit()
		case <-stop:
		}
	}()
	_, err := prog.Run()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, tea.ErrInterrupted), errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil:
		return nil
	}
	return err
}

// programInput is the reader the keys come from. A terminal goes to the
// program as it is, so it can put it in raw mode (and, on Windows, read the
// console's own events); anything else is hidden behind a plain reader, or the
// program would open the terminal for its keys and ignore it.
func programInput(in io.Reader) io.Reader {
	if f, ok := in.(term.File); ok && term.IsTerminal(f.Fd()) {
		return in
	}
	return plainReader{in}
}

// plainReader is a reader and nothing more.
type plainReader struct{ r io.Reader }

func (p plainReader) Read(b []byte) (int, error) { return p.r.Read(b) }

// framed prints the screen whole each time it changes, in place of the
// renderer that would redraw it with cursor movements a pipe cannot show.
type framed struct {
	inner tea.Model
	out   io.Writer
	last  string
}

func (f *framed) Init() tea.Cmd {
	f.emit()
	return f.inner.Init()
}

func (f *framed) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	f.inner, cmd = f.inner.Update(msg)
	f.emit()
	return f, cmd
}

func (f *framed) View() string { return "" }

// emit prints the screen when it is not the one printed last.
func (f *framed) emit() {
	v := f.inner.View()
	if v == f.last {
		return
	}
	f.last = v
	fmt.Fprint(f.out, v+"\n\f\n")
}

// hold points *f at a pipe for the rest of the process and returns a
// release: what is written there until then is kept, and the release writes
// it to the original file and from then on passes every write through. *f is
// set once, before anything else runs, and the pipe never closes, so a
// goroutine the screens did not wait for (a command still running after q, a
// panic's stack) still reaches the original. The release waits, at most a
// second, for what was written before it to come out of the pipe.
func hold(f **os.File) (release func()) {
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	orig := *f
	*f = w
	var (
		mu       sync.Mutex
		held     bytes.Buffer
		released bool
	)
	drained := make(chan struct{}, 1)
	go func() {
		var tail []byte // the start of a marker split across two reads
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			mu.Lock()
			if released {
				// A marker the release stopped waiting for still comes out
				// of the pipe: it is not the original's to print.
				out := bytes.ReplaceAll(append(tail, buf[:n]...), holdMark, nil)
				tail = nil
				if k := partialMark(out); k > 0 {
					out, tail = out[:len(out)-k], bytes.Clone(out[len(out)-k:])
				}
				_, _ = orig.Write(out)
			} else {
				held.Write(buf[:n])
				if i := bytes.Index(held.Bytes(), holdMark); i >= 0 {
					rest := bytes.Clone(held.Bytes()[i+len(holdMark):])
					held.Truncate(i)
					held.Write(rest)
					drained <- struct{}{}
				}
			}
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return func() {
		if _, err := w.Write(holdMark); err == nil {
			select {
			case <-drained:
			case <-time.After(time.Second):
			}
		}
		mu.Lock()
		defer mu.Unlock()
		released = true
		_, _ = orig.Write(held.Bytes())
		held.Reset()
	}
}

// partialMark is how many bytes at the end of b are the start of holdMark.
func partialMark(b []byte) int {
	for k := min(len(b), len(holdMark)-1); k > 0; k-- {
		if bytes.Equal(b[len(b)-k:], holdMark[:k]) {
			return k
		}
	}
	return 0
}

// holdMark is what a release writes to the held pipe to know when what was
// written before it has come out.
var holdMark = []byte("\x00jig: release the held output\x00")
