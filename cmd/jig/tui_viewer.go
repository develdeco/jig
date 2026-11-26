package main

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/develdeco/jig/internal/briefview"
	"github.com/develdeco/jig/internal/store"
)

// tuiViewer is the screens' viewer of a ticket's illustration: the one action
// they take outside a verb, because `jig brief show` blocks until it is
// interrupted and the screens run one verb at a time. It serves what the verb
// serves, with the same handler, on the loopback address, one listener for each
// ticket for as long as the screens run; in a remote session, where a browser
// cannot reach the loopback address, it serves nothing and names the file. It
// writes nothing.
type tuiViewer struct {
	e  env
	st *store.Store

	mu      sync.Mutex
	servers map[string]*viewerListener
}

// viewerListener is a ticket's listener and the address of its illustration.
type viewerListener struct {
	srv  *http.Server
	addr string
}

func newTUIViewer(e env, st *store.Store) *tuiViewer {
	return &tuiViewer{e: e, st: st, servers: map[string]*viewerListener{}}
}

// Address is the address of ticket's illustration, serving it first when the
// screens have not: the address enter on the illustration sends to the opener.
// In a remote session it is the illustration's path.
func (v *tuiViewer) Address(ticket string) (string, error) {
	files, err := briefFilesOf(v.e, v.st, ticket)
	if err != nil {
		return "", err
	}
	if files.illustration == "" {
		return "", errors.New("ticket " + ticket + " has no illustration")
	}
	if v.e.getenv(remoteEnv) != "" {
		return files.illustration, nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if l, ok := v.servers[ticket]; ok {
		return l.addr, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	rec, _ := v.st.ReadTicket(ticket)
	srv := &http.Server{
		Handler:           briefview.Handler(briefview.Source{Ticket: ticket, Title: rec.Title, BriefPath: files.brief, IllustrationPath: files.illustration}),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	go func() { _ = srv.Serve(ln) }()
	l := &viewerListener{srv: srv, addr: "http://" + ln.Addr().String() + briefview.IllustrationRoute}
	v.servers[ticket] = l
	return l.addr, nil
}

// Close stops every listener: the screens ended.
func (v *tuiViewer) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	for id, l := range v.servers {
		_ = l.srv.Close()
		delete(v.servers, id)
	}
}
