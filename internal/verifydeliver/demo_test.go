package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/store"
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

// makeFileLink makes link a symlink to the file target, or reports why this
// platform or account cannot.
func makeFileLink(target, link string) error {
	return os.Symlink(target, link)
}

// linkFileOrSkip is makeFileLink, skipping the test when it cannot.
func linkFileOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := makeFileLink(target, link); err != nil {
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

func demoRes(entries ...string) DemoResult {
	res := DemoResult{Media: []DemoMedia{}, Summary: "shown"}
	for _, e := range entries {
		res.Media = append(res.Media, DemoMedia{File: e, Caption: "caption of " + e})
	}
	return res
}

// verifyFresh is verifyDemoMedia for a media_dir that is, as far as the test
// is concerned, the directory jig made: its identity is read right now. A
// path with nothing there has no identity, and verifyDemoMedia refuses it
// before it looks for one.
func verifyFresh(dir string, res DemoResult) ([]demoFile, error) {
	made, _ := lstatPinned(dir)
	return verifyDemoMedia(dir, made, res)
}

// wantDemoRefusal fails unless verifyDemoMedia refuses res in dir with a
// reason containing every one of parts.
func wantDemoRefusal(t *testing.T, dir string, res DemoResult, parts ...string) {
	t.Helper()
	files, err := verifyFresh(dir, res)
	if err == nil {
		t.Fatalf("verifyDemoMedia accepted %+v, want a refusal", files)
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

// --- the contract's wire shapes and prompt --------------------------------------

func TestDemoLimitsAreWhatGHAttachAccepts(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(demoLimits())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"image_extensions": []any{"png", "jpg", "jpeg", "gif", "webp", "svg"},
		"video_extensions": []any{"mp4", "mov", "webm"},
		"max_image_bytes":  float64(10 << 20),
		"max_video_bytes":  float64(100 << 20),
		"max_files":        float64(50),
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("limits = %s, want %s", gotJSON, wantJSON)
	}
}

// TestRenderDemoPromptMatchesGolden pins the prompt to a golden text
// transcribed independently of demoPromptTemplate, like the reviewer's: it
// states the job (show a person reviewing the change that it works, or
// record nothing and say why), the output contract, and what jig verifies,
// and it names no tool and lists no kind of change - a repo documents its own
// demo tooling in its own CLAUDE.md. A golden catches any addition at all.
func TestRenderDemoPromptMatchesGolden(t *testing.T) {
	t.Parallel()

	req := DemoRequest{Ticket: "JIG-1", Round: 2, BaseSHA: "aaa", HeadSHA: "bbb", MediaDir: "/abs/evidence/id/JIG-1/bbb"}
	prompt := RenderDemoPrompt(req, "/abs/demo.json", "/abs/demo.result.json")

	golden := `You are demonstrating round 2 of ticket JIG-1. Your inputs are in demo.json at /abs/demo.json.
Show a person reviewing this change that it works, in whatever form shows it best: the diff aaa..bbb in this worktree, as it is now, against the change's intent. demo.json's intent names it and its source: "brief" or "explicit" is the human's own statement of what was asked for; "inferred" is jig's own summary of the author's own agent session, a hint that may be partial or wrong; "none" means nothing states it. If nothing about it can be shown, record nothing and say why. Do not edit tracked files, commit, or push.
Write every file you produce directly into media_dir (/abs/evidence/id/JIG-1/bbb), with no subdirectories or links. Every file's type and size must be within the limits in demo.json.
When finished, write /abs/demo.result.json with exactly one JSON object: {"media": [{"file": "<name in media_dir>", "caption": "..."}], "summary": "..."}
An empty media list with a summary saying why nothing is visible is a valid result.
jig checks that this worktree's HEAD and tracked files are unchanged, that the result has a non-empty summary and a non-empty caption on every file, and that every listed file is a non-empty regular file directly in media_dir, of an allowed type and size, within the file limit. A result that fails any check is refused whole: jig records why, and records none of your files. Nothing you record changes the review's verdict.`

	if prompt != golden {
		t.Errorf("prompt does not match the golden text.\ngot:\n%s\nwant:\n%s", prompt, golden)
	}
}

// --- ParseDemoResult -------------------------------------------------------------

func TestParseDemoResultAcceptsAValidResult(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   string
		want DemoResult
	}{
		"media": {
			`{"media":[{"file":"a.png","caption":"the form"},{"file":"b.mp4","caption":"the flow"}],"summary":"two files"}`,
			DemoResult{Media: []DemoMedia{{File: "a.png", Caption: "the form"}, {File: "b.mp4", Caption: "the flow"}}, Summary: "two files"},
		},
		"nothing visible": {
			`{"media":[],"summary":"the change is internal; nothing shows"}`,
			DemoResult{Media: []DemoMedia{}, Summary: "the change is internal; nothing shows"},
		},
		"surrounding whitespace": {
			"\n  {\"media\":[],\"summary\":\"s\"}\n",
			DemoResult{Media: []DemoMedia{}, Summary: "s"},
		},
	}
	for name, c := range cases {
		got, err := ParseDemoResult([]byte(c.in))
		if err != nil {
			t.Errorf("%s: ParseDemoResult: %v", name, err)
			continue
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(c.want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("%s: got %s, want %s", name, gotJSON, wantJSON)
		}
	}
}

// TestParseDemoResultRefusesWhatIsNotTheContract is the strict parsing, one
// rule per case: each input breaks exactly one, and each is refused with a
// reason naming it.
func TestParseDemoResultRefusesWhatIsNotTheContract(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, in, reason string }{
		{"empty", ``, "exactly one JSON object"},
		{"an array", `[]`, "exactly one JSON object"},
		{"null", `null`, "exactly one JSON object"},
		{"a string", `"x"`, "exactly one JSON object"},
		{"prose", `here is my result`, "exactly one JSON object"},
		{"two objects", `{"media":[],"summary":"s"}{"media":[],"summary":"s"}`, "not valid JSON"},
		{"a key repeated", `{"media":[],"media":[],"summary":"s"}`, `"media" repeats`},
		{"a key repeated by case", `{"media":[],"Media":[],"summary":"s"}`, `"Media" repeats`},
		{"an entry key repeated", `{"media":[{"file":"a.png","file":"b.png","caption":"c"}],"summary":"s"}`, `"file" repeats`},
		{"an unknown top-level key", `{"media":[],"summary":"s","extra":1}`, `"extra" is not a recognized field`},
		{"a case variant of a key", `{"Media":[],"summary":"s"}`, `"Media" is not a recognized field`},
		{"an unknown entry key", `{"media":[{"file":"a.png","caption":"c","alt":"x"}],"summary":"s"}`, `"alt" is not a recognized field`},
		{"a case variant of an entry key", `{"media":[{"File":"a.png","caption":"c"}],"summary":"s"}`, `"File" is not a recognized field`},
		{"no media key", `{"summary":"s"}`, `missing "media"`},
		{"no summary key", `{"media":[]}`, `missing "summary"`},
		{"media null", `{"media":null,"summary":"s"}`, `"media" must be a list, not null`},
		{"media not a list", `{"media":"a.png","summary":"s"}`, "not valid JSON"},
		{"an entry not an object", `{"media":["a.png"],"summary":"s"}`, "not valid JSON"},
		{"a file that is not a string", `{"media":[{"file":3,"caption":"c"}],"summary":"s"}`, "not valid JSON"},
		{"an empty summary", `{"media":[],"summary":""}`, "empty summary"},
		{"a blank summary", `{"media":[],"summary":"  \n"}`, "empty summary"},
		{"a null summary", `{"media":[],"summary":null}`, "empty summary"},
		{"an empty file", `{"media":[{"file":"","caption":"c"}],"summary":"s"}`, "empty file"},
		{"a blank file", `{"media":[{"file":" ","caption":"c"}],"summary":"s"}`, "empty file"},
		{"no caption", `{"media":[{"file":"a.png"}],"summary":"s"}`, `"a.png") has an empty caption`},
		{"an empty caption", `{"media":[{"file":"a.png","caption":""}],"summary":"s"}`, `"a.png") has an empty caption`},
		{"a blank caption", `{"media":[{"file":"a.png","caption":"\t"}],"summary":"s"}`, `"a.png") has an empty caption`},
	}
	for _, c := range cases {
		res, err := ParseDemoResult([]byte(c.in))
		if err == nil {
			t.Errorf("%s: ParseDemoResult accepted %s as %+v", c.name, c.in, res)
			continue
		}
		if !strings.Contains(err.Error(), "demo.result.json is invalid") || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: error = %q, want an invalid-result error containing %q", c.name, err, c.reason)
		}
	}
}

