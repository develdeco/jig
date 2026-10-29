//go:build windows

package store

import (
	"errors"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// replacing reports whether err is how Windows fails an open of a file that
// another process is renaming into place, or a rename over a file another
// process has open: a sharing violation, or access denied while the replaced
// file is pending delete.
func replacing(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// readFileOnce reads path whole. Unlike os.ReadFile it opens the file with
// delete sharing, so a reader never stops a writer's rename from replacing
// the file it holds: the reader goes on reading the bytes it opened.
func readFileOnce(path string) ([]byte, error) {
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
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	return io.ReadAll(f)
}

// fileRenameInfo is FILE_RENAME_INFO with its flags, as
// SetFileInformationByHandle takes it for FileRenameInfoEx.
type fileRenameInfo struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

// replaceFile renames tmp over path with POSIX semantics, so the name moves
// to the new file in one step even while another process holds the old one
// open (with delete sharing, as readFileOnce opens it): a reader opening
// path sees the old file or the new one, never neither. A file system that
// has no POSIX semantics gets os.Rename.
func replaceFile(tmp, path string) error {
	err := posixRename(tmp, path)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return os.Rename(tmp, path)
	}
	return err
}

// posixRename is replaceFile's FileRenameInfoEx call.
func posixRename(tmp, path string) error {
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: err}
	}
	h, err := windows.CreateFile(from, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: err}
	}
	defer windows.CloseHandle(h)
	to, err := windows.UTF16FromString(path)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: err}
	}
	nameLen := (len(to) - 1) * 2 // bytes, without the terminating zero
	buf := make([]byte, int(unsafe.Offsetof(fileRenameInfo{}.FileName))+nameLen+2)
	info := (*fileRenameInfo)(unsafe.Pointer(&buf[0]))
	info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.FileNameLength = uint32(nameLen)
	copy(unsafe.Slice(&info.FileName[0], len(to)), to)
	if err := windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buf[0], uint32(len(buf))); err != nil {
		return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: err}
	}
	return nil
}
