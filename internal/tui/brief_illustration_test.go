package tui

import (
	"errors"
	"strings"
	"testing"
)

const illustrationAddr = "http://127.0.0.1:41234/illustration.html"

// illustrated is the brief screen of a brief that has an illustration, with a
// viewer that gives illustrationAddr and an opener that records what it was
// sent; viewed and opened are what each was asked.
func illustrated(t *testing.T, open func(string) error) (g *rig, viewed, opened *[]string) {
	t.Helper()
	text := strings.Replace(briefTextJIG1(), "illustration: no", "illustration: yes", 1)
	g = briefRigWith(t, 100, 60, statusJIG1Brief(), text)
	viewed, opened = &[]string{}, &[]string{}
	g.m.cfg.Viewer = func(ticket string) (string, error) {
		*viewed = append(*viewed, ticket)
		return illustrationAddr, nil
	}
	if open != nil {
		g.m.cfg.Open = func(url string) error {
			*opened = append(*opened, url)
			return open(url)
		}
	}
	return g.focus("illustration"), viewed, opened
}

// TestTheIllustrationIsARowOnlyWhenTheBriefHasOne: a brief with an illustration
// has a row for it after its slices and before the approval, whose detail says it
// is not served yet; a brief with none has no such row.
func TestTheIllustrationIsARowOnlyWhenTheBriefHasOne(t *testing.T) {
	t.Parallel()
	g, _, _ := illustrated(t, nil)
	rows := rowsOf(g.view(), "Parts")
	n := len(rows)
	if !strings.HasPrefix(strings.TrimPrefix(rows[n-2], "▸ "), "illustration") || !strings.Contains(rows[n-2], "not served yet") || !strings.HasPrefix(rows[n-1], "approval") {
		t.Errorf("the last two parts are %q and %q, want the illustration then the approval", rows[n-2], rows[n-1])
	}
	if d := readWords(between(g.view(), "╭─ Detail · illustration", "╰")); !strings.Contains(d, "served not yet: enter serves it in the browser") {
		t.Errorf("the detail of the illustration does not say it is not served:\n%s", g.view())
	}

	none := briefRigWith(t, 100, 60, statusJIG1Brief(), briefTextJIG1())
	if strings.Contains(strings.Join(rowsOf(none.view(), "Parts"), "\n"), "illustration") {
		t.Errorf("a brief with no illustration has a row for it:\n%s", none.view())
	}
}

// TestEnterOnTheIllustrationServesItAndOpensTheBrowser: enter asks the viewer to
// serve the ticket's illustration and sends the address it gives to the opener,
// and the row and its detail then say it is served at that address.
func TestEnterOnTheIllustrationServesItAndOpensTheBrowser(t *testing.T) {
	t.Parallel()
	g, viewed, opened := illustrated(t, func(string) error { return nil })
	g.press("enter")
	if len(*viewed) != 1 || (*viewed)[0] != "JIG-1" || len(*opened) != 1 || (*opened)[0] != illustrationAddr {
		t.Fatalf("the viewer was asked for %v and the opener sent %v, want JIG-1 and %s", *viewed, *opened, illustrationAddr)
	}
	if !strings.Contains(g.m.notice, "opened the illustration: "+illustrationAddr) {
		t.Errorf("the notice is %q", g.m.notice)
	}
	view := g.view()
	if d := readWords(between(view, "╭─ Detail · illustration", "╰")); !strings.Contains(d, "served "+illustrationAddr) {
		t.Errorf("the detail does not name the address:\n%s", view)
	}
	if g.m.Screen() != "brief" {
		t.Errorf("enter on the illustration left the brief for %s", g.m.Screen())
	}
}

// TestEnterOnTheIllustrationWithNoOpenerCarriesTheAddress: with no opener (a pipe)
// the notice carries the address, and the viewer has served it.
func TestEnterOnTheIllustrationWithNoOpenerCarriesTheAddress(t *testing.T) {
	t.Parallel()
	g, viewed, _ := illustrated(t, nil)
	g.press("enter")
	if len(*viewed) != 1 || !strings.Contains(g.m.notice, illustrationAddr) {
		t.Errorf("viewed %v, notice %q, want the address in the notice", *viewed, g.m.notice)
	}
}

// TestARefusedOpenSaysWhyAndCarriesTheAddress: an opener that refuses says why
// and the notice still carries the address, so the person can open it by hand.
func TestARefusedOpenSaysWhyAndCarriesTheAddress(t *testing.T) {
	t.Parallel()
	g, _, opened := illustrated(t, func(string) error { return errors.New("no browser here") })
	g.press("enter")
	if len(*opened) != 1 || !strings.Contains(g.m.notice, "could not open the illustration: no browser here") || !strings.Contains(g.m.notice, illustrationAddr) {
		t.Errorf("opened %v, notice %q, want why it was refused and the address", *opened, g.m.notice)
	}

	// A viewer that cannot serve says why too.
	g2, _, _ := illustrated(t, nil)
	g2.m.cfg.Viewer = func(string) (string, error) { return "", errors.New("no port") }
	g2.press("enter")
	if !strings.Contains(g2.m.notice, "could not serve the illustration: no port") {
		t.Errorf("notice %q, want why it could not be served", g2.m.notice)
	}
}

// TestEnterAgainOnTheIllustrationReusesTheAddress: the viewer is asked once for a
// ticket; the second enter sends the same address to the opener again. An address
// that is a path (a remote session) is named, and no opener is sent it.
func TestEnterAgainOnTheIllustrationReusesTheAddress(t *testing.T) {
	t.Parallel()
	g, viewed, opened := illustrated(t, func(string) error { return nil })
	g.press("enter", "enter")
	if len(*viewed) != 1 || len(*opened) != 2 || (*opened)[0] != (*opened)[1] {
		t.Errorf("viewer asked %d times and opener sent %v, want one ask and the same address twice", len(*viewed), *opened)
	}

	r, _, sent := illustrated(t, func(string) error { return nil })
	r.m.cfg.Viewer = func(string) (string, error) { return "/store/tickets/JIG-1/illustration.html", nil }
	r.press("enter")
	if len(*sent) != 0 || !strings.Contains(r.m.notice, "the illustration is at /store/tickets/JIG-1/illustration.html") {
		t.Errorf("a path was sent to the opener %v, notice %q", *sent, r.m.notice)
	}
	// Nothing listens in that session: the row and the detail never say served.
	view := r.view()
	rows := rowsOf(view, "Parts")
	if row := rows[len(rows)-2]; strings.Contains(row, "served ") && !strings.Contains(row, "not served") {
		t.Errorf("the row says a path is served: %q", row)
	}
	if d := readWords(between(view, "╭─ Detail · illustration", "╰")); !strings.Contains(d, "not served: the file is at /store/tickets/JIG-1/illustration.html") || strings.Contains(d, "served /store") {
		t.Errorf("the detail says a path is served:\n%s", view)
	}
}
