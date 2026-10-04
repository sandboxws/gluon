package repl

import (
	"testing"
)

func TestFlattenForHistory(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single line is untouched",
			in:   "x := 1",
			want: "x := 1",
		},
		{
			name: "func body collapses to one recallable entry",
			in:   "func double(n int) int {\n\treturn n * 2\n}",
			want: "func double(n int) int { return n * 2 }",
		},
		{
			name: "for loop collapses",
			in:   "for i := 0; i < 3; i++ {\n\tsum += i\n}",
			want: "for i := 0; i < 3; i++ { sum += i }",
		},
		{
			// Flattening would pull the closing brace into the comment.
			name: "line comment blocks flattening",
			in:   "func f() int {\n\t// double it\n\treturn 2\n}",
			want: "func f() int {\n\t// double it\n\treturn 2\n}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := flattenForHistory(tc.in); got != tc.want {
				t.Errorf("flattenForHistory(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsQuit(t *testing.T) {
	for _, s := range []string{":q", ":quit", ":exit"} {
		if !isQuit(s) {
			t.Errorf("isQuit(%q) = false", s)
		}
	}
	for _, s := range []string{":src", "x := 1", ":save q"} {
		if isQuit(s) {
			t.Errorf("isQuit(%q) = true", s)
		}
	}
}

// :ls, :layout and :bench all reject a missing argument with a usage line
// rather than a type error from deeper down.
