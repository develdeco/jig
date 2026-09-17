package session

import "testing"

func TestWSLPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`C:\Users\x`, "/mnt/c/Users/x"},
		{`D:\work\repos\jig`, "/mnt/d/work/repos/jig"},
		{`c:\already\lower`, "/mnt/c/already/lower"},
		{"/mnt/c/already/posix", "/mnt/c/already/posix"},
	}
	for _, c := range cases {
		if got := wslPath(c.in); got != c.want {
			t.Errorf("wslPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
