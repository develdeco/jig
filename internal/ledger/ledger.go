// Package ledger is the machine ledger: the append-only NDJSON file under the
// jig home (home.LedgerPath) that every session, hook and detached worker on
// this machine writes to, and that `jig events`, `jig here` and the TUI read.
// It is machine-local, never in a store's git; what other machines must see
// goes in a ticket's journal.
//
// Its lines are journal.Line values, so one shape serves both files and the
// ledger-only events (journal.EventSessionStart and the others in
// journal/events.go) are just names in it. Unlike a journal, which is
// rewritten whole on each append, the ledger is only ever appended to, one
// write per line, so a reader that follows it by offset (ReadFrom) never sees
// its file replaced under it.
package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// lockedCode is Append's refusal when it cannot get the ledger's lock.
const lockedCode = "LEDGER_LOCKED"

// lockTimeout is how long Append waits for another writer before it refuses.
const lockTimeout = 10 * time.Second

// Append adds l to the ledger under the jig home root, as one line. l.TS
// defaults to now with nanoseconds when empty. It takes the ledger's lock
// (store.TryLock on the ledger file) for ten seconds and, if another writer
// still holds it then, refuses with LEDGER_LOCKED rather than write without
// it: a ledger line is a record of what happened, and a hook that cannot
// record it can say so and go on. The line goes out in a single O_APPEND
// write, with a newline in front of it if the file ends in a torn line, so a
// writer that died mid-line costs that one line and not the next.
func Append(root string, l journal.Line) error {
	if l.TS == "" {
		l.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	encoded, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("ledger: encode line: %w", err)
	}
	encoded = append(encoded, '\n')

	path := home.LedgerPath(root)
	release, held, err := store.TryLock(path, lockTimeout)
	if err != nil {
		return fmt.Errorf("ledger: lock %s: %w", path, err)
	}
	defer release()
	if !held {
		return &axi.Error{
			Msg:  fmt.Sprintf("the ledger %s is locked by another process and stayed locked for %s", path, lockTimeout),
			Code: lockedCode,
			Help: []string{"Retry; if it persists, a jig process on this machine is stuck holding it"},
		}
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("ledger: open %s: %w", path, err)
	}
	defer f.Close()
	if torn, err := endsMidLine(f); err != nil {
		return fmt.Errorf("ledger: read %s: %w", path, err)
	} else if torn {
		encoded = append([]byte{'\n'}, encoded...)
	}
	if _, err := f.Write(encoded); err != nil {
		return fmt.Errorf("ledger: write %s: %w", path, err)
	}
	return nil
}

// endsMidLine reports whether f is non-empty and does not end in a newline.
func endsMidLine(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// Read returns every line of the ledger under the jig home root, in file
// order. A line that is not a JSON line (a torn write, a hand edit) is
// skipped, as is a blank one; one bad line never hides the rest. An absent
// ledger reads as no lines. A final line with no newline yet is returned if
// it parses. Readers never lock.
func Read(root string) ([]journal.Line, error) {
	data, err := os.ReadFile(home.LedgerPath(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ledger: read: %w", err)
	}
	var lines []journal.Line
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		if l, ok := parseLine(raw); ok {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// ReadFrom returns the lines of the ledger from byte offset off on, and the
// offset to pass next time: the end of the last complete line it read. A
// follower (jig events) polls with it: it reopens the file by path on every
// call, so it never holds a handle that would stop a writer, and the first
// call's off is 0. A final line still being written, with no newline yet, is
// left for the next call rather than returned in pieces. An off past the end
// of the file means the file was replaced or emptied, and the read starts
// over from 0. Malformed lines are skipped, as in Read. An absent ledger
// reads as no lines, at offset 0.
func ReadFrom(root string, off int64) ([]journal.Line, int64, error) {
	f, err := os.Open(home.LedgerPath(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, off, fmt.Errorf("ledger: open: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, off, fmt.Errorf("ledger: stat: %w", err)
	}
	if off < 0 || off > info.Size() {
		off = 0
	}
	if off == info.Size() {
		return nil, off, nil
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, off, fmt.Errorf("ledger: seek: %w", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, off, fmt.Errorf("ledger: read: %w", err)
	}
	complete := bytes.LastIndexByte(data, '\n') + 1
	var lines []journal.Line
	for _, raw := range bytes.Split(data[:complete], []byte{'\n'}) {
		if l, ok := parseLine(raw); ok {
			lines = append(lines, l)
		}
	}
	return lines, off + int64(complete), nil
}

// parseLine decodes one ledger line, ok false for a blank or malformed one.
func parseLine(raw []byte) (journal.Line, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return journal.Line{}, false
	}
	var l journal.Line
	if err := json.Unmarshal(raw, &l); err != nil {
		return journal.Line{}, false
	}
	return l, true
}
