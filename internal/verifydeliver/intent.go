package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// Intent source values. "brief" and "explicit" are binding: the human's
// own statement of what was asked for. "inferred" is jig's own summary of
// the author's local agent session - a hint that may be partial or wrong,
// never binding. "none" means nothing states it.
const (
	IntentSourceBrief    = "brief"
	IntentSourceExplicit = "explicit"
	IntentSourceInferred = "inferred"
	IntentSourceNone     = "none"
)

// Intent is a gate round's resolved intent binding: where it came from,
// and, for a binding source, the absolute path to the file a reviewer
// reads for it. "" for source "none".
type Intent struct {
	Source string `json:"source" yaml:"source"`
	Path   string `json:"path" yaml:"path"`
}

// intentSourcesPrompt is the one sentence every session prompt that hands a
// session an Intent uses to say what each source means: the reviewer's and
// the demo's. It is written once so that a source jig gains is described to
// both sessions, or to neither. Adding a source above means describing it
// here.
const intentSourcesPrompt = `"brief" or "explicit" is the human's own statement of what was asked for; "inferred" is jig's own summary of the author's own agent session, a hint that may be partial or wrong; "none" means nothing states it.`

// resolveIntent resolves ticket's bound intent for one gate round, once,
// before the round's source runs (Gate calls this ahead of src.Round).
// Precedence: the ticket's own brief.md (source "brief"), else its
// intent.md and the source recorded there, else "none". It also returns
// the exact bytes at the resolved Path - the file the reviewer itself
// reads - so the caller can hash what was actually read for report.yaml,
// never a parsed or normalized stand-in for it.
func resolveIntent(st *store.Store, ticket string) (Intent, string, error) {
	briefPath := filepath.Join(st.TicketDir(ticket), "brief.md")
	briefData, err := os.ReadFile(briefPath)
	switch {
	case err == nil:
		abs, aerr := filepath.Abs(briefPath)
		if aerr != nil {
			return Intent{}, "", fmt.Errorf("verifydeliver: gate: resolve brief.md path: %w", aerr)
		}
		return Intent{Source: IntentSourceBrief, Path: abs}, string(briefData), nil
	case !os.IsNotExist(err):
		return Intent{}, "", fmt.Errorf("verifydeliver: gate: read brief.md: %w", err)
	}

	intentPath := st.IntentPath(ticket)
	raw, err := os.ReadFile(intentPath)
	switch {
	case err == nil:
		// One read: parse the same bytes this function hashes below, never
		// a second read of a file another process could rewrite in
		// between (store.ReadIntent's own read followed by a raw re-read
		// for the hash would be two independent reads of the same path).
		in, perr := store.ParseIntent(raw)
		if perr != nil {
			return Intent{}, "", &axi.Error{
				Msg:  fmt.Sprintf("ticket %s's intent.md cannot be read: %v", ticket, perr),
				Code: "INTENT_INVALID",
				Help: intentReplaceHelp(ticket),
			}
		}
		// intent.md may carry only the source values jig itself ever
		// writes there - "explicit" or "inferred". Anything else (a hand
		// edit, a stale value from a source jig no longer supports, or a
		// missing source key parsed as "") is refused rather than trusted:
		// an unrecognized source is not itself binding, and letting one
		// through would let a hand-edited "brief" or "none" impersonate a
		// provenance the human never actually gave.
		if in.Source != IntentSourceExplicit && in.Source != IntentSourceInferred {
			return Intent{}, "", &axi.Error{
				Msg:  fmt.Sprintf("ticket %s's intent.md has an unsupported source %q; jig only ever records %q or %q there", ticket, in.Source, IntentSourceExplicit, IntentSourceInferred),
				Code: "INTENT_INVALID_SOURCE",
				Help: intentReplaceHelp(ticket),
			}
		}
		// Read as strictly as it is written (writeExplicitIntent): a body
		// with no text to judge a fix against binds nothing, so a hand
		// edit that empties it is refused here the same way --intent or
		// --doc with that text is refused there.
		if intentTextEmpty(in.Text) {
			return Intent{}, "", &axi.Error{
				Msg:  fmt.Sprintf("ticket %s's intent.md holds no intent text", ticket),
				Code: "INTENT_EMPTY",
				Help: intentReplaceHelp(ticket),
			}
		}
		abs, aerr := filepath.Abs(intentPath)
		if aerr != nil {
			return Intent{}, "", fmt.Errorf("verifydeliver: gate: resolve intent.md path: %w", aerr)
		}
		return Intent{Source: in.Source, Path: abs}, string(raw), nil
	case os.IsNotExist(err):
		return Intent{Source: IntentSourceNone}, "", nil
	default:
		return Intent{}, "", fmt.Errorf("verifydeliver: gate: read intent.md: %w", err)
	}
}

