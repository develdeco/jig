package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- helpers --------------------------------------------------------------------

// writeMediaFile writes size bytes of a repeating pattern at dir/name and
// returns its sha256 hex.
func writeMediaFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i%251) + 1
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeSparseFile makes dir/name exactly size bytes without writing them.
func writeSparseFile(t *testing.T, dir, name string, size int64) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Fatalf("truncate %s: %v", name, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", name, err)
	}
}

// linkFileOrSkip makes link a symlink to the file target, skipping the test
// when this platform or account cannot.
func linkFileOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
}

// makeDirLink makes link a link to the directory target: a symlink, or on
// Windows, where one needs a privilege, a junction, which Go's Lstat reads
// as ModeIrregular rather than a symlink. It reports why it cannot.
func makeDirLink(target, link string) error {
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			return fmt.Errorf("mklink /J: %v %s", err, out)
		}
		return nil
	}
	return os.Symlink(target, link)
}

// linkDirOrSkip is makeDirLink, skipping the test when it cannot.
func linkDirOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := makeDirLink(target, link); err != nil {
		t.Skip(err)
	}
}

// listing is a session's listing of entries, each with a caption derived from
// its name.
func listing(entries ...string) []Listed {
	listed := []Listed{}
	for _, e := range entries {
		listed = append(listed, Listed{File: e, Caption: "caption of " + e})
	}
	return listed
}

// verifyFresh is Verify, as "media_dir", for a directory that is, as far as
// the test is concerned, the directory jig made: its identity is read right
// now. A path with nothing there has no identity, and Verify refuses it
// before it looks for one.
func verifyFresh(dir string, listed []Listed) ([]File, error) {
	made, _ := LstatPinned(dir)
	return Verify(dir, "media_dir", made, listed)
}

// wantRefusal fails unless Verify refuses listed in dir with a reason
// containing every one of parts.
func wantRefusal(t *testing.T, dir string, listed []Listed, parts ...string) {
	t.Helper()
	files, err := verifyFresh(dir, listed)
	if err == nil {
		t.Fatalf("Verify accepted %+v, want a refusal", files)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("refusal = %q, want it to contain %q", err, p)
		}
	}
}

// wslMountSpelling is how a session that runs in WSL spells the host path p:
// under /mnt, with the drive letter lowercased and forward slashes (a path
// with no drive gets the letter c, so the spelling is the same shape on every
// platform).
func wslMountSpelling(p string) string {
	vol := filepath.VolumeName(p)
	letter := strings.ToLower(strings.TrimSuffix(vol, ":"))
	if letter == "" {
		letter = "c"
	}
	return "/mnt/" + letter + filepath.ToSlash(strings.TrimPrefix(p, vol))
}

// listedSpellings is every way a session may hand back the file name inside
// dir: the absolute path as jig printed it, with forward slashes, in the WSL
// spelling a herdr session on Windows was told, and one that climbs out with
// dot dot. The name is the same in each, and only the name may be printed.
func listedSpellings(dir, name string) map[string]string {
	abs := filepath.Join(dir, name)
	return map[string]string{
		"an absolute path":            abs,
		"an absolute path, forwarded": filepath.ToSlash(abs),
		"a WSL mount path":            wslMountSpelling(abs),
		"a path that climbs out":      "../../" + filepath.Base(dir) + "/" + name,
	}
}

// --- the limits and the small classifiers -----------------------------------------