// TestParseDemoResultNamesAnEntryByIndexAndBase: a refusal reason is jig's own
// text, committed to the store, and the file a session listed may be a full
// path in any spelling. The reason names the entry by its index and the last
// element of what was listed, and never repeats the string.
func TestParseDemoResultNamesAnEntryByIndexAndBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, listed := range listedSpellings(dir, "shot.png") {
		in, err := json.Marshal(DemoResult{Media: []DemoMedia{{File: "ok.png", Caption: "c"}, {File: listed, Caption: " "}}, Summary: "s"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseDemoResult(in)
		if err == nil {
			t.Fatalf("%s: ParseDemoResult accepted an entry with a blank caption", name)
		}
		if want := `media entry 1 ("shot.png") has an empty caption`; !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %q, want it to contain %q", name, err, want)
		}
		if strings.Contains(err.Error(), listed) || strings.Contains(err.Error(), filepath.Dir(listed)) {
			t.Errorf("%s: error = %q repeats what the session listed", name, err)
		}
	}
}

// --- verifyDemoMedia ---------------------------------------------------------------

func TestVerifyDemoMediaAcceptsEveryAllowedType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	names := []string{"a.png", "b.jpg", "c.jpeg", "d.gif", "e.webp", "f.svg", "g.mp4", "h.mov", "i.webm", "UPPER.PNG"}
	want := map[string]string{}
	for _, n := range names {
		want[n] = writeMediaFile(t, dir, n, 100+len(n))
	}
	files, err := verifyFresh(dir, demoRes(names...))
	if err != nil {
		t.Fatalf("verifyDemoMedia: %v", err)
	}
	if len(files) != len(names) {
		t.Fatalf("accepted %d files, want %d", len(files), len(names))
	}
	for i, f := range files {
		if f.name != names[i] {
			t.Errorf("file %d = %q, want the listed order %q", i, f.name, names[i])
		}
		if f.sha256 != want[f.name] || f.size != int64(100+len(f.name)) {
			t.Errorf("%s: sha256 %s size %d, want %s size %d", f.name, f.sha256, f.size, want[f.name], 100+len(f.name))
		}
		if f.caption != "caption of "+f.name {
			t.Errorf("%s: caption %q", f.name, f.caption)
		}
	}
	if files[len(files)-1].ext != "png" {
		t.Errorf("the extension of UPPER.PNG = %q, want it lowercased", files[len(files)-1].ext)
	}
}

func TestVerifyDemoMediaAcceptsNothingListed(t *testing.T) {
	t.Parallel()
	files, err := verifyFresh(t.TempDir(), demoRes())
	if err != nil || len(files) != 0 {
		t.Fatalf("verifyDemoMedia of an empty list = %v, %v; want no files and no error", files, err)
	}
}

// TestVerifyDemoMediaRefusesAFileOutsideMediaDir: a listed name is a plain
// file name directly inside media_dir, never a path that reaches elsewhere.
func TestVerifyDemoMediaRefusesAFileOutsideMediaDir(t *testing.T) {
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
		wantDemoRefusal(t, dir, demoRes(name), "not a plain file name directly inside media_dir")
	}
}

// TestVerifyDemoMediaNamesAnEntryThatIsNotAPlainNameByIndexAndBase: the
// refusal for a listed name that is not a plain file name is jig's own text,
// committed to the store, and the session may have listed the file by its full
// path, in a spelling its backend gave it (the WSL mount of a Windows path, for
// one) that jig has no way to know. It names the entry by its index and the
// last element of the string, and never repeats the string.
func TestVerifyDemoMediaNamesAnEntryThatIsNotAPlainNameByIndexAndBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "ok.png", 10)
	writeMediaFile(t, dir, "shot.png", 10)

	for name, listed := range listedSpellings(dir, "shot.png") {
		wantDemoRefusal(t, dir, demoRes("ok.png", listed), `media entry 1 ("shot.png") is not a plain file name directly inside media_dir`)

		_, err := verifyFresh(dir, demoRes("ok.png", listed))
		if strings.Contains(err.Error(), listed) || strings.Contains(err.Error(), filepath.Dir(listed)) {
			t.Errorf("%s: refusal = %q repeats what the session listed", name, err)
		}
	}
}

// TestVerifyDemoMediaRefusesASubdirectory: a directory is not a media file,
// even one named like one.
func TestVerifyDemoMediaRefusesASubdirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "shots.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantDemoRefusal(t, dir, demoRes("shots.png"), `"shots.png"`, "not a regular file")
}

