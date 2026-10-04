package gluonrt

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// deepFix nests through a slice rather than a pointer so its flat fallback
// prints no address: the byte-identity fixture has to be reproducible, and
// %+v on a *deepFix would write a different number every run.
type deepFix struct {
	N int
	X []deepFix
}

func nestFix(n int) deepFix {
	v := deepFix{N: n}
	if n > 0 {
		v.X = []deepFix{nestFix(n - 1)}
	}
	return v
}

// withLimits sets the bounds for one test. They are package variables read at
// init, so a test cannot reach them through the environment after the fact —
// the environment is what the child parses, and the child is a fresh process.
func withLimits(t *testing.T, items, depth int) {
	t.Helper()
	oi, od := maxItems, maxDepth
	maxItems, maxDepth = items, depth
	t.Cleanup(func() { maxItems, maxDepth = oi, od })
}

func TestLimitParsesOnlyPositiveIntegers(t *testing.T) {
	for _, tc := range []struct {
		set  bool
		val  string
		want int
	}{
		{set: false, want: 200},
		{set: true, val: "", want: 200},
		{set: true, val: "500", want: 500},
		{set: true, val: "1", want: 1},
		{set: true, val: "0", want: 200},
		{set: true, val: "-5", want: 200},
		{set: true, val: "abc", want: 200},
		{set: true, val: "200.5", want: 200},
		{set: true, val: " 300", want: 200},
	} {
		name := "unset"
		if tc.set {
			name = "GLUON_MAX_ITEMS=" + tc.val
		}
		t.Run(name, func(t *testing.T) {
			if tc.set {
				t.Setenv("GLUON_MAX_ITEMS", tc.val)
			} else {
				os.Unsetenv("GLUON_MAX_ITEMS")
			}
			if got := __gluonLimit("GLUON_MAX_ITEMS", 200); got != tc.want {
				t.Errorf("__gluonLimit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestItemLimitBoundsOneLevel(t *testing.T) {
	sl := make([]int, 10)
	for i := range sl {
		sl[i] = i
	}

	withLimits(t, 3, 6)
	got := Payload(sl)

	if !strings.Contains(got, `"m":7`) {
		t.Errorf("omitted count not reported at maxItems=3: %s", got)
	}
	if n := strings.Count(got, `"k":"scalar"`); n != 3 {
		t.Errorf("described %d elements, want 3: %s", n, got)
	}
}

func TestDepthLimitBoundsNesting(t *testing.T) {
	v := nestFix(7)

	withLimits(t, 200, 2)
	shallow := Payload(v)
	withLimits(t, 200, 6)
	deep := Payload(v)

	// The flat fallback carries the whole remaining value on one line, so a
	// lower depth means it appears sooner and the structural description is
	// shorter. Counting struct nodes is the observation that does not depend
	// on how %+v happens to render.
	sn, dn := strings.Count(shallow, `"k":"struct"`), strings.Count(deep, `"k":"struct"`)
	if sn >= dn {
		t.Errorf("maxDepth=2 described %d struct levels, maxDepth=6 described %d; want fewer at the lower bound", sn, dn)
	}
	if sn == 0 {
		t.Error("maxDepth=2 described no structure at all")
	}
}

// TestDefaultLimitsAreByteIdentical pins the encoder's output at the defaults
// against a fixture captured before the bounds became variables. Invariant 6
// rests on gluon's own process producing the child's exact bytes, so a change
// to the bounds that moved a single byte at the defaults would break the
// constant fast path rather than this test.
func TestDefaultLimitsAreByteIdentical(t *testing.T) {
	os.Unsetenv("GLUON_MAX_ITEMS")
	os.Unsetenv("GLUON_MAX_DEPTH")
	if got := __gluonLimit("GLUON_MAX_ITEMS", 200); got != 200 {
		t.Fatalf("default items = %d, want 200", got)
	}
	if got := __gluonLimit("GLUON_MAX_DEPTH", 6); got != 6 {
		t.Fatalf("default depth = %d, want 6", got)
	}

	withLimits(t, 200, 6)

	sl := make([]int, 300)
	for i := range sl {
		sl[i] = i
	}
	got := Payload(sl, nestFix(7))

	want, err := os.ReadFile("testdata/default_limits.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("encoder output changed at the default limits\n got %d bytes\nwant %d bytes", len(got), len(want))
	}

	// The fixture is only evidence if it exercises both bounds.
	if !regexp.MustCompile(`"m":100`).MatchString(string(want)) {
		t.Error("fixture does not exercise the item bound")
	}
	if !strings.Contains(string(want), `"k":"struct","r":`) {
		t.Error("fixture does not exercise the depth bound")
	}
}
