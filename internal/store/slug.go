package store

import "strings"

// slugMax is the length a slug is cut to: "about forty characters", cut at a
// word boundary.
const slugMax = 40

// Slug turns a ticket's title into the short kebab-case tail of its branch
// name: the lowercase ASCII words of the title joined by dashes, cut at a
// word boundary so it is at most slugMax characters. Anything that is not an
// ASCII letter or digit ends a word; an apostrophe sits inside one ("don't"
// is "dont"), so it ends nothing. A first word longer than slugMax is cut
// where it stands, since a slug that is empty for a long word would name
// nothing. A title with no ASCII word has no slug: "" (a branch then keeps the
// name jig has always given a ticket; see defaultBranch).
//
// The result is only ever [a-z0-9] and single dashes between words, so it can
// follow an id in a branch name without git objecting to the whole.
func Slug(title string) string {
	var words []string
	var word strings.Builder
	end := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range title {
		switch {
		case r >= 'A' && r <= 'Z':
			word.WriteByte(byte(r - 'A' + 'a'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			word.WriteByte(byte(r))
		case r == '\'' || r == '\u2019':
		default:
			end()
		}
	}
	end()

	slug := ""
	for _, w := range words {
		next := w
		if slug != "" {
			next = slug + "-" + w
		}
		if len(next) > slugMax {
			if slug == "" {
				slug = next[:slugMax]
			}
			break
		}
		slug = next
	}
	return slug
}
