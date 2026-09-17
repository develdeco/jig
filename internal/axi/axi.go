// Package axi renders jig's plain-text output register: labelled key/value
// blocks, tables, help hints, and structured errors. Every command prints
// through these primitives so output stays predictable and script-friendly.
package axi

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// NeedsInput is the structured-error code that maps to exit code 2, used
// when a command pauses waiting for an answer.
var NeedsInput = "NEEDS_INPUT"

// Block is the shared internal representation of one axi output block: a
// header line followed by zero or more indented content lines.
type Block struct {
	Header string
	Lines  []string
}

// String renders the block: just the header when there are no lines, else
// the header followed by each line on its own row.
func (b Block) String() string {
	if len(b.Lines) == 0 {
		return b.Header
	}
	return strings.Join(append([]string{b.Header}, b.Lines...), "\n")
}

// KV renders a labelled block of key/value pairs: "label:\n  key: value\n...".
// Values are quoted via Quote when they contain characters that would make
// the line ambiguous.
func KV(label string, pairs [][2]string) string {
	b := Block{Header: label + ":"}
	for _, p := range pairs {
		b.Lines = append(b.Lines, "  "+p[0]+": "+Quote(p[1]))
	}
	return b.String()
}

// Table renders a labelled block of CSV rows: "label[N]{c1,c2}:\n  v1,v2\n...".
// An empty row set renders just the header, e.g. "label[0]:".
func Table(label string, cols []string, rows [][]string) string {
	b := Block{Header: fmt.Sprintf("%s[%d]{%s}:", label, len(rows), strings.Join(cols, ","))}
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = Quote(c)
		}
		b.Lines = append(b.Lines, "  "+strings.Join(cells, ","))
	}
	return b.String()
}

// Help renders a labelled block of hint lines: "help[N]:\n  line\n...". Every
// command should end with exactly one Help block naming a contextual next
// step.
func Help(lines ...string) string {
	b := Block{Header: fmt.Sprintf("help[%d]:", len(lines))}
	for _, l := range lines {
		b.Lines = append(b.Lines, "  "+l)
	}
	return b.String()
}

// Quote double-quotes s when it contains a character that would make it
// ambiguous in a KV line or a Table CSV row: comma, colon, double quote,
// backslash, or a newline. Inner double quotes are escaped as \" and
// newlines as the two-character sequence \n.
func Quote(s string) string {
	if !strings.ContainsAny(s, ",:\"\\") && !strings.ContainsAny(s, "\n\r") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			// normalize bare CR the same as LF
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Error is jig's structured, user-facing error: a message, a machine-
// readable code, and optional help lines rendered alongside it.
type Error struct {
	Msg  string
	Code string
	Help []string
}

// Error implements the error interface, returning the plain message.
func (e *Error) Error() string {
	return e.Msg
}

// Render writes each block to w, one per line (multi-line blocks keep their
// internal newlines), in order.
func Render(w io.Writer, blocks ...string) {
	for _, b := range blocks {
		fmt.Fprintln(w, b)
	}
}

// RenderError writes e as "error: <msg>\ncode: <CODE>\n" followed by an
// optional help block when e.Help is non-empty.
func RenderError(w io.Writer, e *Error) {
	fmt.Fprintf(w, "error: %s\n", e.Msg)
	fmt.Fprintf(w, "code: %s\n", e.Code)
	if len(e.Help) > 0 {
		fmt.Fprintln(w, Help(e.Help...))
	}
}

// ExitCode maps err to a process exit code: nil is 0; an *Error with code
// VALIDATION_ERROR or NeedsInput is 2; any other error is 1.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ae *Error
	if errors.As(err, &ae) {
		if ae.Code == "VALIDATION_ERROR" || ae.Code == NeedsInput {
			return 2
		}
		return 1
	}
	return 1
}