func TestLimitsAreWhatGHAttachAccepts(t *testing.T) {
	t.Parallel()
	if got, want := fmt.Sprint(ImageExtensions()), "[png jpg jpeg gif webp svg]"; got != want {
		t.Errorf("ImageExtensions() = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(VideoExtensions()), "[mp4 mov webm]"; got != want {
		t.Errorf("VideoExtensions() = %s, want %s", got, want)
	}
	if MaxImageBytes != 10<<20 || MaxVideoBytes != 100<<20 || MaxFiles != 50 {
		t.Errorf("limits = %d, %d, %d; want 10 MiB, 100 MiB and 50 files", MaxImageBytes, MaxVideoBytes, MaxFiles)
	}
}

// TestExtensionListsAreCopies: a caller that edits what it was given cannot
// change what is accepted.
func TestExtensionListsAreCopies(t *testing.T) {
	t.Parallel()
	images, videos := ImageExtensions(), VideoExtensions()
	images[0], videos[0] = "exe", "exe"
	if Kind("exe") != "" || Kind("png") != "image" || Kind("mp4") != "video" {
		t.Errorf("editing the returned lists changed what is accepted: exe=%q png=%q mp4=%q", Kind("exe"), Kind("png"), Kind("mp4"))
	}
	if ImageExtensions()[0] != "png" || VideoExtensions()[0] != "mp4" {
		t.Errorf("the lists changed: %v %v", ImageExtensions(), VideoExtensions())
	}
}

func TestKindClassifiesALowercaseExtension(t *testing.T) {
	t.Parallel()
	for ext, want := range map[string]string{
		"png": "image", "jpg": "image", "jpeg": "image", "gif": "image", "webp": "image", "svg": "image",
		"mp4": "video", "mov": "video", "webm": "video",
		"": "", "txt": "", "avi": "", "PNG": "", "png.exe": "",
	} {
		if got := Kind(ext); got != want {
			t.Errorf("Kind(%q) = %q, want %q", ext, got, want)
		}
	}
}

func TestPlainNameIsOneFileNameAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a.png", "UPPER.PNG", "shot 1.png", ".hidden.png"} {
		if !plainName(name) {
			t.Errorf("plainName(%q) = false, want true", name)
		}
	}
	bad := []string{"", ".", "..", "a/b.png", `a\b.png`, "../a.png", `..\a.png`, "/a.png", "C:a.png", "a.png:stream", "a\x00.png"}
	if runtime.GOOS == "windows" {
		bad = append(bad, "NUL")
	}
	for _, name := range bad {
		if plainName(name) {
			t.Errorf("plainName(%q) = true, want false", name)
		}
	}
}

// TestEntryLabelNamesAnEntryByIndexAndBase: a label is part of a refusal
// reason, so it names the entry by its index and the last element of what was
// listed, and never repeats the string.
func TestEntryLabelNamesAnEntryByIndexAndBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, listed := range listedSpellings(dir, "shot.png") {
		got := EntryLabel(1, listed)
		if want := `media entry 1 ("shot.png")`; got != want {
			t.Errorf("%s: EntryLabel = %q, want %q", name, got, want)
		}
	}
}

// --- Verify ---------------------------------------------------------------------

// TestVerifyNamesTheDirectoryAsTheCallerCallsIt: a refusal calls the
// directory by the name the caller gives for it, never by a name of its own.
func TestVerifyNamesTheDirectoryAsTheCallerCallsIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	other := t.TempDir()
	writeMediaFile(t, dir, "a.png", 10)
	made, err := LstatPinned(dir)
	if err != nil {
		t.Fatal(err)
	}
	otherMade, err := LstatPinned(other)
	if err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone")

	for _, tc := range []struct {
		name   string
		dir    string
		made   os.FileInfo
		listed []Listed
		want   string
	}{
		{"a directory that cannot be read", gone, made, listing("a.png"), "shots cannot be read"},
		{"a directory that is not the one made", dir, otherMade, listing("a.png"), "shots is not the directory jig made (it, or a directory above it, was replaced)"},
		{"a name that is not plain", dir, made, listing("../a.png"), "is not a plain file name directly inside shots"},
		{"a file that is not there", dir, made, listing("b.png"), `"b.png" is not in shots`},
	} {
		_, err := Verify(tc.dir, "shots", tc.made, tc.listed)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Verify = %v, want a refusal containing %q", tc.name, err, tc.want)
			continue
		}
		if strings.Contains(err.Error(), "media_dir") {
			t.Errorf("%s: refusal = %q calls the directory media_dir, not the caller's name for it", tc.name, err)
		}
	}
}