// TestVerifyDemoMediaRefusesALink covers a symlink to a file outside media_dir
// and a link to a directory named like a media file (on Windows a junction,
// which Lstat reads as irregular): neither is followed.
func TestVerifyDemoMediaRefusesALink(t *testing.T) {
	t.Parallel()

	t.Run("file symlink", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "media")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeMediaFile(t, root, "secret.png", 10)
		linkFileOrSkip(t, filepath.Join(root, "secret.png"), filepath.Join(dir, "link.png"))
		wantDemoRefusal(t, dir, demoRes("link.png"), `"link.png"`, "not a regular file")
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
		wantDemoRefusal(t, dir, demoRes("shots.png"), `"shots.png"`, "not a regular file")
	})
}

// TestVerifyDemoMediaRefusesALinkedMediaDir: media_dir itself must be the real
// directory jig made, not a link a session swapped in.
func TestVerifyDemoMediaRefusesALinkedMediaDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, real, "a.png", 10)
	link := filepath.Join(root, "link")
	linkDirOrSkip(t, real, link)
	wantDemoRefusal(t, link, demoRes("a.png"), "media_dir is not a plain directory")
}

func TestVerifyDemoMediaRefusesAMissingMediaDir(t *testing.T) {
	t.Parallel()
	wantDemoRefusal(t, filepath.Join(t.TempDir(), "gone"), demoRes("a.png"), "media_dir cannot be read")
}

// TestVerifyDemoMediaRefusesADirectoryAboveMediaDirSwappedForALink: a plain
// directory check follows every parent of media_dir, so a directory above it
// that a session replaced with a link (on Windows a junction) would carry the
// demo, and jig's rename after it, outside the evidence tree. media_dir must
// be the directory jig made, whatever spelling now reaches it.
func TestVerifyDemoMediaRefusesADirectoryAboveMediaDirSwappedForALink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := filepath.Join(root, "evidence")
	dir := filepath.Join(parent, "head")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	made, err := lstatPinned(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, dir, "a.png", 10)
	// The control reads its own identity: comparing made would settle its id
	// early, before the swap the test is about.
	if files, err := verifyFresh(dir, demoRes("a.png")); err != nil || len(files) != 1 {
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

	files, err := verifyDemoMedia(dir, made, demoRes("a.png"))
	if err == nil {
		t.Fatalf("accepted %+v through a swapped parent, want a refusal", files)
	}
	if !strings.Contains(err.Error(), "not the directory jig made") {
		t.Fatalf("refusal = %q, want it to say media_dir is not the directory jig made", err)
	}
}

// TestVerifyDemoMediaRefusesAnEmptyFile: gh --attach refuses an empty file,
// so a demo that recorded one would fail at publish. One byte is not empty.
func TestVerifyDemoMediaRefusesAnEmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "empty.png", 0)
	writeMediaFile(t, dir, "empty.mp4", 0)
	writeMediaFile(t, dir, "one.png", 1)
	wantDemoRefusal(t, dir, demoRes("empty.png"), `"empty.png" is empty`)
	wantDemoRefusal(t, dir, demoRes("one.png", "empty.mp4"), `"empty.mp4" is empty`)
	if files, err := verifyFresh(dir, demoRes("one.png")); err != nil || len(files) != 1 || files[0].size != 1 {
		t.Fatalf("a file of one byte: %+v, %v; want it accepted", files, err)
	}
}

func TestVerifyDemoMediaRefusesABadExtension(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"notes.txt", "clip.avi", "readme", "x.png.exe", "x.pngg", "x."} {
		writeMediaFile(t, dir, name, 10)
		wantDemoRefusal(t, dir, demoRes(name), fmt.Sprintf("%q", name), "not an allowed image")
	}
}

func TestVerifyDemoMediaRefusesAMissingFile(t *testing.T) {
	t.Parallel()
	wantDemoRefusal(t, t.TempDir(), demoRes("gone.png"), `"gone.png" is not in media_dir`)
}

func TestVerifyDemoMediaRefusesAFileListedTwice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "a.png", 10)
	wantDemoRefusal(t, dir, demoRes("a.png", "a.png"), `"a.png" is listed more than once`)
	// The same file under another case is the same file on a case-insensitive
	// filesystem, so it is one listing too.
	wantDemoRefusal(t, dir, demoRes("a.png", "A.PNG"), `"A.PNG" is listed more than once`)
}

// TestVerifyDemoMediaRefusesAnOversizeFile pins each kind's limit at its
// boundary: an image at exactly 10 MiB is accepted and one byte more is
// refused; a video is held to 100 MiB, not the image limit.
func TestVerifyDemoMediaRefusesAnOversizeFile(t *testing.T) {
	t.Parallel()

	t.Run("image", func(t *testing.T) {
		dir := t.TempDir()
		writeSparseFile(t, dir, "fits.png", 10<<20)
		if _, err := verifyFresh(dir, demoRes("fits.png")); err != nil {
			t.Fatalf("an image of exactly 10 MiB: %v", err)
		}
		writeSparseFile(t, dir, "big.png", 10<<20+1)
		wantDemoRefusal(t, dir, demoRes("big.png"), `"big.png"`, "10485761 bytes", "image", "at most 10485760")
	})

	t.Run("video", func(t *testing.T) {
		dir := t.TempDir()
		writeSparseFile(t, dir, "fits.mp4", 11<<20)
		if _, err := verifyFresh(dir, demoRes("fits.mp4")); err != nil {
			t.Fatalf("a video of 11 MiB, over the image limit: %v", err)
		}
		writeSparseFile(t, dir, "big.webm", 100<<20+1)
		wantDemoRefusal(t, dir, demoRes("big.webm"), `"big.webm"`, "104857601 bytes", "video", "at most 104857600")
	})
}

func TestVerifyDemoMediaRefusesTooManyFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var names []string
	for i := 0; i < 51; i++ {
		name := fmt.Sprintf("f%02d.png", i)
		writeMediaFile(t, dir, name, 5)
		names = append(names, name)
	}
	if _, err := verifyFresh(dir, demoRes(names[:50]...)); err != nil {
		t.Fatalf("exactly 50 files: %v", err)
	}
	wantDemoRefusal(t, dir, demoRes(names...), "lists 51 files", "at most 50")
}

// TestVerifyDemoMediaNeverAcceptsPartially: one bad file in the middle refuses
// the whole result, and nothing has been renamed or removed by then.
func TestVerifyDemoMediaNeverAcceptsPartially(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "a.png", 10)
	writeMediaFile(t, dir, "notes.txt", 10)
	writeMediaFile(t, dir, "c.png", 10)
	files, err := verifyFresh(dir, demoRes("a.png", "notes.txt", "c.png"))
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

// --- renameDemoMedia ---------------------------------------------------------------

