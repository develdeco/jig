package envrun

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/manifest"
)

// existsCmd and removeCmd build platform-appropriate shell fragments so the
// marker-file test exercises the same substitution path Shell uses in
// production, without depending on a particular OS.
func existsCmd(name string) string {
	if runtime.GOOS == "windows" {
		return "if exist " + name + " (exit 0) else (exit 1)"
	}
	return "test -f " + name
}

func removeCmd(name string) string {
	if runtime.GOOS == "windows" {
		return "del " + name
	}
	return "rm -f " + name
}

// trimEOL strips the trailing line ending `echo` writes.
func trimEOL(s string) string {
	return strings.TrimRight(s, "\r\n")
}

func TestShellExitCodes(t *testing.T) {
	dir := t.TempDir()
	if err := Shell("exit 0", dir); err != nil {
		t.Fatalf("exit 0: %v", err)
	}
	if err := Shell("exit 1", dir); err == nil {
		t.Fatal("exit 1: want error, got nil")
	}
}

// markerClass builds an EnvClass whose up/check/down commands operate on a
// port-named marker file, so a wrong substitution anywhere in the chain
// shows up as a check or down failure.
func markerClass() manifest.EnvClass {
	return manifest.EnvClass{
		Up:    "echo {ticket}>up-{port}.marker",
		Check: existsCmd("up-{port}.marker"),
		Down:  removeCmd("up-{port}.marker"),
	}
}

func TestUpCheckDownPortSubstitution(t *testing.T) {
	dir := t.TempDir()
	c := markerClass()

	h, err := Up(c, "T-1", dir)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if h.Port <= 0 {
		t.Fatalf("Port = %d, want positive", h.Port)
	}

	markerPath := filepath.Join(dir, fmt.Sprintf("up-%d.marker", h.Port))
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("marker file for allocated port missing: %v", err)
	}
	if got := trimEOL(string(data)); got != "T-1" {
		t.Fatalf("marker content = %q, want T-1", got)
	}

	if err := h.Down(); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("marker file should be removed by Down, stat err = %v", err)
	}
}

func TestUpFailureReturnsUnavailableWithPolicy(t *testing.T) {
	dir := t.TempDir()
	c := manifest.EnvClass{Up: "exit 1", Check: "exit 0", Down: "exit 0", Unavailable: "defer-ci"}

	_, err := Up(c, "T-1", dir)
	if err == nil {
		t.Fatal("expected error")
	}
	var u *Unavailable
	if !errors.As(err, &u) {
		t.Fatalf("expected *Unavailable, got %T: %v", err, err)
	}
	if u.Stage != "up" {
		t.Fatalf("Stage = %q, want up", u.Stage)
	}
	if u.Policy != "defer-ci" {
		t.Fatalf("Policy = %q, want defer-ci", u.Policy)
	}
}

func TestCheckFailureReturnsUnavailableWithPolicy(t *testing.T) {
	dir := t.TempDir()
	c := manifest.EnvClass{Up: "exit 0", Check: "exit 1", Down: "exit 0", Unavailable: ""}

	_, err := Up(c, "T-1", dir)
	if err == nil {
		t.Fatal("expected error")
	}
	var u *Unavailable
	if !errors.As(err, &u) {
		t.Fatalf("expected *Unavailable, got %T: %v", err, err)
	}
	if u.Stage != "check" {
		t.Fatalf("Stage = %q, want check", u.Stage)
	}
	if u.Policy != "" {
		t.Fatalf("Policy = %q, want empty (pause)", u.Policy)
	}
}

// TestAllocatePortLoopbackOnly guards the port probe against binding every
// interface, which opens a port to the network and makes Windows Firewall
// prompt for each new jig binary.
func TestAllocatePortLoopbackOnly(t *testing.T) {
	host, _, err := net.SplitHostPort(portProbeAddr)
	if err != nil {
		t.Fatalf("split %q: %v", portProbeAddr, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("portProbeAddr = %q, want a loopback address", portProbeAddr)
	}
	port, err := allocatePort()
	if err != nil {
		t.Fatalf("allocatePort: %v", err)
	}
	l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("port %d from allocatePort is not free on %s: %v", port, host, err)
	}
	l.Close()
}
