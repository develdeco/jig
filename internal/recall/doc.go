// Package recall finds what a planner should see before a ticket is written:
// the open tickets and the edges between them, the open tickets whose titles
// or bodies come near a query, the fog of the charts, the platform documents
// and the answers the ledger holds. It backs `jig recall`, and its
// similarity measure is the one `jig validate` uses to tell a planner that a
// slice's goal looks covered by work that already shipped.
//
// Build is pure: it takes the tickets the caller read (with the state the
// readiness rules gave them) and the Material Read took from the store's
// files, and returns a Report that marshals to JSON as it stands and renders
// in the jig text register through Report.Blocks. Two texts come near each
// other when the Jaccard index of their word sets reaches Threshold (see
// Similarity); nothing here judges meaning, it only tells a planner where to
// look.
package recall
