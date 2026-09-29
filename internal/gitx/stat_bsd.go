//go:build darwin || freebsd || netbsd

package gitx

import (
	"os"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// fillStat records the stat data the git program compares an index entry
// with, besides size and modification time, so it does not read the file
// again to find it unchanged.
func fillStat(e *index.Entry, info os.FileInfo) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		e.CreatedAt = time.Unix(st.Ctimespec.Unix())
		e.Dev, e.Inode = uint32(st.Dev), uint32(st.Ino)
		e.UID, e.GID = st.Uid, st.Gid
	}
}
