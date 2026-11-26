package tui

import "testing"

// TestAMoveOnAPhaseScreenLaysNoListOutAgain: a frame is built from the rows
// already laid out. A key that moves the cursor changes which row the detail
// reads and which row is lit, and the lists, which are the same cells at the same
// width, are not measured again, the activity panel apart, whose rows follow the
// room the detail leaves; a change of what the lists are made of, or of the
// width they are drawn in, lays them out again.
func TestAMoveOnAPhaseScreenLaysNoListOutAgain(t *testing.T) {
	t.Parallel()
	for _, sc := range []struct{ name, key string }{{"build", "2"}, {"review", "3"}, {"brief", "1"}, {"publish", "4"}} {
		g := tourRig(t, 100, 30)
		g.press(sc.key)
		if g.m.Screen() != sc.name {
			t.Fatalf("on %s, want %s", g.m.Screen(), sc.name)
		}
		g.view()
		before := g.m.laidBuilds
		if before == 0 {
			t.Fatalf("%s: the first frame laid out no list", sc.name)
		}
		for _, key := range []string{"down", "up", "down", "down", "up"} {
			g.press(key)
			g.view()
		}
		if got := g.m.laidBuilds; got-before > 5 {
			t.Errorf("%s: five moves laid %d lists out again, more than the activity panel each, whose rows follow the room the detail leaves", sc.name, got-before)
		}
		g.sized(120, 30)
		g.view()
		if g.m.laidBuilds == before {
			t.Errorf("%s: a wider screen drew the lists from the memo of the narrower one", sc.name)
		}
	}
}
