package ledger

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// backwardChunk is how much of the ledger ReadBackward reads at a time.
const backwardChunk = 64 << 10

// ReadBackward calls visit with the lines of the ledger under the jig home
// root, newest first, until visit returns false or the lines run out. It is
// the read for a question about what a session did lately: the answer is
// near the end of the file, and the file (every tool call of the machine
// adds a line to it) is too long to read whole on every hook.
//
// It reads the file in chunks from its end, so the cost is the distance from
// the end to the line that ends the walk. maxBytes bounds that distance: a
// positive value stops the walk that many bytes from the end (a line cut by
// the bound is not visited), zero or less reads to the start of the file.
// Malformed and blank lines are skipped, as in Read; a final line with no
// newline yet is visited if it parses. An absent ledger has no lines.
// Readers never lock, and lines a writer appends while the walk runs are not
// seen.
func ReadBackward(root string, maxBytes int64, visit func(journal.Line) bool) error {
	f, err := os.Open(home.LedgerPath(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("ledger: open: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("ledger: stat: %w", err)
	}
	end := info.Size()
	// floor is where the first line the bound admits may start. The walk reads
	// the byte before it as well, to tell a line that starts exactly there
	// (the byte is a newline) from one the bound cuts in two.
	floor := int64(0)
	if maxBytes > 0 && maxBytes < end {
		floor = end - maxBytes
	}
	lo := max(floor-1, 0)

	// rest is the start of the line the next chunk ends: the bytes read so far
	// after the last newline seen.
	var rest []byte
	for end > lo {
		start := max(end-backwardChunk, lo)
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("ledger: read: %w", err)
		}
		end = start
		data := append(buf, rest...)
		first := bytes.IndexByte(data, '\n')
		if first < 0 {
			// No line ends in this chunk: it is all the start of one line.
			rest = data
			continue
		}
		rest = data[:first]
		for tail := data[first+1:]; len(tail) > 0; {
			cut := bytes.LastIndexByte(tail[:len(tail)-1], '\n') + 1
			if l, ok := parseLine(tail[cut:]); ok && !visit(l) {
				return nil
			}
			tail = tail[:cut]
		}
	}
	// What is left is the start of a line. It is whole only when the walk
	// reached the start of the file.
	if lo == 0 {
		if l, ok := parseLine(rest); ok {
			visit(l)
		}
	}
	return nil
}
