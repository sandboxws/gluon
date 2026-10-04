package inspect

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

func hdr(typ string, ptr uint64, ln, cp, elem int) pretty.Value {
	return pretty.Value{
		Type: typ, Kind: "hdr",
		Ptr: &ptr, Len: &ln, Cap: &cp, Elem: &elem,
	}
}

// Sharing is not pointer equality. A slice taken from the middle of another
// starts at a different address while still writing into the same array, and
// that is exactly the case worth detecting.
func TestAliasing(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		vals  []pretty.Value
		want  string // "" means no sharing reported
	}{
		{
			name:  "identical slices",
			names: []string{"x", "y"},
			vals:  []pretty.Value{hdr("[]int", 0x1000, 5, 5, 8), hdr("[]int", 0x1000, 5, 5, 8)},
			want:  "x and y share a backing array",
		},
		{
			name:  "a view starting partway in",
			names: []string{"x", "y"},
			vals:  []pretty.Value{hdr("[]int", 0x1000, 5, 5, 8), hdr("[]int", 0x1008, 2, 4, 8)},
			want:  "y starts 1 element into x",
		},
		{
			name:  "separate arrays",
			names: []string{"x", "z"},
			vals:  []pretty.Value{hdr("[]int", 0x1000, 5, 5, 8), hdr("[]int", 0x2000, 2, 2, 8)},
			want:  "",
		},
		{
			// Adjacent but not overlapping: x spans [0x1000,0x1028), z starts
			// exactly at its end.
			name:  "adjacent arrays do not overlap",
			names: []string{"x", "z"},
			vals:  []pretty.Value{hdr("[]int", 0x1000, 5, 5, 8), hdr("[]int", 0x1028, 2, 2, 8)},
			want:  "",
		},
		{
			// The tail of x's cap reaches past its len, and writing there is
			// exactly how an append surprises someone.
			name:  "overlap within cap but past len",
			names: []string{"x", "y"},
			vals:  []pretty.Value{hdr("[]int", 0x1000, 2, 5, 8), hdr("[]int", 0x1018, 1, 1, 8)},
			want:  "y starts 3 elements into x",
		},
		{
			name:  "different element types cannot share",
			names: []string{"a", "b"},
			vals:  []pretty.Value{hdr("[]int", 0x1000, 5, 5, 8), hdr("[]byte", 0x1000, 5, 5, 1)},
			want:  "",
		},
		{
			name:  "a string offset counts bytes",
			names: []string{"s", "s[2:]"},
			vals:  []pretty.Value{hdr("string", 0x1000, 6, 6, 1), hdr("string", 0x1002, 4, 4, 1)},
			want:  "starts 2 bytes into s",
		},
		{
			name:  "a nil slice has no backing array",
			names: []string{"n", "x"},
			vals:  []pretty.Value{{Type: "[]int", Kind: "nil", Repr: "nil"}, hdr("[]int", 0x1000, 5, 5, 8)},
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(aliasing(tc.names, tc.vals), "\n")
			if tc.want == "" {
				if got != "" {
					t.Errorf("reported sharing where there is none: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("aliasing = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// Something with no header at all must not stretch every column to the width
// of an explanatory sentence.
func TestRenderSlicesKeepsNonSlicesOutOfTheTable(t *testing.T) {
	vals := []pretty.Value{
		hdr("[]int", 0x1000, 5, 5, 8),
		{Type: "int", Kind: "scalar", Repr: "not a slice or string — no header to show"},
	}
	got := RenderSlices([]string{"x", "5"}, vals, pretty.Styles{}, false)
	lines := strings.Split(got, "\n")
	for _, line := range lines {
		if strings.Contains(line, "0x1000") && strings.Contains(line, "no header") {
			t.Errorf("the message was put in a table row: %q", line)
		}
	}
	if !strings.Contains(got, "5: not a slice") {
		t.Errorf("the message was dropped entirely: %q", got)
	}
}

func TestRenderSlicesWithNoHeadersAtAll(t *testing.T) {
	vals := []pretty.Value{{Type: "int", Kind: "scalar", Repr: "not a slice or string"}}
	got := RenderSlices([]string{"5"}, vals, pretty.Styles{}, false)
	if strings.Contains(got, "ptr") {
		t.Errorf("drew an empty table: %q", got)
	}
}
