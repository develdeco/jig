// Package ready decides what can be built next: which slices of a ticket are
// ready (every slice they wait on is green) and in what order a ready set is
// scheduled, and which tickets of a store are ready to start. It holds the
// rules `jig next` and `jig here` share, taken out of internal/frontier so
// they need no session backend.
//
// The package holds no code yet; its directory and its place in
// ARCHITECTURE.md are reserved so the code that fills it lands in files of
// its own.
package ready
