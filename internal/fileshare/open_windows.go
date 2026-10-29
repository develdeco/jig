//go:build windows

package fileshare

import (
	"os"

	"golang.org/x/sys/windows"
)

// Open opens path for reading, sharing it for reading, writing and deletion,
// so a rename over it or a removal of it goes ahead while it is open: the
// holder goes on reading the bytes it opened. A directory is not opened.
func Open(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
