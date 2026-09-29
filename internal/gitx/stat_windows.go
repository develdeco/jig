package gitx

import (
	"os"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// fillStat records the stat data Git for Windows compares an index entry
// with, besides size and modification time: its change time is the file's
// creation time, and it has no device, inode or owner.
func fillStat(e *index.Entry, info os.FileInfo) {
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		e.CreatedAt = time.Unix(0, d.CreationTime.Nanoseconds())
	}
}
