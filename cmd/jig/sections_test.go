package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestSectionSetOrdersAndRefusesDuplicates pins the shared behavior of the
// here and status registries: sections come back by Order then Name, and a
// nameless or repeated one panics. Each case uses a set of its own, so the
// binary's registries stay as the init() functions left them.
func TestSectionSetOrdersAndRefusesDuplicates(t *testing.T) {
	t.Parallel()

	here := newHereSections()
	for _, s := range []hereSection{{Order: 20, Name: "b"}, {Order: 10, Name: "z"}, {Order: 20, Name: "a"}} {
		here.add(s)
	}
	var names []string
	for _, s := range here.sorted() {
		names = append(names, s.Name)
	}
	if want := []string{"z", "a", "b"}; !slices.Equal(names, want) {
		t.Errorf("here sections sort as %q, want %q: by Order, then by Name", names, want)
	}

	status := newStatusSections()
	status.add(statusSection{Name: "spend"})
	for _, c := range []struct {
		name string
		s    statusSection
		want string
	}{
		{"a name taken", statusSection{Name: "spend"}, `"spend" is already registered`},
		{"no name", statusSection{}, "no Name"},
	} {
		func() {
			defer func() {
				got := recover()
				if msg, _ := got.(string); !strings.Contains(msg, c.want) {
					t.Errorf("%s: panic = %v, want it to mention %q", c.name, got, c.want)
				}
			}()
			status.add(c.s)
		}()
	}
}

// TestRegisterSectionsRefuseWhatProducesNothing pins that a section with no
// way to render is refused when it registers.
func TestRegisterSectionsRefuseWhatProducesNothing(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]func(){
		"here section with neither Blocks nor JSON": func() { registerHereSection(hereSection{Name: "empty"}) },
		"status section with no Blocks":             func() { registerStatusSection(statusSection{Name: "empty"}) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: registered without a panic", name)
				}
			}()
			f()
		}()
	}
	for _, s := range hereSections() {
		if s.Name == "empty" {
			t.Errorf("a refused here section was registered anyway")
		}
	}
}

// TestRenderStatusRendersRegisteredSectionsBeforeTheHelpLine pins where a
// status section's blocks land: after the status blocks, in section order,
// and before the one trailing help block. Called through renderStatus with
// sections of the test's own, so the binary's registry is untouched.
func TestRenderStatusRendersRegisteredSectionsBeforeTheHelpLine(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	plain, err := renderStatus(st, fx.Ticket, nil)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	helpAt := strings.LastIndex(plain, "help[")
	if helpAt < 0 {
		t.Fatalf("status has no help block:\n%s", plain)
	}

	var gotTicket string
	sections := []statusSection{
		{Order: 1, Name: "first", Blocks: func(_ *store.Store, ticket string) ([]string, error) {
			gotTicket = ticket
			return []string{"first_section: one", "first_two: 2"}, nil
		}},
		{Order: 2, Name: "second", Blocks: func(*store.Store, string) ([]string, error) {
			return []string{"second_section: yes"}, nil
		}},
	}
	out, err := renderStatus(st, fx.Ticket, sections)
	if err != nil {
		t.Fatalf("renderStatus: %v", err)
	}
	if gotTicket != fx.Ticket {
		t.Errorf("section was given ticket %q, want %q", gotTicket, fx.Ticket)
	}
	if want := plain[:helpAt] + "first_section: one\nfirst_two: 2\nsecond_section: yes\n" + plain[helpAt:]; out != want {
		t.Errorf("status with sections:\n%s\nwant the plain status with the section blocks just before its help block:\n%s", out, want)
	}

	// A failing section fails the render with its error.
	boom := errors.New("section failed")
	_, err = renderStatus(st, fx.Ticket, []statusSection{{Name: "bad", Blocks: func(*store.Store, string) ([]string, error) { return nil, boom }}})
	if !errors.Is(err, boom) {
		t.Errorf("renderStatus with a failing section = %v, want its error", err)
	}
}
