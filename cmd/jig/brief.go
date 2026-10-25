package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/briefview"
	"github.com/develdeco/jig/internal/store"
)

// The usage lines of `jig brief show`: the help row and the FlagSet carry the
// same text, which the help parity test compares.
const (
	briefShowPortUsage    = "port the viewer listens on, on 127.0.0.1; 0 picks a free one"
	briefShowPathUsage    = "print the absolute paths of brief.md and illustration.html instead of serving them"
	briefShowTextUsage    = "print the brief's sections as plain text instead of serving them"
	briefShowStoreUsage   = "explicit store path"
	briefShowProjectUsage = "project name, resolved via the machine mapping"
)

const (
	// briefViewerPort is the port the viewer listens on unless --port says
	// otherwise.
	briefViewerPort = 4719
	// remoteEnv is set by a harness whose session runs in a VM or a cloud
	// container, where a browser cannot reach the session's 127.0.0.1.
	remoteEnv = "CLAUDE_CODE_REMOTE"
	// briefViewerGrace is how long the viewer gives a request in flight, once
	// it is told to stop, before it closes the connection.
	briefViewerGrace = 2 * time.Second
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"brief show", "show a ticket's brief and its illustration: serve them on 127.0.0.1, or print their paths or text", []flagSpec{
			{"port", briefShowPortUsage, false},
			{"path", briefShowPathUsage, false},
			{"text", briefShowTextUsage, false},
			{"store", briefShowStoreUsage, false},
			{"project", briefShowProjectUsage, false},
		}, false},
		Order: 410,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdBriefShow(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
}

// cmdBriefShow implements `jig brief show [<ticket>] [--port n] [--path]
// [--text]`. By default it starts a minimal viewer of the ticket's brief on
// 127.0.0.1, prints its address and serves until the run is interrupted. --path
// prints where brief.md and illustration.html are instead, which is also what
// it does when the harness says the session is remote (remoteEnv), since a
// browser cannot reach the viewer there. --text prints the brief's sections as
// plain text. The ticket is the argument, else the one the branch names. It
// reads the store and writes nothing.
func cmdBriefShow(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "brief show")
	port := fs.Int("port", briefViewerPort, briefShowPortUsage)
	pathOnly := fs.Bool("path", false, briefShowPathUsage)
	text := fs.Bool("text", false, briefShowTextUsage)
	storeFlag := fs.String("store", "", briefShowStoreUsage)
	projectFlag := fs.String("project", "", briefShowProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if *pathOnly && *text {
		return renderErr(stdout, &axi.Error{Msg: "--path and --text are alternatives: pick one", Code: "VALIDATION_ERROR"})
	}
	if *port < 0 || *port > 65535 {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("--port %d is not a port: use 0 to 65535", *port), Code: "VALIDATION_ERROR"})
	}

	st, _, _, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := reviewTicket(e, st, jigHome, positionals, "brief show", stdout)
	if err == nil {
		err = requireTicket(st, ticket)
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	files, err := briefFilesOf(e, st, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}

	remote := e.getenv(remoteEnv) != ""
	switch {
	case *text:
		md, err := os.ReadFile(files.brief)
		if err != nil {
			return renderErr(stdout, err)
		}
		axi.Render(stdout, briefTextBlocks(ticket, md, files.illustration != "")...)
		return 0
	case *pathOnly || remote:
		blocks := []string{axi.KV("paths", files.pairs())}
		hint := fmt.Sprintf("`jig brief show %s` serves the brief on 127.0.0.1 for a browser", ticket)
		if remote {
			hint = remoteEnv + " is set, so the viewer is not started: a browser cannot reach 127.0.0.1 in this session. Read the files at these paths"
		}
		axi.Render(stdout, append(blocks, axi.Help(hint))...)
		return 0
	}
	rec, _ := st.ReadTicket(ticket)
	return serveBrief(e, stdout, ticket, rec.Title, files, *port)
}

// briefFiles are the files of a ticket's brief: the absolute path of brief.md,
// and of illustration.html when the ticket has one ("" when not).
type briefFiles struct {
	dir          string
	brief        string
	illustration string
}

