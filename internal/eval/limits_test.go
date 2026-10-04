package eval

import (
	"slices"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/gluonrt"
)

func TestLimitEnvIsEmptyAtTheDefaults(t *testing.T) {
	for name, e := range map[string]*Evaluator{
		"zero value":      {},
		"set to defaults": {maxItems: gluonrt.DefaultMaxItems, maxDepth: gluonrt.DefaultMaxDepth},
	} {
		t.Run(name, func(t *testing.T) {
			if env := e.limitEnv(); len(env) != 0 {
				t.Errorf("child would receive %v, want nothing", env)
			}
		})
	}
}

func TestLimitEnvCarriesOnlyWhatMoved(t *testing.T) {
	for _, tc := range []struct {
		name         string
		items, depth int
		want         []string
	}{
		{"items only", 500, 0, []string{"GLUON_MAX_ITEMS=500"}},
		{"depth only", 0, 12, []string{"GLUON_MAX_DEPTH=12"}},
		{"both", 300, 9, []string{"GLUON_MAX_ITEMS=300", "GLUON_MAX_DEPTH=9"}},
		{"items back to default", gluonrt.DefaultMaxItems, 9, []string{"GLUON_MAX_DEPTH=9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &Evaluator{}
			e.SetLimits(tc.items, tc.depth)
			if got := e.limitEnv(); !slices.Equal(got, tc.want) {
				t.Errorf("limitEnv = %v, want %v", got, tc.want)
			}
		})
	}
}

// A non-positive bound leaves the other one alone, so a caller with only one
// to set does not have to know the default for the other.
func TestSetLimitsIgnoresNonPositive(t *testing.T) {
	e := &Evaluator{}
	e.SetLimits(500, 9)
	e.SetLimits(0, -1)

	items, depth := e.Limits()
	if items != 500 || depth != 9 {
		t.Errorf("Limits = (%d, %d), want (500, 9)", items, depth)
	}
}

func TestLimitsReportsDefaultsWhenUnset(t *testing.T) {
	items, depth := (&Evaluator{}).Limits()
	if items != gluonrt.DefaultMaxItems || depth != gluonrt.DefaultMaxDepth {
		t.Errorf("Limits = (%d, %d), want (%d, %d)", items, depth, gluonrt.DefaultMaxItems, gluonrt.DefaultMaxDepth)
	}
}

// A default session keys on the bare program text. The suffix is add-only:
// an entry recorded before the limits existed is still the entry it finds.
func TestCacheKeyIsBareTextAtTheDefaults(t *testing.T) {
	const src = "package main\n\nfunc main() {}\n"
	if got := (&Evaluator{}).cacheKey(src); got != src {
		t.Errorf("cacheKey = %q, want the bare program text", got)
	}
}

func TestCacheKeySeparatesTwoLimits(t *testing.T) {
	const src = "package main\n\nfunc main() {}\n"

	low := &Evaluator{}
	high := &Evaluator{}
	high.SetLimits(500, 0)

	if low.cacheKey(src) == high.cacheKey(src) {
		t.Fatal("the same text under two limits shares one cache key")
	}
	if k := high.cacheKey(src); !strings.HasPrefix(k, src) {
		t.Errorf("cacheKey = %q, want the program text and then the limits", k)
	}

	// Going back to the first limit finds the first entry: the key is a
	// function of the bounds, not of how many times they have moved.
	back := &Evaluator{}
	back.SetLimits(500, 0)
	if back.cacheKey(src) != high.cacheKey(src) {
		t.Error("returning to a limit does not return to its entry")
	}
}

// The cache itself is what the key protects: the same text at two limits must
// be two entries, and neither may be served for the other.
func TestCacheServesTheEntryForTheLimitInForce(t *testing.T) {
	const src = "package main\n\nfunc main() {}\n"
	e := &Evaluator{cache: newResultCache(8)}

	e.putUnlessLive(evalOpts{}, src, Result{Output: "200 items\n"})

	e.SetLimits(300, 0)
	if _, _, ok := e.cache.get(e.cacheKey(src)); ok {
		t.Fatal("the raised limit was served the answer recorded under the default")
	}
	e.putUnlessLive(evalOpts{}, src, Result{Output: "300 items\n"})

	e.SetLimits(gluonrt.DefaultMaxItems, 0)
	res, _, ok := e.cache.get(e.cacheKey(src))
	if !ok {
		t.Fatal("returning to the default lost its entry")
	}
	if res.Output != "200 items\n" {
		t.Errorf("default limit served %q, want the answer recorded under it", res.Output)
	}
}

// live neither reads nor writes, whatever the limits are.
func TestLiveStillWritesNothingUnderANonDefaultLimit(t *testing.T) {
	const src = "package main\n\nfunc main() {}\n"
	e := &Evaluator{cache: newResultCache(8)}
	e.SetLimits(300, 0)

	e.putUnlessLive(evalOpts{live: true}, src, Result{Output: "rows\n"})
	if _, _, ok := e.cache.get(e.cacheKey(src)); ok {
		t.Error("a live evaluation wrote an entry")
	}
}