// TestVerifyRefusesTooManyFiles: exactly MaxFiles files are accepted and one
// more refuses the whole listing.
func TestVerifyRefusesTooManyFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var names []string
	for i := 0; i < MaxFiles+1; i++ {
		name := fmt.Sprintf("f%02d.png", i)
		writeMediaFile(t, dir, name, 5)
		names = append(names, name)
	}
	if _, err := verifyFresh(dir, listing(names[:MaxFiles]...)); err != nil {
		t.Fatalf("exactly 50 files: %v", err)
	}
	wantRefusal(t, dir, listing(names...), "51 files are listed", "at most 50")
}

func TestVerifyAcceptsEveryAllowedType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	names := []string{"a.png", "b.jpg", "c.jpeg", "d.gif", "e.webp", "f.svg", "g.mp4", "h.mov", "i.webm", "UPPER.PNG"}
	want := map[string]string{}
	for _, n := range names {
		want[n] = writeMediaFile(t, dir, n, 100+len(n))
	}
	files, err := verifyFresh(dir, listing(names...))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(files) != len(names) {
		t.Fatalf("accepted %d files, want %d", len(files), len(names))
	}
	for i, f := range files {
		if f.Name != names[i] {
			t.Errorf("file %d = %q, want the listed order %q", i, f.Name, names[i])
		}
		if f.SHA256 != want[f.Name] || f.Size != int64(100+len(f.Name)) {
			t.Errorf("%s: sha256 %s size %d, want %s size %d", f.Name, f.SHA256, f.Size, want[f.Name], 100+len(f.Name))
		}
		if f.Caption != "caption of "+f.Name {
			t.Errorf("%s: caption %q", f.Name, f.Caption)
		}
	}
	if files[len(files)-1].Ext != "png" {
		t.Errorf("the extension of UPPER.PNG = %q, want it lowercased", files[len(files)-1].Ext)
	}
}

func TestVerifyAcceptsNothingListed(t *testing.T) {
	t.Parallel()
	files, err := verifyFresh(t.TempDir(), listing())
	if err != nil || len(files) != 0 {
		t.Fatalf("Verify of an empty list = %v, %v; want no files and no error", files, err)
	}
}

// TestVerifyRefusesAFileOutsideMediaDir: a listed name is a plain
// file name directly inside media_dir, never a path that reaches elsewhere.
func TestVerifyRefusesAFileOutsideMediaDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "media")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, root, "outside.png", 10)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, filepath.Join(dir, "sub"), "inner.png", 10)
	abs := filepath.Join(root, "outside.png")

	for _, name := range []string{
		"../outside.png",
		"..\\outside.png",
		"sub/inner.png",
		"sub\\inner.png",
		"./x.png",
		abs,
		"C:\\outside.png",
		"C:outside.png",
		"outside.png:stream",
		"..",
		".",
	} {
		wantRefusal(t, dir, listing(name), "not a plain file name directly inside media_dir")
	}
}

// TestVerifyNamesAnEntryThatIsNotAPlainNameByIndexAndBase: the
// refusal for a listed name that is not a plain file name is jig's own text,
// committed to the store, and the session may have listed the file by its full
// path, in a spelling its backend gave it (the WSL mount of a Windows path, for
// one) that jig has no way to know. It names the entry by its index and the
// last element of the string, and never repeats the string.
func TestVerifyNamesAnEntryThatIsNotAPlainNameByIndexAndBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "ok.png", 10)
	writeMediaFile(t, dir, "shot.png", 10)

	for name, listed := range listedSpellings(dir, "shot.png") {
		wantRefusal(t, dir, listing("ok.png", listed), `media entry 1 ("shot.png") is not a plain file name directly inside media_dir`)

		_, err := verifyFresh(dir, listing("ok.png", listed))
		if strings.Contains(err.Error(), listed) || strings.Contains(err.Error(), filepath.Dir(listed)) {
			t.Errorf("%s: refusal = %q repeats what the session listed", name, err)
		}
	}
}

