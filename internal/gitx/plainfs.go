package gitx

import (
	"os"
	"path/filepath"

	"github.com/go-git/go-billy/v5"
)

// plainFS is the file system gitx gives go-git for a git directory: plain
// OS calls on paths joined under root. go-billy's own OS file systems
// resolve every path component for symbolic links on every call, a stat
// per component, which on Windows costs more than the object read itself;
// gitx opens only a store's .git directory and its local bare remote this
// way, whose paths come from git, not from the files it tracks.
type plainFS struct {
	root string
}

var _ billy.Filesystem = plainFS{}

// path is name under root; an absolute name, as TempFile's files report,
// stays as it is.
func (fs plainFS) path(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(fs.root, filepath.FromSlash(name))
}

func (fs plainFS) Create(name string) (billy.File, error) {
	return fs.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

func (fs plainFS) Open(name string) (billy.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs plainFS) OpenFile(name string, flag int, perm os.FileMode) (billy.File, error) {
	p := fs.path(name)
	if flag&os.O_CREATE != 0 {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(p, flag, perm)
	if err != nil {
		return nil, err
	}
	return plainFile{File: f, name: name}, nil
}

func (fs plainFS) Stat(name string) (os.FileInfo, error) { return os.Stat(fs.path(name)) }

func (fs plainFS) Rename(from, to string) error {
	t := fs.path(to)
	if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
		return err
	}
	return renameOver(fs.path(from), t)
}

func (fs plainFS) Remove(name string) error { return os.Remove(fs.path(name)) }

func (fs plainFS) Join(elem ...string) string { return filepath.Join(elem...) }

func (fs plainFS) TempFile(dir, prefix string) (billy.File, error) {
	d := fs.path(dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(d, prefix)
	if err != nil {
		return nil, err
	}
	return plainFile{File: f, name: f.Name()}, nil
}

func (fs plainFS) ReadDir(name string) ([]os.FileInfo, error) {
	ents, err := os.ReadDir(fs.path(name))
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(ents))
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (fs plainFS) MkdirAll(name string, perm os.FileMode) error {
	return os.MkdirAll(fs.path(name), perm)
}

// Chmod lets go-git leave loose objects read-only, as the git program does.
func (fs plainFS) Chmod(name string, mode os.FileMode) error { return os.Chmod(fs.path(name), mode) }

func (fs plainFS) Lstat(name string) (os.FileInfo, error) { return os.Lstat(fs.path(name)) }

func (fs plainFS) Symlink(target, link string) error { return os.Symlink(target, fs.path(link)) }

func (fs plainFS) Readlink(link string) (string, error) { return os.Readlink(fs.path(link)) }

func (fs plainFS) Chroot(name string) (billy.Filesystem, error) {
	return plainFS{root: fs.path(name)}, nil
}

func (fs plainFS) Root() string { return fs.root }

// plainFile is an os.File that reports the name it was opened with. Lock
// and Unlock do nothing: go-git takes them only around its own packed-refs
// and ref writes, which gitx does not use (writeRef and CommitAll take the
// git program's lock files instead).
type plainFile struct {
	*os.File
	name string
}

func (f plainFile) Name() string  { return f.name }
func (f plainFile) Lock() error   { return nil }
func (f plainFile) Unlock() error { return nil }
