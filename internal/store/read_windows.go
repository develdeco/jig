//go:build windows

package store

import (
	"errors"

	"golang.org/x/sys/windows"
)

// replacing reports whether err is how Windows fails an open of a file that
// another process is renaming into place: a sharing violation, or access
// denied while the replaced file is pending delete.
func replacing(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
