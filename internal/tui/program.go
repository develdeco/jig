package tui

import (
	"context"
	"errors"
	"fmt"
	"io"

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

	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(programInput(in)), tea.WithOutput(out)}
	var model tea.Model = m
	if screen {
		opts = append(opts, tea.WithAltScreen())
	} else {
		opts = append(opts, tea.WithoutRenderer())
		model = &framed{inner: m, out: out}
	}
	_, err := tea.NewProgram(model, opts...).Run()
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