// TestVerifyRefusesASubdirectory: a directory is not a media file,
// even one named like one.
func TestVerifyRefusesASubdirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "shots.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantRefusal(t, dir, listing("shots.png"), `"shots.png"`, "not a regular file")
}

// TestVerifyRefusesALink covers a symlink to a file outside media_dir
// and a link to a directory named like a media file (on Windows a junction,
// which Lstat reads as irregular): neither is followed.
func TestVerifyRefusesALink(t *testing.T) {
	t.Parallel()

	t.Run("file symlink", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "media")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeMediaFile(t, root, "secret.png", 10)
		linkFileOrSkip(t, filepath.Join(root, "secret.png"), filepath.Join(dir, "link.png"))
		wantRefusal(t, dir, listing("link.png"), `"link.png"`, "not a regular file")
	})

	t.Run("directory link", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "media")
		outside := filepath.Join(root, "outside")
		for _, d := range []string{dir, outside} {
			if err := os.Mkdir(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		linkDirOrSkip(t, outside, filepath.Join(dir, "shots.png"))
		wantRefusal(t, dir, listing("shots.png"), `"shots.png"`, "not a regular file")
	})
}

// TestVerifyRefusesALinkedMediaDir: media_dir itself must be the real
// directory jig made, not a link a session swapped in.
func TestVerifyRefusesALinkedMediaDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, real, "a.png", 10)
	link := filepath.Join(root, "link")
	linkDirOrSkip(t, real, link)
	wantRefusal(t, link, listing("a.png"), "media_dir is not a plain directory")
}

func TestVerifyRefusesAMissingMediaDir(t *testing.T) {
	t.Parallel()
	wantRefusal(t, filepath.Join(t.TempDir(), "gone"), listing("a.png"), "media_dir cannot be read")
}

// TestVerifyRefusesADirectoryAboveMediaDirSwappedForALink: a plain
// directory check follows every parent of media_dir, so a directory above it
// that a session replaced with a link (on Windows a junction) would carry the
// demo, and jig's rename after it, outside the evidence tree. media_dir must
// be the directory jig made, whatever spelling now reaches it.
func TestVerifyRefusesADirectoryAboveMediaDirSwappedForALink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := filepath.Join(root, "evidence")
	dir := filepath.Join(parent, "head")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	made, err := LstatPinned(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, dir, "a.png", 10)
	// The control reads its own identity: comparing made would settle its id
	// early, before the swap the test is about.
	if files, err := verifyFresh(dir, listing("a.png")); err != nil || len(files) != 1 {
		t.Fatalf("the directory jig made, unswapped: %+v, %v; want it accepted", files, err)
	}

	// Somewhere else, a tree of the same shape holds a file that would pass
	// every other check.
	elsewhere := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(filepath.Join(elsewhere, "head"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, filepath.Join(elsewhere, "head"), "a.png", 10)
	if err := os.Rename(parent, parent+".moved"); err != nil {
		t.Fatal(err)
	}
	linkDirOrSkip(t, elsewhere, parent)

	files, err := Verify(dir, "media_dir", made, listing("a.png"))
	if err == nil {
		t.Fatalf("accepted %+v through a swapped parent, want a refusal", files)
	}
	if !strings.Contains(err.Error(), "not the directory jig made") {
		t.Fatalf("refusal = %q, want it to say media_dir is not the directory jig made", err)
	}
}

