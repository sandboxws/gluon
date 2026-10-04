package eval

import "testing"

func TestResultCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := newResultCache(2)
	c.put("a", Result{Output: "A"}, nil)
	c.put("b", Result{Output: "B"}, nil)

	// Touch "a" so "b" becomes the least recently used.
	if _, _, ok := c.get("a"); !ok {
		t.Fatal("a should still be cached")
	}
	c.put("c", Result{Output: "C"}, nil)

	if _, _, ok := c.get("b"); ok {
		t.Error("b should have been evicted")
	}
	for _, k := range []string{"a", "c"} {
		if _, _, ok := c.get(k); !ok {
			t.Errorf("%s should still be cached", k)
		}
	}
}

func TestResultCacheUpdatesInPlace(t *testing.T) {
	c := newResultCache(4)
	c.put("k", Result{Output: "first"}, nil)
	c.put("k", Result{Output: "second"}, nil)
	res, _, ok := c.get("k")
	if !ok || res.Output != "second" {
		t.Errorf("got %q,%v; want second,true", res.Output, ok)
	}
	if c.order.Len() != 1 {
		t.Errorf("re-putting a key should not grow the cache, len=%d", c.order.Len())
	}
}

// A doomed build is avoided only if the seeded callees are actually matched.
func TestSeedNoValueCoversCommonCalls(t *testing.T) {
	seed := seedNoValue()
	for _, name := range []string{"close", "delete", "clear", "slices.Sort", "sort.Slice"} {
		if !seed[name] {
			t.Errorf("%s should be seeded as returning no value", name)
		}
	}
}