// briefFilesOf finds the brief of ticket. A ticket with no brief.md is
// refused, since there is nothing to show.
func briefFilesOf(e env, st *store.Store, ticket string) (briefFiles, error) {
	dir, err := e.abs(st.TicketDir(ticket))
	if err != nil {
		return briefFiles{}, err
	}
	f := briefFiles{dir: dir, brief: filepath.Join(dir, "brief.md")}
	if _, err := os.Stat(f.brief); errors.Is(err, fs.ErrNotExist) {
		return briefFiles{}, &axi.Error{
			Msg:  fmt.Sprintf("ticket %s has no brief.md", ticket),
			Code: "VALIDATION_ERROR",
			Help: []string{intakeHint(ticket)},
		}
	} else if err != nil {
		return briefFiles{}, err
	}
	ill := filepath.Join(dir, store.IllustrationFile)
	if _, err := os.Stat(ill); err == nil {
		f.illustration = ill
	} else if !errors.Is(err, fs.ErrNotExist) {
		return briefFiles{}, err
	}
	return f, nil
}

// pairs is the files as the key/value lines of a paths block: the brief, and
// the illustration when there is one.
func (f briefFiles) pairs() [][2]string {
	pairs := [][2]string{{"brief", f.brief}}
	if f.illustration != "" {
		pairs = append(pairs, [2]string{"illustration", f.illustration})
	}
	return pairs
}

// briefTextBlocks is `jig brief show --text`: a brief block naming the
// ticket, the brief's title and whether it has an illustration; a sections
// table of each "## " heading with the first line or two of its body; then,
// for each section in order, a block whose header is "section: <heading>" and
// whose lines are the section's body indented by two spaces, a blank line
// staying empty. A block ends at the next line that is not indented, so a body
// can hold any text. Text before the first section is not part of it.
func briefTextBlocks(ticket string, md []byte, hasIllustration bool) []string {
	sections := store.BriefSections(md)
	kv := [][2]string{{"ticket", ticket}}
	if title := briefview.Title(md); title != "" {
		kv = append(kv, [2]string{"title", title})
	}
	illustration := "no"
	if hasIllustration {
		illustration = "yes"
	}
	kv = append(kv, [2]string{"illustration", illustration})
	blocks := []string{axi.KV("brief", kv)}

	rows := make([][]string, 0, len(sections))
	for _, sec := range sections {
		rows = append(rows, []string{sec.Heading, briefview.Summary(sec.Body)})
	}
	blocks = append(blocks, axi.Table("sections", []string{"heading", "summary"}, rows))

	for _, sec := range sections {
		b := axi.Block{Header: "section: " + axi.Quote(sec.Heading)}
		body := strings.TrimLeft(sec.Body, "\n")
		if body != "" {
			for _, line := range strings.Split(body, "\n") {
				if line = strings.TrimRight(line, " \t"); line != "" {
					line = "  " + line
				}
				b.Lines = append(b.Lines, line)
			}
		}
		blocks = append(blocks, b.String())
	}
	return append(blocks, axi.Help(fmt.Sprintf("`jig brief show %s` serves the brief and its illustration on 127.0.0.1 for a browser", ticket)))
}

// serveBrief serves the ticket's brief on 127.0.0.1:port until e.ctx ends,
// after printing where. It re-reads the files on every request, so the person
// reloads to see an edit. The run ends 0 when the context ends, since an
// interrupt is how a viewer is stopped.
func serveBrief(e env, stdout io.Writer, ticket, title string, files briefFiles, port int) int {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("cannot serve the brief on 127.0.0.1:%d: %v", port, err),
			Code: "VIEWER_UNAVAILABLE",
			Help: []string{"Pick another port with --port, or `--port 0` for any free one"},
		})
	}
	base := "http://" + ln.Addr().String()
	srv := &http.Server{
		Handler: briefview.Handler(briefview.Source{
			Ticket: ticket, Title: title,
			BriefPath: files.brief, IllustrationPath: filepath.Join(files.dir, store.IllustrationFile),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	pairs := [][2]string{{"url", base + "/"}, {"ticket", ticket}}
	if files.illustration != "" {
		pairs = append(pairs, [2]string{"illustration", base + briefview.IllustrationRoute})
	}
	axi.Render(stdout, axi.KV("viewer", pairs), axi.Help("Open the url in a browser and reload to see an edit; interrupt (Ctrl-C) to stop the viewer"))

	select {
	case err := <-served:
		return renderErr(stdout, fmt.Errorf("the viewer stopped: %w", err))
	case <-e.ctx.Done():
	}
	ctx, cancel := context.WithTimeout(context.Background(), briefViewerGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		srv.Close()
	}
	<-served
	return 0
}
