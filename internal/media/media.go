// Package media holds what jig accepts of the media it attaches to a pull
// request: the file types and sizes `gh ... --attach` accepts, and the checks
// that make a file a session wrote into a directory jig made one a later
// publish can attach - a real, non-empty, regular file of an allowed type and
// size, hashed from the very file that was checked. It is a leaf over the
// standard library, so the packages that record, drive and render media can
// all use it without importing one another.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The limits media are held to: the types and sizes `gh ... --attach`
// accepts, so a file jig records is one a later publish can attach. Images
// are at most 10 MiB, videos at most 100 MiB, a file is never empty (gh
// refuses one), and a listing has at most 50 files.
const (
	MaxImageBytes = 10 << 20
	MaxVideoBytes = 100 << 20
	MaxFiles      = 50
)

var (
	imageExts = []string{"png", "jpg", "jpeg", "gif", "webp", "svg"}
	videoExts = []string{"mp4", "mov", "webm"}
)

// ImageExtensions returns the lowercase extensions accepted as images.
func ImageExtensions() []string { return append([]string{}, imageExts...) }

// VideoExtensions returns the lowercase extensions accepted as videos.
func VideoExtensions() []string { return append([]string{}, videoExts...) }

// Listed is one file a session reports: a name inside the media directory and
// the caption a reader sees beside it.
type Listed struct {
	File    string
	Caption string
}

// File is one listed file that passed verification, still under the session's
// own name. Ext is its lowercase extension without the dot.
type File struct {
	Name    string
	Ext     string
	Size    int64
	SHA256  string
	Caption string
}

// Kind classifies a lowercase extension: "image", "video", or "" for a type
// that is not accepted.
func Kind(ext string) string {
	for _, e := range imageExts {
		if e == ext {
			return "image"
		}
	}
	for _, e := range videoExts {
		if e == ext {
			return "video"
		}
	}
	return ""
}

// PlainName reports whether name is a single file name: no path separator
// (either spelling), no drive or stream colon, not a dot name, and - via
// filepath.IsLocal - not a Windows reserved device name.
func PlainName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") {
		return false
	}
	return filepath.IsLocal(name)
}

// EntryLabel names the entry at index i of a session's listing, listed as
// file, in a refusal reason: by its index and the last element of file, never
// by file itself. The session was told the media directory's absolute path, so
// it may list a file by a full path, in whatever spelling its backend gave it
// (raw, forward-slash, a WSL mount), and a reason is committed to the store
// and printed. jig cannot know every spelling to leave out, so it does not
// repeat a path it did not choose.
func EntryLabel(i int, file string) string {
	return fmt.Sprintf("media entry %d (%q)", i, filepath.Base(file))
}

// LstatPinned is os.Lstat with the file's identity read now. On Windows a
// FileInfo from Lstat holds no file id: os.SameFile opens its path to read
// one the first time it compares, so an identity captured before a swap
// would be read after it, through whatever then sits at that path, and every
// comparison would pass. Comparing the info with itself makes it load the id.
func LstatPinned(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, info) {
		return nil, fmt.Errorf("the identity of %s cannot be read", path)
	}
	return info, nil
}

// PlainParents refuses a media directory reached through a store-id or ticket
// directory that is a link or a junction, before anything is cleared or made
// below it. jig made those directories, so a link in their place is one a
// session swapped in. Verify's identity check catches a swap made during its
// own attempt, but one that stays would send the next attempt's clearing,
// mkdir and media through it, so it is refused before that. A directory that
// does not exist yet is fine, since MkdirAll makes it plain. The head
// directory itself is not checked: an attempt removes a link there as itself.
func PlainParents(mediaDir string) error {
	ticketDir := filepath.Dir(mediaDir)
	for _, p := range []string{filepath.Dir(ticketDir), ticketDir} {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("evidence directory %q cannot be read: %w", filepath.Base(p), err)
		}
		if info.Mode().Type() != os.ModeDir {
			return fmt.Errorf("evidence directory %q is not a plain directory (a link or junction is refused, and left for the operator to remove)", filepath.Base(p))
		}
	}
	return nil
}

// Verify checks every listed file against what may be recorded, and refuses
// the whole listing on the first that fails one: at most MaxFiles files; dir
// itself a real directory (never a link or a junction a session swapped in)
// and the very directory jig made (made, pinned by LstatPinned when it was
// made), so a directory above it swapped for a link cannot carry the media
// outside the evidence tree; each name a plain file name listed once; an
// allowed extension; a regular file, decided by Lstat, so a symlink or a
// Windows junction (which reads as irregular) is never followed; not empty
// (gh refuses an empty file); and within the size limit for its kind. The
// bytes are hashed from the very file Lstat saw. Nothing is renamed here.
// dirName is what a refusal calls dir, the name the session was told for it
// (demo.json's "media_dir"), since dir itself is a path of this machine.
func Verify(dir, dirName string, made os.FileInfo, listed []Listed) ([]File, error) {
	if len(listed) > MaxFiles {
		return nil, fmt.Errorf("%d files are listed; at most %d are accepted", len(listed), MaxFiles)
	}
	dirInfo, err := LstatPinned(dir)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", dirName, err)
	}
	if dirInfo.Mode().Type() != os.ModeDir {
		return nil, fmt.Errorf("%s is not a plain directory (a link or junction is refused)", dirName)
	}
	if !os.SameFile(made, dirInfo) {
		return nil, fmt.Errorf("%s is not the directory jig made (it, or a directory above it, was replaced)", dirName)
	}

	seen := map[string]bool{}
	files := make([]File, 0, len(listed))
	for i, m := range listed {
		if !PlainName(m.File) {
			return nil, fmt.Errorf("%s is not a plain file name directly inside %s", EntryLabel(i, m.File), dirName)
		}
		key := strings.ToLower(m.File)
		if seen[key] {
			return nil, fmt.Errorf("file %q is listed more than once", m.File)
		}
		seen[key] = true

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(m.File), "."))
		kind := Kind(ext)
		if kind == "" {
			return nil, fmt.Errorf("file %q has a type that is not an allowed image (%s) or video (%s) type", m.File, strings.Join(imageExts, ", "), strings.Join(videoExts, ", "))
		}
		limit := int64(MaxImageBytes)
		if kind == "video" {
			limit = MaxVideoBytes
		}

		path := filepath.Join(dir, m.File)
		info, err := LstatPinned(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("file %q is not in %s", m.File, dirName)
			}
			return nil, fmt.Errorf("file %q cannot be read: %w", m.File, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("file %q is not a regular file (a directory, link or junction is refused)", m.File)
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("file %q is empty", m.File)
		}
		if info.Size() > limit {
			return nil, fmt.Errorf("file %q is %d bytes; a %s may be at most %d", m.File, info.Size(), kind, limit)
		}
		sum, err := HashRegularFile(path, info)
		if err != nil {
			return nil, fmt.Errorf("file %q: %w", m.File, err)
		}
		files = append(files, File{Name: m.File, Ext: ext, Size: info.Size(), SHA256: sum, Caption: m.Caption})
	}
	return files, nil
}

// HashRegularFile returns the sha256 hex of the file at path, which
// LstatPinned (info) reported as a regular file of that size. It opens the
// file and checks it is the same one and still that size, so a swap or a
// growth between the Lstat and the read is refused instead of hashed.
func HashRegularFile(path string, info os.FileInfo) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", fmt.Errorf("changed while it was being checked")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, info.Size()+1))
	if err != nil {
		return "", err
	}
	if n != info.Size() {
		return "", fmt.Errorf("changed size while it was being checked")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
