// Package spend sums the token usage of a harness session from its transcript,
// prices it, and holds it against a ticket's budget, so cost shows in status
// and in the context a stop hook returns. It reports and never stops anything.
//
// Spend is kept as records:
//
//   - a spend line in the ticket journal, one per session, cumulative: the
//     latest line of a session says what the session has spent so far
//     (Line, Latest, FromLines);
//   - a budget.yaml in the ticket folder, written by `jig budget` (ReadBudget,
//     WriteBudget), with the project's jig.yaml budget as the default a ticket
//     starts from (ProjectDefault).
//
// The numbers come from the Claude Code transcript (FromTranscript): the
// usage of each API message, counted once however many transcript lines
// repeat it, for the lines the session itself wrote plus its subagents'.
// Tokens are exact. Dollars are list prices: a built-in table, laid over by
// the jig home's prices.yaml (LoadPrices), and a model that neither names is
// reported as unpriced rather than guessed at.
//
// Notes is the note source `jig observe` calls at every event of a session:
// at a stop and a session end it writes the session's spend line when the
// total changed, and whenever the ticket is near or over its budget it says
// so, as an advisory note and never an enforced one.
package spend
