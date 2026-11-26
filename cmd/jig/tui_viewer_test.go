package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/store"
)

// TestTheTUIViewerServesTheIllustrationOnLoopbackAndStops: the viewer the screens
// are given serves the ticket's illustration, as the file is, on the loopback
// address; asked again it gives the same address; and when the screens end it
// stops, so the address answers no more.
func TestTheTUIViewerServesTheIllustrationOnLoopbackAndStops(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, true)
	v := newTUIViewer(testEnv(r.fx.Home), r.st)
	defer v.Close()

	addr, err := v.Address(showTicket)
	if err != nil || !strings.HasPrefix(addr, "http://127.0.0.1:") || !strings.HasSuffix(addr, "/"+store.IllustrationFile) {
		t.Fatalf("Address = %q, %v, want the illustration on 127.0.0.1", addr, err)
	}
	if code, body := fetch(t, addr); code != 200 || body != showHTML {
		t.Fatalf("GET %s: %d %q, want the file as it is", addr, code, body)
	}
	if again, err := v.Address(showTicket); err != nil || again != addr {
		t.Errorf("asked again, Address = %q, %v, want the same address %q", again, err, addr)
	}
	// The brief's page is served beside it.
	if code, page := fetch(t, strings.TrimSuffix(addr, store.IllustrationFile)); code != 200 || !strings.Contains(page, "<h2>Goal</h2>") {
		t.Errorf("the brief's page: %d %q", code, page)
	}

	v.Close()
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(addr); err == nil {
		resp.Body.Close()
		t.Errorf("GET %s still answers after the viewer was closed", addr)
	}
}

// TestTheTUIViewerInARemoteSessionGivesThePath: where a browser cannot reach the
// loopback address the viewer serves nothing and names the illustration's file, as
// `jig brief show --path` does, and a brief with no illustration is refused.
func TestTheTUIViewerInARemoteSessionGivesThePath(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t, true)
	v := newTUIViewer(remoteTestEnv(r.fx.Home), r.st)
	defer v.Close()
	got, err := v.Address(showTicket)
	if err != nil || got != r.path(store.IllustrationFile) {
		t.Fatalf("Address = %q, %v, want the path %q", got, err, r.path(store.IllustrationFile))
	}
	if n := len(v.servers); n != 0 {
		t.Errorf("a remote session serves %d listeners, want none", n)
	}

	bare := newBriefRig(t, false)
	if got, err := newTUIViewer(testEnv(bare.fx.Home), bare.st).Address(showTicket); err == nil {
		t.Errorf("a brief with no illustration is served at %q", got)
	}
}
