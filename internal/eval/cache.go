package eval

import "container/list"

// resultCache maps a rendered program to what running it produced. The same
// program text always compiles to the same binary, so a repeat is free.
//
// It does not help the common case — every new line makes a new program, so
// typing forward always misses. What it does cover is going backwards: :undo
// re-evaluates a program the session has already run, which would otherwise
// cost a full build and exec.
//
// The tradeoff is that a hit skips execution, so a cached program's side
// effects do not fire again and time- or rand-dependent output is the value
// from when it first ran. For :undo that is arguably the better behaviour:
// the user is returning to a state they have already seen.
type resultCache struct {
	max   int
	order *list.List // front = most recently used
	items map[string]*list.Element
}

type cacheEntry struct {
	key string
	res Result
	err error
}

func newResultCache(max int) *resultCache {
	return &resultCache{max: max, order: list.New(), items: map[string]*list.Element{}}
}

func (c *resultCache) get(key string) (Result, error, bool) {
	el, ok := c.items[key]
	if !ok {
		return Result{}, nil, false
	}
	c.order.MoveToFront(el)
	e := el.Value.(*cacheEntry)
	return e.res, e.err, true
}

func (c *resultCache) put(key string, res Result, err error) {
	if el, ok := c.items[key]; ok {
		el.Value.(*cacheEntry).res = res
		el.Value.(*cacheEntry).err = err
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&cacheEntry{key: key, res: res, err: err})
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		if oldest == nil {
			return
		}
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry).key)
	}
}
