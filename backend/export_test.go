package backend

import "testing"

func TestTheArgumentsTextusSetsAreRefused(t *testing.T) {
	cases := []struct {
		args  []string
		owned string
	}{
		{[]string{"--toc"}, ""},
		{[]string{"--to=html"}, "--to=html"},
		{[]string{"--out", "x"}, "--out"},
		{[]string{"-o", "x"}, "-o"},
		{[]string{"-V", "-o"}, ""}, // -V takes the next argument as its value
		{[]string{"--metadata", "--to"}, ""},
		{[]string{"-sN"}, ""},
		{[]string{"stray"}, "stray"},
		{[]string{"--"}, "--"},
	}
	for _, c := range cases {
		got, ok := ownedArg(c.args)
		if got != c.owned || ok != (c.owned != "") {
			t.Errorf("%v: got %q", c.args, got)
		}
	}
}
