//go:build !linux && !darwin && !freebsd && !netbsd && !windows

package gitx

import (
	"os"

	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// fillStat records nothing beyond size and modification time here; the git
// program then reads such a file again to find it unchanged.
func fillStat(*index.Entry, os.FileInfo) {}
