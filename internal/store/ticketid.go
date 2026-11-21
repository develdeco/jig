package store

import (
	"errors"
	"fmt"
	"strings"
)

// CheckTicketID reports why id cannot name a ticket's folder, or nil when it
// can. The id is used as one directory name under the store's tickets, so it
// must be a single path component that does not start with a dot (which also
// rules out "." and "..").
func CheckTicketID(id string) error {
	switch {
	case id == "":
		return errors.New("ticket id is empty")
	case strings.ContainsAny(id, `/\`):
		return fmt.Errorf("ticket id %q contains a path separator; it must name a single directory", id)
	case strings.HasPrefix(id, "."):
		return fmt.Errorf("ticket id %q starts with a dot; it must name a plain, visible directory", id)
	}
	return nil
}
