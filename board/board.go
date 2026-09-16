// Package board defines jig's structural-round interface. It is a stub in
// v0.1: Deferred implements Board but refuses every call, pending the
// real implementation.
package board

import "github.com/develdeco/jig/axi"

// Board opens a structural round of some kind, carrying an
// implementation-defined payload.
type Board interface {
	OpenStructuralRound(kind string, payload any) error
}

// Deferred is the v0.1 stand-in for Board: every call fails with a
// NOT_IMPLEMENTED error pointing at v0.2.
type Deferred struct{}

// OpenStructuralRound always fails: the board feature ships in v0.2.
func (Deferred) OpenStructuralRound(kind string, payload any) error {
	return &axi.Error{
		Msg:  "structural rounds are not implemented yet; board ships in v0.2",
		Code: "NOT_IMPLEMENTED",
	}
}