// TestVerifyRefusesAnEmptyFile: gh --attach refuses an empty file,
// so a demo that recorded one would fail at publish. One byte is not empty.
func TestVerifyRefusesAnEmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "empty.png", 0)
	writeMediaFile(t, dir, "empty.mp4", 0)
	writeMediaFile(t, dir, "one.png", 1)
	wantRefusal(t, dir, listing("empty.png"), `"empty.png" is empty`)
	wantRefusal(t, dir, listing("one.png", "empty.mp4"), `"empty.mp4" is empty`)
	if files, err := verifyFresh(dir, listing("one.png")); err != nil || len(files) != 1 || files[0].Size != 1 {
		t.Fatalf("a file of one byte: %+v, %v; want it accepted", files, err)
	}
}

func TestVerifyRefusesABadExtension(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"notes.txt", "clip.avi", "readme", "x.png.exe", "x.pngg", "x."} {
		writeMediaFile(t, dir, name, 10)
		wantRefusal(t, dir, listing(name), fmt.Sprintf("%q", name), "not an allowed image")
	}
}

func TestVerifyRefusesAMissingFile(t *testing.T) {
	t.Parallel()
	wantRefusal(t, t.TempDir(), listing("gone.png"), `"gone.png" is not in media_dir`)
}

func TestVerifyRefusesAFileListedTwice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "a.png", 10)
	wantRefusal(t, dir, listing("a.png", "a.png"), `"a.png" is listed more than once`)
	// The same file under another case is the same file on a case-insensitive
	// filesystem, so it is one listing too.
	wantRefusal(t, dir, listing("a.png", "A.PNG"), `"A.PNG" is listed more than once`)
}

// TestVerifyRefusesAnOversizeFile pins each kind's limit at its
// boundary: an image at exactly 10 MiB is accepted and one byte more is
// refused; a video is held to 100 MiB, not the image limit.
func TestVerifyRefusesAnOversizeFile(t *testing.T) {
	t.Parallel()

	t.Run("image", func(t *testing.T) {
		dir := t.TempDir()
		writeSparseFile(t, dir, "fits.png", 10<<20)
		if _, err := verifyFresh(dir, listing("fits.png")); err != nil {
			t.Fatalf("an image of exactly 10 MiB: %v", err)
		}
		writeSparseFile(t, dir, "big.png", 10<<20+1)
		wantRefusal(t, dir, listing("big.png"), `"big.png"`, "10485761 bytes", "image", "at most 10485760")
	})

	t.Run("video", func(t *testing.T) {
		dir := t.TempDir()
		writeSparseFile(t, dir, "fits.mp4", 11<<20)
		if _, err := verifyFresh(dir, listing("fits.mp4")); err != nil {
			t.Fatalf("a video of 11 MiB, over the image limit: %v", err)
		}
		writeSparseFile(t, dir, "big.webm", 100<<20+1)
		wantRefusal(t, dir, listing("big.webm"), `"big.webm"`, "104857601 bytes", "video", "at most 104857600")
	})
}

// TestVerifyNeverAcceptsPartially: one bad file in the middle refuses
// the whole result, and nothing has been renamed or removed by then.
func TestVerifyNeverAcceptsPartially(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "a.png", 10)
	writeMediaFile(t, dir, "notes.txt", 10)
	writeMediaFile(t, dir, "c.png", 10)
	files, err := verifyFresh(dir, listing("a.png", "notes.txt", "c.png"))
	if err == nil {
		t.Fatalf("accepted %+v", files)
	}
	if files != nil {
		t.Errorf("a refused result still returned %d files", len(files))
	}
	for _, n := range []string{"a.png", "notes.txt", "c.png"} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s was touched by a refused verification: %v", n, err)
		}
	}
}

