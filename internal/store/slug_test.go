package store

import (
	"strings"
	"testing"
)

// TestSlug: the lowercase ASCII words of a title, joined by dashes.
func TestSlug(t *testing.T) {
	for _, tc := range []struct {
		name, title, want string
	}{
		{"plain words", "Upload item gallery", "upload-item-gallery"},
		{"case folds", "Fix The LOGIN Page", "fix-the-login-page"},
		{"punctuation ends a word", "Add retry: back-off, jitter & caps!", "add-retry-back-off-jitter-caps"},
		{"digits are words", "Support Go 1.27 and HTTP/2", "support-go-1-27-and-http-2"},
		{"separators collapse", "  a --  b__c\t\nd  ", "a-b-c-d"},
		{"an apostrophe sits inside a word", "Don't drop users\u2019 sessions", "dont-drop-users-sessions"},
		{"non-ASCII letters end a word", "caf\u00e9 menu", "caf-menu"},
		{"the Kelvin sign is not a k", "\u212Aey", "ey"},
		{"nothing ASCII has no slug", "\u65e5\u672c\u8a9e\u306e\u30bf\u30a4\u30c8\u30eb", ""},
		{"empty", "", ""},
		{"only punctuation", "?! -- ...", ""},
		{"exactly forty keeps every word", "aaaa kkkk cccc dddd eeee ffff gggg hhhhh", "aaaa-kkkk-cccc-dddd-eeee-ffff-gggg-hhhhh"},
		{"over forty is cut at a word boundary", "aaaa kkkk cccc dddd eeee ffff gggg hhhhh iiii", "aaaa-kkkk-cccc-dddd-eeee-ffff-gggg-hhhhh"},
		{"a word that would cross forty is left out whole", "aaaa kkkk cccc dddd eeee ffff gggg hhhhhh iiii", "aaaa-kkkk-cccc-dddd-eeee-ffff-gggg"},
		{"a first word over forty is cut where it stands", strings.Repeat("x", 55), strings.Repeat("x", 40)},
		{"a long first word ends the slug", strings.Repeat("x", 55) + " tail", strings.Repeat("x", 40)},
	} {
		if got := Slug(tc.title); got != tc.want {
			t.Errorf("%s: Slug(%q) = %q, want %q", tc.name, tc.title, got, tc.want)
		}
	}
}

// TestSlugIsAlwaysAGitSafeTail: whatever the title, the slug is at most forty
// characters of [a-z0-9] and single dashes between words, so it never makes
// the branch name it ends something git refuses.
func TestSlugIsAlwaysAGitSafeTail(t *testing.T) {
	for _, title := range []string{
		"Normal title", "UPPER lower 123", "~^:?*[ weird ]@{ stuff }.lock", "a..b", "-lead", "trail-", "x\u0000y",
		"emoji \U0001F680 launch", strings.Repeat("word ", 30), strings.Repeat("\u00fc", 50),
	} {
		got := Slug(title)
		if len(got) > slugMax {
			t.Errorf("Slug(%q) = %q: %d characters, want at most %d", title, got, len(got), slugMax)
		}
		if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") || strings.Contains(got, "--") {
			t.Errorf("Slug(%q) = %q: a dash that does not sit between two words", title, got)
		}
		for _, r := range got {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				t.Errorf("Slug(%q) = %q: %q is not a lowercase ASCII letter, digit or dash", title, got, r)
				break
			}
		}
	}
}