// intentReplaceHelp is the recovery every refusal of a bad intent.md
// shares: only `jig gate --intent`/`--doc` ever writes the file, and either
// one replaces whatever is there, so rerunning one is the whole fix.
func intentReplaceHelp(ticket string) []string {
	return []string{fmt.Sprintf("Rerun `jig gate %s` with --intent or --doc to replace intent.md.", ticket)}
}

// intentTextEmpty reports whether text states no intent at all: empty, or
// only whitespace. Judged on the text a reader would actually see, not on
// the literal bytes, so writeExplicitIntent (refusing to record it) and
// resolveIntent (refusing to read it back) apply the same rule.
func intentTextEmpty(text string) bool {
	return strings.TrimSpace(text) == ""
}

// writeExplicitIntent writes ticket's intent.md from a `jig gate --intent`
// or `--doc` flag: text is --intent's literal value, doc is --doc's file
// path. GateOpts is a library entry point, not only a CLI one, so this
// function enforces at most one of the two ever being set itself - the
// CLI's own check (cmd/jig/gate.go) is a fast path to the same refusal,
// never the only guard against it. A ticket that already has a brief.md
// always resolves to it (resolveIntent's own precedence), so writing an
// intent.md beside it would record a provenance jig would never read;
// that is refused rather than silently accepted.
func writeExplicitIntent(st *store.Store, ticket, text, doc string) error {
	briefPath := filepath.Join(st.TicketDir(ticket), "brief.md")
	if _, err := os.Stat(briefPath); err == nil {
		return &axi.Error{
			Msg:  fmt.Sprintf("ticket %s already has a brief.md, which is always the binding intent", ticket),
			Code: "INTENT_CONFLICT",
			Help: []string{"Amend brief.md instead of setting an explicit intent."},
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("verifydeliver: gate: stat brief.md: %w", err)
	}
	if text != "" && doc != "" {
		return &axi.Error{
			Msg:  fmt.Sprintf("ticket %s's intent text and doc are mutually exclusive; set only one", ticket),
			Code: "VALIDATION_ERROR",
		}
	}

	body := text
	if doc != "" {
		data, err := os.ReadFile(doc)
		if err != nil {
			return &axi.Error{
				Msg:  fmt.Sprintf("intent doc %q is set but unreadable: %v", doc, err),
				Code: "INTENT_DOC_MISSING",
			}
		}
		body = string(data)
	}
	// A whitespace-only body binds nothing, the same as an empty one:
	// "\n" or "   " is refused the same way "" is (intentTextEmpty) -
	// rather than recording a binding explicit intent with nothing in it
	// for a reviewer to judge a fix against.
	if intentTextEmpty(body) {
		return &axi.Error{
			Msg:  fmt.Sprintf("ticket %s's explicit intent is empty", ticket),
			Code: "INTENT_EMPTY",
		}
	}
	return st.WriteIntent(ticket, store.Intent{Source: IntentSourceExplicit, Text: body})
}

// intentSHA256 returns the sha256 hex digest of text, or "" for source
// "none": report.yaml records what was hashed, and "none" states nothing
// to hash.
func intentSHA256(source, text string) string {
	if source == IntentSourceNone {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