func acceptedFiles(t *testing.T, dir string, names ...string) []demoFile {
	t.Helper()
	files, err := verifyFresh(dir, demoRes(names...))
	if err != nil {
		t.Fatalf("verifyDemoMedia: %v", err)
	}
	return files
}

func TestRenameDemoMediaNamesFilesInOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	shaB := writeMediaFile(t, dir, "before-fix.gif", 30)
	shaA := writeMediaFile(t, dir, "Shot.PNG", 20)
	got, err := renameDemoMedia(dir, acceptedFiles(t, dir, "before-fix.gif", "Shot.PNG"))
	if err != nil {
		t.Fatalf("renameDemoMedia: %v", err)
	}
	want := []DemoFile{
		{Name: "demo-1.gif", SHA256: shaB, Size: 30, Caption: "caption of before-fix.gif"},
		{Name: "demo-2.png", SHA256: shaA, Size: 20, Caption: "caption of Shot.PNG"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("recorded %+v, want %+v", got, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if fmt.Sprint(names) != "[demo-1.gif demo-2.png]" {
		t.Fatalf("media_dir holds %v, want exactly [demo-1.gif demo-2.png]", names)
	}
	// The bytes moved with their names.
	for _, f := range got {
		data, err := os.ReadFile(filepath.Join(dir, f.Name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			t.Errorf("%s holds different bytes than the recorded sha256", f.Name)
		}
	}
}

// TestRenameDemoMediaSurvivesASessionThatUsedTheFinalNames: a session that
// already called its files demo-2 and demo-1, in that order, must not have
// one rename overwrite the other's source.
func TestRenameDemoMediaSurvivesASessionThatUsedTheFinalNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sha2 := writeMediaFile(t, dir, "demo-2.png", 22)
	sha1 := writeMediaFile(t, dir, "demo-1.png", 11)
	got, err := renameDemoMedia(dir, acceptedFiles(t, dir, "demo-2.png", "demo-1.png"))
	if err != nil {
		t.Fatalf("renameDemoMedia: %v", err)
	}
	if got[0].Name != "demo-1.png" || got[0].SHA256 != sha2 || got[1].Name != "demo-2.png" || got[1].SHA256 != sha1 {
		t.Fatalf("recorded %+v, want the first-listed file (sha %s) as demo-1 and the second (sha %s) as demo-2", got, sha2, sha1)
	}
	for _, f := range got {
		data, err := os.ReadFile(filepath.Join(dir, f.Name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			t.Errorf("%s holds different bytes than the recorded sha256", f.Name)
		}
	}
}

// TestRenameDemoMediaRefusesToOverwriteAnUnlistedFile: an unlisted file that
// already holds a final name is never clobbered, and nothing is renamed.
func TestRenameDemoMediaRefusesToOverwriteAnUnlistedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMediaFile(t, dir, "shot.png", 10)
	shaKept := writeMediaFile(t, dir, "demo-1.png", 99)
	_, err := renameDemoMedia(dir, acceptedFiles(t, dir, "shot.png"))
	if err == nil || !strings.Contains(err.Error(), `"demo-1.png"`) {
		t.Fatalf("renameDemoMedia over an unlisted demo-1.png = %v, want a refusal naming it", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "shot.png")); err != nil {
		t.Errorf("the listed file was renamed anyway: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "demo-1.png"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != shaKept {
		t.Error("the unlisted demo-1.png was overwritten")
	}
}

// TestPlainEvidenceParentsRefusesALinkedStoreIDOrTicketDirectory: the
// directories jig made above a head's media_dir must be plain directories
// before an attempt clears or makes anything below them; ones that do not
// exist yet are fine, and so is a link at the head itself, which an attempt
// removes as itself.
func TestPlainEvidenceParentsRefusesALinkedStoreIDOrTicketDirectory(t *testing.T) {
	t.Parallel()
	newTree := func(t *testing.T) (root, mediaDir string) {
		t.Helper()
		root = t.TempDir()
		mediaDir = filepath.Join(root, "evidence", "id", "JIG-1", "head")
		return root, mediaDir
	}

	t.Run("nothing made yet", func(t *testing.T) {
		_, mediaDir := newTree(t)
		if err := plainEvidenceParents(mediaDir); err != nil {
			t.Fatalf("plainEvidenceParents with nothing made: %v", err)
		}
	})
	t.Run("plain directories", func(t *testing.T) {
		_, mediaDir := newTree(t)
		if err := os.MkdirAll(mediaDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := plainEvidenceParents(mediaDir); err != nil {
			t.Fatalf("plainEvidenceParents with plain directories: %v", err)
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
			err := plainEvidenceParents(mediaDir)
			if err == nil || !strings.Contains(err.Error(), "is not a plain directory") {
				t.Fatalf("plainEvidenceParents through a linked %s = %v, want a refusal", level.name, err)
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
		if err := plainEvidenceParents(mediaDir); err != nil {
			t.Fatalf("plainEvidenceParents with a link at the head: %v", err)
		}
	})
}

// TestPruneDemoMediaLeavesExactlyTheRecordedFiles: after a demo is accepted
// media_dir holds what demo.yaml lists and nothing else - an unlisted file
// (even one named like jig's own), a subdirectory and its contents, and a
// link - and a link is removed as itself, never followed to what it points at.
func TestPruneDemoMediaLeavesExactlyTheRecordedFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "media")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shaA := writeMediaFile(t, dir, "shot.png", 10)
	shaB := writeMediaFile(t, dir, "clip.mp4", 20)
	got, err := renameDemoMedia(dir, acceptedFiles(t, dir, "shot.png", "clip.mp4"))
	if err != nil {
		t.Fatal(err)
	}

	writeMediaFile(t, dir, "notes.txt", 5)
	writeMediaFile(t, dir, "demo-7.png", 5)
	if err := os.MkdirAll(filepath.Join(dir, "scratch", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, filepath.Join(dir, "scratch", "deep"), "x.png", 5)
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, outside, "keep.png", 5)
	if makeDirLink(outside, filepath.Join(dir, "j")) != nil {
		t.Log("directory links unavailable here; pruning without one")
	}

	if err := pruneDemoMedia(dir, got); err != nil {
		t.Fatalf("pruneDemoMedia: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, e := range entries {
		listed = append(listed, e.Name())
	}
	if fmt.Sprint(listed) != "[demo-1.png demo-2.mp4]" {
		t.Fatalf("media_dir holds %v, want exactly the two recorded files", listed)
	}
	for name, want := range map[string]string{"demo-1.png": shaA, "demo-2.mp4": shaB} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			t.Errorf("%s was changed by pruning", name)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.png")); err != nil {
		t.Errorf("pruning followed a link and removed what it points at: %v", err)
	}
}

// --- demo.yaml ------------------------------------------------------------------------

func newDemoStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// decodeDemoYAML reads round n's demo.yaml as a plain map, the way a reader
// with no knowledge of jig's types would.
func decodeDemoYAML(t *testing.T, st *store.Store, ticket string, n int) map[string]any {
	t.Helper()
	data, err := os.ReadFile(demoYAMLPath(st, ticket, n))
	if err != nil {
		t.Fatalf("read demo.yaml: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode demo.yaml: %v", err)
	}
	return m
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mkRoundDir(t *testing.T, st *store.Store, ticket string, n int) {
	t.Helper()
	if err := os.MkdirAll(gateRoundDir(st, ticket, n), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestWriteDemoRecordRecordedShape pins demo.yaml's keys, decoding it as a
// map: a reader must see exactly these, no more.
func TestWriteDemoRecordRecordedShape(t *testing.T) {
	t.Parallel()
	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	files := []DemoFile{
		{Name: "demo-1.png", SHA256: strings.Repeat("a", 64), Size: 123, Caption: "the form"},
		{Name: "demo-2.mp4", SHA256: strings.Repeat("b", 64), Size: 456, Caption: "the flow"},
	}
	report, err := writeDemoRecord(st, "JIG-1", 1, "headsha", files, "two files", nil)
	if err != nil {
		t.Fatalf("writeDemoRecord: %v", err)
	}
	if report.Status != DemoRecorded || report.Summary != "two files" || len(report.Media) != 2 {
		t.Fatalf("report = %+v", report)
	}

	m := decodeDemoYAML(t, st, "JIG-1", 1)
	if got := fmt.Sprint(sortedKeys(m)); got != "[head_sha media status summary]" {
		t.Fatalf("keys = %s, want [head_sha media status summary]", got)
	}
	if m["status"] != "recorded" || m["head_sha"] != "headsha" || m["summary"] != "two files" {
		t.Fatalf("demo.yaml = %v", m)
	}
	media, ok := m["media"].([]any)
	if !ok || len(media) != 2 {
		t.Fatalf("media = %#v, want a list of 2", m["media"])
	}
	first := media[0].(map[string]any)
	if got := fmt.Sprint(sortedKeys(first)); got != "[caption name sha256 size]" {
		t.Fatalf("a media entry's keys = %s, want [caption name sha256 size]", got)
	}
	if first["name"] != "demo-1.png" || first["sha256"] != strings.Repeat("a", 64) || first["size"] != 123 || first["caption"] != "the form" {
		t.Fatalf("first media entry = %v", first)
	}
}

// TestWriteDemoRecordNothingVisibleKeepsTheMediaKey: a demo that shows
// nothing is still a recorded demo, with an explicit empty list.
func TestWriteDemoRecordNothingVisibleKeepsTheMediaKey(t *testing.T) {
	t.Parallel()
	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	if _, err := writeDemoRecord(st, "JIG-1", 1, "headsha", nil, "internal change; nothing to show", nil); err != nil {
		t.Fatalf("writeDemoRecord: %v", err)
	}
	m := decodeDemoYAML(t, st, "JIG-1", 1)
	if got := fmt.Sprint(sortedKeys(m)); got != "[head_sha media status summary]" {
		t.Fatalf("keys = %s", got)
	}
	if media, ok := m["media"].([]any); !ok || len(media) != 0 {
		t.Fatalf("media = %#v, want an empty list", m["media"])
	}
}

func TestWriteDemoRecordRefusedShape(t *testing.T) {
	t.Parallel()
	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 3)
	report, err := writeDemoRecord(st, "JIG-1", 3, "headsha", nil, "", &demoRefusal{Reason: `file "x.png" is not in media_dir`})
	if err != nil {
		t.Fatalf("writeDemoRecord: %v", err)
	}
	if report.Status != DemoRefused || report.Reason != `file "x.png" is not in media_dir` {
		t.Fatalf("report = %+v", report)
	}
	m := decodeDemoYAML(t, st, "JIG-1", 3)
	if got := fmt.Sprint(sortedKeys(m)); got != "[head_sha reason status]" {
		t.Fatalf("keys = %s, want [head_sha reason status]", got)
	}
	if m["status"] != "refused" || m["head_sha"] != "headsha" || m["reason"] != `file "x.png" is not in media_dir` {
		t.Fatalf("demo.yaml = %v", m)
	}
}

// TestDemoReasonIsOneBoundedLine: a backend error can carry a whole stderr
// tail; the record keeps one line of it.
func TestDemoReasonIsOneBoundedLine(t *testing.T) {
	t.Parallel()
	got := demoReason(fmt.Errorf("the demo session failed:\n  exit 1\r\n\t%s", strings.Repeat("x", 1000)))
	if strings.ContainsAny(got, "\r\n\t") || strings.Contains(got, "  ") {
		t.Errorf("reason %q keeps line breaks or runs of whitespace", got)
	}
	if !strings.HasPrefix(got, "the demo session failed: exit 1 xxx") {
		t.Errorf("reason = %q", got[:40])
	}
	if want := demoReasonCap + len("..."); len([]rune(got)) != want {
		t.Errorf("reason is %d runes, want it capped at %d", len([]rune(got)), want)
	}
	short := demoReason(fmt.Errorf("short"))
	if short != "short" {
		t.Errorf("a short reason = %q, want it unchanged", short)
	}
}

// TestRefusalOfAFailureRecordsOnlyItsCode: when the demo session or its
// backend failed, the reason is jig's own words and the failure's code, never
// the failure's text, whatever that text holds; the text is the detail, for
// the report alone.
func TestRefusalOfAFailureRecordsOnlyItsCode(t *testing.T) {
	t.Parallel()
	text := "herdr agent prompt jig-JIG-1-gate-demo-a1 You are demonstrating round 1. Your inputs are in /mnt/c/jighome/evidence/x.demo.json"
	hide := func(s string) string { return strings.ReplaceAll(s, "SECRET", "<hidden>") }
	cases := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"an error with no code", &demoFailure{err: errors.New(text)}, "the demo session failed: INTERNAL"},
		{"an error with a code", &demoFailure{err: &axi.Error{Msg: text, Code: "SCREEN_UNAVAILABLE"}}, "the demo session failed: SCREEN_UNAVAILABLE"},
		{"a code under a wrapper", &demoFailure{err: fmt.Errorf("session/headless: %w", &axi.Error{Msg: text, Code: "SESSION_TIMEOUT"})}, "the demo session failed: SESSION_TIMEOUT"},
		{"the failure itself wrapped", fmt.Errorf("attempt: %w", &demoFailure{err: errors.New(text)}), "the demo session failed: INTERNAL"},
	}
	for _, c := range cases {
		got := refusalOf(c.err, hide)
		if got.Reason != c.wantReason {
			t.Errorf("%s: reason = %q, want %q", c.name, got.Reason, c.wantReason)
		}
		if strings.Contains(got.Reason, "herdr") || strings.Contains(got.Reason, "/mnt/") {
			t.Errorf("%s: reason %q holds the failure's text", c.name, got.Reason)
		}
		if !strings.Contains(got.Detail, "/mnt/c/jighome/evidence/x.demo.json") {
			t.Errorf("%s: detail = %q, want the failure's whole text", c.name, got.Detail)
		}
	}
	// The text is one line however many it was.
	multi := refusalOf(&demoFailure{err: errors.New("first\n  second\r\n\tthird")}, hide)
	if multi.Detail != "first second third" {
		t.Errorf("detail = %q, want one line", multi.Detail)
	}
}

// TestRefusalOfARefusalIsJigsOwnWords: a refusal jig composed keeps its text
// as its reason, with the directories of this machine named by role, and has
// no detail.
func TestRefusalOfARefusalIsJigsOwnWords(t *testing.T) {
	t.Parallel()
	hide := func(s string) string { return strings.ReplaceAll(s, "/secret/home", "<jig home>") }
	got := refusalOf(errors.New(`media entry 2 ("shot.exe") has an extension that is not an image or video type in /secret/home`), hide)
	if want := `media entry 2 ("shot.exe") has an extension that is not an image or video type in <jig home>`; got.Reason != want {
		t.Errorf("reason = %q, want %q", got.Reason, want)
	}
	if got.Detail != "" {
		t.Errorf("detail = %q, want none for a refusal jig composed", got.Detail)
	}
	long := refusalOf(errors.New(strings.Repeat("w ", 500)), hide)
	if n := len([]rune(long.Reason)); n != demoReasonCap+len("...") {
		t.Errorf("a long refusal is %d runes, want it capped at %d", n, demoReasonCap)
	}
}

// TestWriteDemoRecordKeepsAFailuresDetailOutOfTheRecord: the detail reaches
// the report and not demo.yaml.
func TestWriteDemoRecordKeepsAFailuresDetailOutOfTheRecord(t *testing.T) {
	t.Parallel()
	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	refusal := refusalOf(&demoFailure{err: errors.New("the backend's own stderr, /srv/jighome/x")}, func(s string) string { return s })
	report, err := writeDemoRecord(st, "JIG-1", 1, "headsha", nil, "", &refusal)
	if err != nil {
		t.Fatal(err)
	}
	if report.Reason != "the demo session failed: INTERNAL" || !strings.Contains(report.Detail, "/srv/jighome/x") {
		t.Fatalf("report = %+v, want the code as its reason and the text as its detail", report)
	}
	data, err := os.ReadFile(demoYAMLPath(st, "JIG-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "jighome") || strings.Contains(string(data), "stderr") {
		t.Errorf("demo.yaml holds the failure's text:\n%s", data)
	}
	m := decodeDemoYAML(t, st, "JIG-1", 1)
	if got := fmt.Sprint(sortedKeys(m)); got != "[head_sha reason status]" {
		t.Errorf("keys = %s, want [head_sha reason status]", got)
	}
}

// TestBackendFallback: it is the "outcome" field that says a demo result file
// is the backend's, not any wording, and not the file's other fields.
func TestBackendFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		data        string
		ok          bool
		wantOutcome string
		wantSummary string
	}{
		{"a slice result", `{"outcome":"failed","summary":"no result block; denied tool calls: Write x"}`, true, "failed", "no result block; denied tool calls: Write x"},
		{"a slice result with the tail the backends keep", `{"outcome":"failed","summary":"no result block","raw_tail":"..."}`, true, "failed", "no result block"},
		{"an outcome and nothing else", `{"outcome":"blocked"}`, true, "blocked", ""},
		{"an outcome that is not a string", `{"outcome":7,"summary":["x"]}`, true, "", ""},
		{"a demo result that also has an outcome", `{"media":[],"summary":"s","outcome":"green"}`, true, "green", "s"},
		{"a demo result", `{"media":[],"summary":"the outcome is fine"}`, false, "", ""},
		{"a demo result whose caption says outcome", `{"media":[{"file":"a.png","caption":"outcome"}],"summary":"s"}`, false, "", ""},
		{"a case variant is not the field", `{"Outcome":"failed"}`, false, "", ""},
		{"an outcome nested below the top", `{"media":[],"summary":"s","x":{"outcome":"green"}}`, false, "", ""},
		{"not an object", `["outcome"]`, false, "", ""},
		{"not json", `outcome: failed`, false, "", ""},
		{"empty", ``, false, "", ""},
	}
	for _, c := range cases {
		outcome, summary, ok := backendFallback([]byte(c.data))
		if ok != c.ok || outcome != c.wantOutcome || summary != c.wantSummary {
			t.Errorf("%s: backendFallback(%s) = %q, %q, %v; want %q, %q, %v", c.name, c.data, outcome, summary, ok, c.wantOutcome, c.wantSummary, c.ok)
		}
	}
}

// TestBackendFallbackRecognizesWhatTheBackendsWrite: what headless and herdr
// write when a session wrote no result is the marshaled outcome.Result of its
// final message, whatever that message was, and it has the "outcome" field
// backendFallback looks for. If that shape changes, this fails before a demo
// stops recognizing it.
func TestBackendFallbackRecognizesWhatTheBackendsWrite(t *testing.T) {
	t.Parallel()
	finals := map[string]string{
		"no result block":        "I could not write the result file.",
		"no message at all":      "",
		"a green block":          "All done.\n```json\n{\"outcome\":\"green\",\"summary\":\"done\"}\n```",
		"a block that is a demo": "```json\n{\"media\":[],\"summary\":\"s\"}\n```",
		"two blocks":             "```json\n{}\n```\n```json\n{}\n```",
	}
	for name, final := range finals {
		data, err := json.Marshal(outcome.ParseText("slice", final))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, ok := backendFallback(data); !ok {
			t.Errorf("%s: the backend's fallback %s is not recognized", name, data)
		}
	}
}

func TestReadDemoRecordRoundTrips(t *testing.T) {
	t.Parallel()
	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	files := []DemoFile{{Name: "demo-1.gif", SHA256: strings.Repeat("c", 64), Size: 9, Caption: "c"}}
	if _, err := writeDemoRecord(st, "JIG-1", 1, "h1", files, "s", nil); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := readDemoRecord(st, "JIG-1", 1)
	if err != nil || !ok {
		t.Fatalf("readDemoRecord: %v, ok=%v", err, ok)
	}
	if rec.Status != DemoRecorded || rec.HeadSHA != "h1" || rec.Summary != "s" || fmt.Sprint(rec.Media) != fmt.Sprint(files) {
		t.Fatalf("record = %+v", rec)
	}
	if _, ok, err := readDemoRecord(st, "JIG-1", 2); ok || err != nil {
		t.Fatalf("a round with no demo.yaml = ok %v err %v, want neither", ok, err)
	}
}

func TestReadDemoRecordRefusesWhatItDoesNotUnderstand(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"an unknown key":    "status: recorded\nhead_sha: h\nsummary: s\nmedia: []\nextra: 1\n",
		"an unknown status": "status: accepted\nhead_sha: h\n",
		"no status":         "head_sha: h\n",
		"not yaml":          "status: [\n",
	}
	for name, content := range cases {
		st := newDemoStore(t)
		mkRoundDir(t, st, "JIG-1", 1)
		if err := os.WriteFile(demoYAMLPath(st, "JIG-1", 1), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if rec, ok, err := readDemoRecord(st, "JIG-1", 1); err == nil {
			t.Errorf("%s: readDemoRecord = %+v, ok=%v, want an error", name, rec, ok)
		}
	}
}

// TestRecordedDemoRound: only a recorded demo for the same head counts, and
// only from an earlier round.
func TestRecordedDemoRound(t *testing.T) {
	t.Parallel()
	st := newDemoStore(t)
	for n := 1; n <= 5; n++ {
		mkRoundDir(t, st, "JIG-1", n)
	}
	write := func(n int, head string, refusal *demoRefusal) {
		t.Helper()
		if _, err := writeDemoRecord(st, "JIG-1", n, head, nil, "s", refusal); err != nil {
			t.Fatal(err)
		}
	}
	write(1, "h1", nil)                          // recorded, another head
	write(2, "h2", &demoRefusal{Reason: "nope"}) // refused, this head
	write(3, "h2", nil)                          // recorded, this head
	write(4, "h2", nil)                          // recorded again
	// round 5 has no demo.yaml at all (a scripted or unclean round).

	for _, c := range []struct {
		before int
		head   string
		want   int
	}{
		{5, "h2", 3}, // the earliest recorded one, not the refused round 2
		{3, "h2", 0}, // round 2's is refused and round 3 is not earlier
		{4, "h2", 3}, // rounds before 4
		{3, "h1", 1}, // another head's own record
		{5, "h9", 0}, // nothing recorded for it
		{1, "h1", 0}, // no earlier round at all
		{6, "h1", 1}, // a round with no demo.yaml (5) is skipped, not an error
	} {
		got, err := recordedDemoRound(st, "JIG-1", c.before, c.head)
		if err != nil || got != c.want {
			t.Errorf("recordedDemoRound(before %d, head %s) = %d, %v; want %d", c.before, c.head, got, err, c.want)
		}
	}

	if err := os.WriteFile(demoYAMLPath(st, "JIG-1", 5), []byte("status: bogus\nhead_sha: h\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := recordedDemoRound(st, "JIG-1", 6, "h1"); err != nil {
		// h1 is found at round 1 first, before the bad round 5 is read.
		t.Errorf("recordedDemoRound stopped at a later bad record it never needed: %v", err)
	}
	if _, err := recordedDemoRound(st, "JIG-1", 6, "h9"); err == nil {
		t.Error("recordedDemoRound skipped an unreadable demo.yaml instead of refusing")
	}
}

// --- where media live --------------------------------------------------------------

func TestDemoMediaDirIsUnderTheJigHome(t *testing.T) {
	t.Parallel()
	st := newDemoStore(t)
	homeRoot := t.TempDir()
	d := Deps{Store: st, Home: homeRoot}
	got, err := demoMediaDir(d, "JIG-1", "abc123")
	if err != nil {
		t.Fatalf("demoMediaDir: %v", err)
	}
	id, err := st.ID()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Join(homeRoot, "evidence", id, "JIG-1", "abc123"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("media dir = %q, want %q", got, want)
	}
	if rel, err := filepath.Rel(st.Root, got); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("media dir %q is inside the store %q; media never go in the store's git", got, st.Root)
	}
}

func TestDemoMediaDirRefusesATicketThatIsNotOneDirectory(t *testing.T) {
	t.Parallel()
	d := Deps{Store: newDemoStore(t), Home: t.TempDir()}
	for _, ticket := range []string{"", "../x", "a/b", ".hidden"} {
		if dir, err := demoMediaDir(d, ticket, "abc123"); err == nil {
			t.Errorf("demoMediaDir for ticket %q = %q, want a refusal", ticket, dir)
		}
	}
}

// --- hashRegularFile -----------------------------------------------------------------

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
	if got, err := hashRegularFile(path, info); err != nil || got != want {
		t.Fatalf("hashRegularFile of an unchanged file = %q, %v; want %q", got, err, want)
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
	if got, err := hashRegularFile(path, info); err == nil || !strings.Contains(err.Error(), "changed size") {
		t.Errorf("hashRegularFile of a file that grew = %q, %v; want a changed-size refusal", got, err)
	}

	if err := os.Truncate(path, 3); err != nil {
		t.Fatal(err)
	}
	if got, err := hashRegularFile(path, info); err == nil || !strings.Contains(err.Error(), "changed size") {
		t.Errorf("hashRegularFile of a file that shrank = %q, %v; want a changed-size refusal", got, err)
	}

	writeMediaFile(t, dir, "b.png", 10)
	other, err := os.Lstat(filepath.Join(dir, "b.png"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := hashRegularFile(path, other); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Errorf("hashRegularFile of another file than Lstat saw = %q, %v; want a refusal", got, err)
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
	info, err := lstatPinned(path)
	if err != nil {
		t.Fatal(err)
	}
	// The old file is kept, so the new one cannot reuse its inode.
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	writeMediaFile(t, dir, "a.png", 10)
	if got, err := hashRegularFile(path, info); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("hashRegularFile of a swapped file = %q, %v; want a refusal", got, err)
	}
}

// TestSessionPromptsDescribeTheIntentSourcesInOnePlace: the reviewer's prompt
// and the demo's both hand the session an intent and a source, so both carry
// the one sentence that says what each source means.
func TestSessionPromptsDescribeTheIntentSourcesInOnePlace(t *testing.T) {
	t.Parallel()
	review := RenderReviewPrompt(ReviewRequest{Ticket: "JIG-1", Round: 1, Scope: "full"}, "/abs/review.json", "/abs/result.json")
	demo := RenderDemoPrompt(DemoRequest{Ticket: "JIG-1", Round: 1}, "/abs/demo.json", "/abs/demo.result.json")
	for name, prompt := range map[string]string{"reviewer": review, "demo": demo} {
		if !strings.Contains(prompt, intentSourcesPrompt) {
			t.Errorf("the %s prompt does not carry intentSourcesPrompt:\n%s", name, prompt)
		}
	}
}

func TestDemoMediaDirRefusesAnUnsetJigHome(t *testing.T) {
	t.Parallel()
	d := Deps{Store: newDemoStore(t)}
	if dir, err := demoMediaDir(d, "JIG-1", "abc123"); err == nil {
		t.Fatalf("demoMediaDir with no jig home = %q, want a refusal", dir)
	}
}

// --- host paths ----------------------------------------------------------------------

// TestDemoJSONIsBesideTheMediaDirNotInTheStore: demo.json holds the absolute
// media_dir, so it is the media directory's sibling under the jig home, named
// for the head, whatever spelling of that directory the caller has.
func TestDemoJSONIsBesideTheMediaDirNotInTheStore(t *testing.T) {
	t.Parallel()
	st := newDemoStore(t)
	d := Deps{Store: st, Home: t.TempDir()}
	media, err := demoMediaDir(d, "JIG-1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(media), "abc123.demo.json")
	for _, spelling := range []string{media, media + string(filepath.Separator)} {
		if got := demoJSONPath(spelling); got != want {
			t.Errorf("demoJSONPath(%q) = %q, want %q", spelling, got, want)
		}
	}
	if rel, err := filepath.Rel(st.Root, want); err == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("demo.json %q is inside the store %q", want, st.Root)
	}
}

// TestLeaveOutHostPathsNamesDirectoriesByRole: a real operating system error
// for a path under each directory prints, after leaveOutHostPaths, the role of
// the directory and the rest of the path, and nothing of the host.
func TestLeaveOutHostPathsNamesDirectoriesByRole(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jigHome := filepath.Join(root, "home")
	media := filepath.Join(jigHome, "evidence", "id", "JIG-1", "abc123")
	storeRoot := filepath.Join(root, "store")
	dirs := []hostDir{{storeRoot, "<store>"}, {jigHome, "<jig home>"}, {media, "media_dir"}}

	errFor := func(path string) string {
		_, err := os.Lstat(path)
		if err == nil {
			t.Fatalf("%s exists", path)
		}
		return err.Error()
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{"a path in media_dir", errFor(filepath.Join(media, "a.png")), "media_dir" + string(filepath.Separator) + "a.png"},
		{"a path in the jig home, outside media_dir", errFor(filepath.Join(jigHome, "evidence", "id", "JIG-1", "other")), "<jig home>" + string(filepath.Separator) + "evidence"},
		{"a path in the store", errFor(filepath.Join(storeRoot, "JIG-1", "work")), "<store>" + string(filepath.Separator) + "JIG-1"},
		{"the forward-slash spelling", "mkdir " + filepath.ToSlash(filepath.Join(jigHome, "evidence")) + ": no", "mkdir <jig home>/evidence: no"},
		{"the media directory itself", "cannot read " + media + ".", "cannot read media_dir."},
		{"no path", "the demo session failed: boom", "the demo session failed: boom"},
	}
	for _, c := range cases {
		got := leaveOutHostPaths(c.text, dirs...)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want it to contain %q", c.name, got, c.want)
		}
		for _, host := range []string{root, jigHome, storeRoot} {
			if strings.Contains(got, host) || strings.Contains(got, filepath.ToSlash(host)) {
				t.Errorf("%s: %q still names %s", c.name, got, host)
			}
		}
	}
}

// TestLeaveOutHostPathsNamesARelativeDirectoryByItsAbsoluteSpelling: a
// relative directory (a store reached by a relative --store) is named where
// text spells it absolutely, and its relative spelling, which names nothing of
// the machine, stays.
func TestLeaveOutHostPathsNamesARelativeDirectoryByItsAbsoluteSpelling(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	text := "open " + filepath.Join(cwd, "rel-store", "x") + " (see ." + sep + "rel-store)"
	want := "open <store>" + sep + "x (see ." + sep + "rel-store)"
	if got := leaveOutHostPaths(text, hostDir{"rel-store", "<store>"}); got != want {
		t.Errorf("leaveOutHostPaths(%q) = %q, want %q", text, got, want)
	}
}

// TestLeaveOutHostPathsNeverRewritesWhatIsNotAHostDirectory: an empty
// directory (which would resolve to the working directory), a bare relative
// spelling and a filesystem root each name nothing of the machine (or, for a
// root, a prefix of every path), so text holding them is left as it is.
func TestLeaveOutHostPathsNeverRewritesWhatIsNotAHostDirectory(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fsRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	text := "clear media_dir: remove store: the file is in use. See ./store, " + cwd + " and " + filepath.Join(fsRoot, "tmp")
	got := leaveOutHostPaths(text, hostDir{"", "<empty>"}, hostDir{"store", "<store>"}, hostDir{fsRoot, "<root>"})
	if got != text {
		t.Errorf("leaveOutHostPaths rewrote %q to %q", text, got)
	}
}

// TestLeaveOutHostPathsNamesTheGoQuotedSpelling: a message that quotes one of
// jig's own paths with %q spells it Go-quoted, where a backslash doubles (every
// separator of a Windows path) and a quote character is escaped, and that is
// the same path, so it is named too. The directory has a quote in its name so
// the quoted spelling differs from the plain one on every platform.
func TestLeaveOutHostPathsNamesTheGoQuotedSpelling(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jigHome := filepath.Join(root, `we"ird`, "home")
	media := filepath.Join(jigHome, "evidence", "id", "JIG-1", "abc123")
	quoted := func(p string) string { return strings.Trim(strconv.Quote(p), `"`) }
	if quoted(jigHome) == jigHome {
		t.Fatalf("the quoted spelling of %q is the plain one, so this test proves nothing", jigHome)
	}

	got := leaveOutHostPaths(fmt.Sprintf("open %q: no such file; media %q", filepath.Join(jigHome, "evidence"), filepath.Join(media, "a.png")),
		hostDir{jigHome, "<jig home>"}, hostDir{media, "media_dir"})
	sep := string(filepath.Separator)
	if want := fmt.Sprintf(`open "<jig home>%sevidence": no such file; media "media_dir%sa.png"`, quoted(sep), quoted(sep)); got != want {
		t.Errorf("leaveOutHostPaths = %q, want %q", got, want)
	}
	for _, host := range []string{root, jigHome} {
		if strings.Contains(got, quoted(host)) {
			t.Errorf("%q still names %s", got, quoted(host))
		}
	}
}