// TestPlainParentsRefusesALinkedStoreIDOrTicketDirectory: the
// directories jig made above a head's media_dir must be plain directories
// before an attempt clears or makes anything below them; ones that do not
// exist yet are fine, and so is a link at the head itself, which an attempt
// removes as itself.
func TestPlainParentsRefusesALinkedStoreIDOrTicketDirectory(t *testing.T) {
	t.Parallel()
	newTree := func(t *testing.T) (root, mediaDir string) {
		t.Helper()
		root = t.TempDir()
		mediaDir = filepath.Join(root, "evidence", "id", "JIG-1", "head")
		return root, mediaDir
	}

	t.Run("nothing made yet", func(t *testing.T) {
		root, mediaDir := newTree(t)
		if err := PlainParents(filepath.Join(root, "evidence"), mediaDir); err != nil {
			t.Fatalf("PlainParents with nothing made: %v", err)
		}
	})
	t.Run("plain directories", func(t *testing.T) {
		root, mediaDir := newTree(t)
		if err := os.MkdirAll(mediaDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := PlainParents(filepath.Join(root, "evidence"), mediaDir); err != nil {
			t.Fatalf("PlainParents with plain directories: %v", err)
		}
	})
	for _, level := range []struct{ name, rel string }{
		{"store id directory", filepath.Join("evidence", "id")},
		{"ticket directory", filepath.Join("evidence", "id", "JIG-1")},
	} {
		level := level
		t.Run("a linked "+level.name, func(t *testing.T) {
			root, mediaDir := newTree(t)
			elsewhere := filepath.Join(root, "elsewhere")
			if err := os.MkdirAll(filepath.Join(elsewhere, "JIG-1", "head"), 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, level.rel)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			linkDirOrSkip(t, elsewhere, link)
			err := PlainParents(filepath.Join(root, "evidence"), mediaDir)
			if err == nil || !strings.Contains(err.Error(), "is not a plain directory") {
				t.Fatalf("PlainParents through a linked %s = %v, want a refusal", level.name, err)
			}
			if strings.Contains(err.Error(), root) {
				t.Errorf("the refusal names a host path: %v", err)
			}
		})
	}
	t.Run("a link at the head is cleared as itself, not refused here", func(t *testing.T) {
		root, mediaDir := newTree(t)
		if err := os.MkdirAll(filepath.Dir(mediaDir), 0o755); err != nil {
			t.Fatal(err)
		}
		linkDirOrSkip(t, root, mediaDir)
		if err := PlainParents(filepath.Join(root, "evidence"), mediaDir); err != nil {
			t.Fatalf("PlainParents with a link at the head: %v", err)
		}
	})
}

// TestPlainParentsChecksEveryDirectoryBetweenTopAndDir: the check is not tied
// to a layout. In a deeper one (the recordings of a ticket sit one directory
// lower than a head's demo media), each directory strictly between top and dir
// must be plain, a missing one ends the check, and neither top nor dir is
// examined; a dir that is not below top is refused.
func TestPlainParentsChecksEveryDirectoryBetweenTopAndDir(t *testing.T) {
	t.Parallel()
	newTree := func(t *testing.T) (top, dir string) {
		t.Helper()
		top = filepath.Join(t.TempDir(), "evidence")
		return top, filepath.Join(top, "id", "JIG-1", "recordings", "head")
	}

	t.Run("nothing made yet", func(t *testing.T) {
		top, dir := newTree(t)
		if err := PlainParents(top, dir); err != nil {
			t.Fatalf("PlainParents with nothing made: %v", err)
		}
	})
	t.Run("plain directories", func(t *testing.T) {
		top, dir := newTree(t)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := PlainParents(top, dir); err != nil {
			t.Fatalf("PlainParents with plain directories: %v", err)
		}
	})
	t.Run("a directory that is not made yet ends the check", func(t *testing.T) {
		top, dir := newTree(t)
		if err := os.MkdirAll(filepath.Join(top, "id"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := PlainParents(top, dir); err != nil {
			t.Fatalf("PlainParents with only the store id directory made: %v", err)
		}
	})
	for _, rel := range []string{
		"id",
		filepath.Join("id", "JIG-1"),
		filepath.Join("id", "JIG-1", "recordings"),
	} {
		rel := rel
		t.Run("a link at "+rel, func(t *testing.T) {
			top, dir := newTree(t)
			elsewhere := filepath.Join(filepath.Dir(top), "elsewhere")
			if err := os.MkdirAll(elsewhere, 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(top, rel)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			linkDirOrSkip(t, elsewhere, link)
			err := PlainParents(top, dir)
			if err == nil || !strings.Contains(err.Error(), "is not a plain directory") {
				t.Fatalf("PlainParents through a link at %s = %v, want a refusal", rel, err)
			}
			if strings.Contains(err.Error(), filepath.Dir(top)) {
				t.Errorf("the refusal names a host path: %v", err)
			}
		})
	}
	t.Run("a file where a directory belongs", func(t *testing.T) {
		top, dir := newTree(t)
		if err := os.MkdirAll(filepath.Join(top, "id"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(top, "id", "JIG-1"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := PlainParents(top, dir); err == nil || !strings.Contains(err.Error(), "is not a plain directory") {
			t.Fatalf("PlainParents through a file = %v, want a refusal", err)
		}
	})
	t.Run("top itself is not examined", func(t *testing.T) {
		top, dir := newTree(t)
		elsewhere := filepath.Join(filepath.Dir(top), "elsewhere")
		if err := os.MkdirAll(elsewhere, 0o755); err != nil {
			t.Fatal(err)
		}
		linkDirOrSkip(t, elsewhere, top)
		if err := PlainParents(top, dir); err != nil {
			t.Fatalf("PlainParents with a link at top: %v", err)
		}
	})
	t.Run("a dir that is not below top", func(t *testing.T) {
		top, _ := newTree(t)
		for _, dir := range []string{filepath.Join(filepath.Dir(top), "other", "x", "y"), filepath.Dir(top)} {
			if err := PlainParents(top, dir); err == nil {
				t.Errorf("PlainParents(%q, %q) = nil, want a refusal", top, dir)
			}
		}
	})
}

// TestHashRegularFileRefusesAFileThatChanged: the hash is of the very file
// Lstat described. A file that grew or shrank since, or is another file than
// the one Lstat saw, is refused instead of hashed, so a session that left a
// process running cannot slip different bytes in between the check and the read.
func TestHashRegularFileRefusesAFileThatChanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := writeMediaFile(t, dir, "a.png", 10)
	path := filepath.Join(dir, "a.png")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := HashRegularFile(path, info); err != nil || got != want {
		t.Fatalf("HashRegularFile of an unchanged file = %q, %v; want %q", got, err, want)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("grown"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := HashRegularFile(path, info); err == nil || !strings.Contains(err.Error(), "changed size") {
		t.Errorf("HashRegularFile of a file that grew = %q, %v; want a changed-size refusal", got, err)
	}

	if err := os.Truncate(path, 3); err != nil {
		t.Fatal(err)
	}
	if got, err := HashRegularFile(path, info); err == nil || !strings.Contains(err.Error(), "changed size") {
		t.Errorf("HashRegularFile of a file that shrank = %q, %v; want a changed-size refusal", got, err)
	}

	writeMediaFile(t, dir, "b.png", 10)
	other, err := os.Lstat(filepath.Join(dir, "b.png"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := HashRegularFile(path, other); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Errorf("HashRegularFile of another file than Lstat saw = %q, %v; want a refusal", got, err)
	}
}

// TestHashRegularFileRefusesAFileSwappedAfterLstat: a file replaced by another
// between the Lstat and the read is refused whatever the platform. On Windows
// that holds only if the Lstat's file id was read when it was taken.
func TestHashRegularFileRefusesAFileSwappedAfterLstat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.png")
	writeMediaFile(t, dir, "a.png", 10)
	info, err := LstatPinned(path)
	if err != nil {
		t.Fatal(err)
	}
	// The old file is kept, so the new one cannot reuse its inode.
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, dir, "a.png", 10)
	if got, err := HashRegularFile(path, info); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("HashRegularFile of a swapped file = %q, %v; want a refusal", got, err)
	}
}
