// Package gluonrt holds the value encoder that gluon injects into every
// generated program. It is compiled twice: once as part of gluon itself (so
// the compiler checks it), and once inside the temp module, where render
// rewrites the package clause to "main" and drops it in beside main.go.
//
// It describes values as JSON rather than formatting them, because the
// formatting happens in gluon's own process where lipgloss is available.
// Linking lipgloss into the generated program would mean network-resolved
// dependencies in the temp module and a slower build on every single line, so
// this file stays strictly standard library — and hand-rolls its JSON, which
// is easy for writing and keeps encoding/json out of the link.
//
// Identifiers are __gluon-prefixed so a session that declares P, render, or
// value does not collide with them.
package gluonrt

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// Marker delimits the encoded value from whatever the user's own code printed.
const Marker = "\x00GLUON\x00"

// maxNodes bounds the whole value, which maxDepth and maxItems do not: they
// cap depth and per-level width separately, and the product of the two is
// enormous. Reading an unexported composite makes this matter — one
// *http.Request reaches a context, a transport and several sync.Maps. It is
// the one bound that stays a constant: it is a guard, not a preference.
const maxNodes = 2000

// maxDepth is per composite level. A pointer unwrap does not consume one, so
// this is how many nested structures deep a value is described before falling
// back to a flat %+v line. It can afford to be this deep only because cycles
// are detected: without that, depth is the only thing standing between a
// linked list and nonsense.
//
// maxDepth and maxItems are variables rather than constants so a session can
// raise them, but the source text does not change between sessions: the
// numbers arrive in the environment. Templating them into this file instead
// would give every non-default session a different program, which would miss
// the result cache on every :undo and rebuild the runtime file on every
// setting change.
// The defaults are exported because two other packages need the same numbers:
// eval decides whether a limit is worth putting in the child's environment,
// and config reports them as the settings' defaults. A second copy of 200 is
// a second thing to forget.
const (
	DefaultMaxDepth = 6
	DefaultMaxItems = 200

	// EnvMaxDepth and EnvMaxItems are the variables the child reads them from.
	EnvMaxDepth = "GLUON_MAX_DEPTH"
	EnvMaxItems = "GLUON_MAX_ITEMS"
)

var (
	maxDepth = __gluonLimit(EnvMaxDepth, DefaultMaxDepth)
	maxItems = __gluonLimit(EnvMaxItems, DefaultMaxItems)
)

// __gluonLimit reads one bound from the environment. Anything that is not a
// positive integer is the default, because the variable can also arrive from
// the user's own shell rather than from gluon, and a bound gluon cannot make
// sense of should behave as though it were not set at all.
func __gluonLimit(name string, def int) int {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil || n < 1 {
		return def
	}
	return n
}

var __gluonErrIface = reflect.TypeOf((*error)(nil)).Elem()

// __gluonUnsafeOK gates reading unexported composite fields. The mechanism is
// unsafe, so it gets an escape hatch, the way GLUON_NO_TYPECHECK is one for
// the checker.
var __gluonUnsafeOK = os.Getenv("GLUON_NO_UNSAFE") == ""

// __gluonRef identifies one addressable object. The type is part of the key
// because a struct and its first field share an address — &a and &a.B are the
// same uintptr and different values — and the length is, because s and s[0:1]
// share a first element.
type __gluonRef struct {
	p uintptr
	n int
	t reflect.Type
}

// __gluonState tracks object identity for one payload. Every object that
// enters it is labelled; deciding which labels are worth showing needs the
// whole tree, so that happens in pretty, where the whole tree exists.
//
// path holds the ancestors of the value being encoded right now, so a hit
// there is a cycle: the value is inside itself. done holds everything already
// finished, so a hit there is sharing: two places reach one object, which
// terminates when printed but hides that mutating through one is visible
// through the other. They are different lessons, so they are different sets.
//
// It must be created per __gluonPayload call and never live at package scope.
// gluon's own process calls Payload many times to answer constant expressions
// while the child calls it once, so a shared counter would number the same
// value differently in the two, and the byte-identity the constant fast path
// rests on would fail — intermittently, only after the first few constants.
type __gluonState struct {
	path  map[__gluonRef]int
	done  map[__gluonRef]int
	next  int
	nodes int
}

func __gluonNewState() *__gluonState {
	return &__gluonState{
		path:  map[__gluonRef]int{},
		done:  map[__gluonRef]int{},
		nodes: maxNodes,
	}
}

func (st *__gluonState) reset() {
	clear(st.path)
}

func (st *__gluonState) enter(ref __gluonRef) int {
	st.next++
	st.path[ref] = st.next
	return st.next
}

func (st *__gluonState) leave(ref __gluonRef, id int) {
	delete(st.path, ref)
	st.done[ref] = id
}

// seen reports a revisit: a cycle when the target is still on the current
// path, sharing when it has already been finished.
func (st *__gluonState) seen(ref __gluonRef) (kind string, id int, ok bool) {
	if id, ok := st.path[ref]; ok {
		return "cycle", id, true
	}
	if id, ok := st.done[ref]; ok {
		return "shared", id, true
	}
	return "", 0, false
}

// __gluonAddressable returns a value whose fields can be read through unsafe.
// A value that arrived through an interface or came out of a map is not
// addressable, and copying it into fresh storage is what makes UnsafeAddr
// legal on its fields.
//
// The copy is of the container only: pointers inside are copied by value, so
// object identity is unaffected.
func __gluonAddressable(v reflect.Value) reflect.Value {
	if v.CanAddr() || !v.CanInterface() || !__gluonUnsafeOK {
		return v
	}
	p := reflect.New(v.Type())
	p.Elem().Set(v)
	return p.Elem()
}

// __gluonDrain gives goroutines the session started a bounded chance to run.
//
// Without it `go func(){ fmt.Println("x") }()` prints nothing at all: main
// returns before the goroutine is ever scheduled, and a REPL that shows
// nothing teaches that goroutines do not work.
//
// It says what it did, every time. Waiting is a divergence from real Go —
// main does not wait — and a REPL that quietly papered over that would be
// teaching the wrong thing just as loudly.
//
// It waits only for goroutines a line of the session started: ones created by
// package main. A library keeps goroutines of its own for as long as the value
// that owns them — database/sql one per *sql.DB until Close, net/http one per
// idle connection — and waiting on one of those waited the whole deadline on
// every line after a sql.Open, to say a goroutine nobody wrote was still there.
func __gluonDrain() {
	n := __gluonOwnGoroutines()
	if n == 0 {
		return
	}
	start := time.Now()
	deadline := start.Add(2 * time.Second)
	left := n
	for left > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Microsecond)
		left = __gluonOwnGoroutines()
	}
	waited := time.Since(start).Round(time.Microsecond)
	if left > 0 {
		fmt.Fprintf(os.Stdout, "[gluon waited %v; %d goroutine(s) still running — "+
			"a real main would have exited immediately]\n", waited, left)
		return
	}
	fmt.Fprintf(os.Stdout, "[gluon waited %v for %d goroutine(s) — "+
		"a real main would have exited here]\n", waited, n)
}

// __gluonOwnGoroutines counts the live goroutines package main created, from
// the runtime's own record of where each one was started. The count alone
// cannot tell a line's goroutine from a library's, and the record can.
func __gluonOwnGoroutines() int {
	if runtime.NumGoroutine() <= 1 {
		return 0
	}
	buf := make([]byte, 64<<10)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, line := range bytes.Split(buf, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("created by main.")) {
			count++
		}
	}
	return count
}

// __gluonPrint encodes each value. A multi-value call such as
// __gluonPrint(f()) lands here as several arguments via Go's f(g()) rule.
func __gluonPrint(vs ...any) {
	os.Stdout.Write(__gluonPayload(vs...))
}

// __gluonPayload is the wire format: the marker, then one JSON array. It is
// split out from __gluonPrint because gluon's own process answers constant
// expressions without running a program at all, and calls this to produce a
// payload byte-identical to what the child would have written.
func __gluonPayload(vs ...any) []byte {
	b := make([]byte, 0, 256)
	b = append(b, Marker...)
	b = append(b, '[')
	// One state for the whole tuple, so a function returning (p, p) describes
	// the second as shared with the first rather than printing it twice.
	st := __gluonNewState()
	for i, v := range vs {
		if i > 0 {
			b = append(b, ',')
		}
		b = __gluonEnc(b, v, 0, st)
	}
	return append(b, ']', '\n')
}

// __gluonHdrs describes the runtime triple behind a slice or a string —
// pointer, len, cap — rather than its contents. Two slices that share a
// backing array are indistinguishable by their values and obvious by their
// pointers, and that aliasing is the thing worth being able to look at.
//
// The three fields are the runtime's own slice header, in its order, so the
// REPL describes a slice the way the language specification does.
func __gluonHdrs(vs ...any) {
	b := make([]byte, 0, 256)
	b = append(b, Marker...)
	b = append(b, '[')
	for i, v := range vs {
		if i > 0 {
			b = append(b, ',')
		}
		b = __gluonHdr(b, v)
	}
	os.Stdout.Write(append(b, ']', '\n'))
}

func __gluonHdr(b []byte, v any) []byte {
	if v == nil {
		return __gluonObj(b, kv{"k", "nil"}, kv{"t", ""}, kv{"r", "nil"})
	}
	rv := reflect.ValueOf(v)
	rt := rv.Type()

	switch rv.Kind() {
	case reflect.Slice:
		f := []kv{{"t", rt.String()}, {"k", "hdr"}, {"l", rv.Len()}, {"c", rv.Cap()},
			{"z", int(rt.Elem().Size())}}
		if rv.IsNil() {
			// A nil slice has no backing array at all: len 0, cap 0, no pointer.
			return __gluonObj(b, append(f, kv{"r", "nil"})...)
		}
		return __gluonObj(b, append(f, kv{"p", uint64(uintptr(rv.UnsafePointer()))})...)

	case reflect.String:
		str := rv.String()
		f := []kv{{"t", rt.String()}, {"k", "hdr"}, {"l", len(str)}, {"z", 1}}
		if len(str) == 0 {
			return __gluonObj(b, append(f, kv{"r", `""`})...)
		}
		// A string has a pointer and a length but no cap: it is immutable, so
		// there is nothing to grow into.
		return __gluonObj(b, append(f, kv{"p", uint64(uintptr(unsafe.Pointer(unsafe.StringData(str))))})...)
	}

	return __gluonObj(b, kv{"t", rt.String()}, kv{"k", "scalar"},
		kv{"r", "not a slice or string — no header to show"})
}

func __gluonEnc(b []byte, v any, depth int, st *__gluonState) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			// The panic unwound out of a partially entered path, so the
			// ancestors left behind would report false cycles.
			st.reset()
			out = __gluonObj(b, kv{"k", "scalar"}, kv{"t", "?"},
				kv{"r", fmt.Sprintf("<unprintable: %v>", r)})
		}
	}()
	if v == nil {
		// A nil error or nil interface arrives with its type erased by the
		// conversion to any, so there is nothing truthful to name.
		return __gluonObj(b, kv{"k", "nil"}, kv{"t", ""}, kv{"r", "nil"})
	}
	return __gluonEncVal(b, __gluonAddressable(reflect.ValueOf(v)), depth, st)
}

func __gluonEncVal(b []byte, rv reflect.Value, depth int, st *__gluonState) []byte {
	rt := rv.Type()
	fields := []kv{{"t", rt.String()}}

	// The node budget bounds the whole value; depth and item caps only bound
	// one dimension each.
	if st.nodes <= 0 {
		return __gluonObj(b, append(fields, kv{"k", "scalar"}, kv{"r", __gluonFlat(rv)})...)
	}
	st.nodes--

	switch rv.Kind() {
	case reflect.String:
		s := rv.String()
		fields = append(fields, kv{"k", "string"}, kv{"r", s},
			kv{"l", len(s)}, kv{"u", utf8.RuneCountInString(s)})
		return __gluonObj(b, fields...)

	// byte is an alias for uint8, so this is what s[i] lands on. Report the
	// number and the hex; a glyph only for printable ASCII, because a byte
	// >= 0x80 is a fragment of a multi-byte rune, not a character.
	case reflect.Uint8:
		n := rv.Uint()
		r := fmt.Sprintf("%d (0x%02x)", n, n)
		if n >= 32 && n <= 126 {
			r += fmt.Sprintf(" %q", rune(n))
		}
		return __gluonObj(b, append(fields, kv{"k", "scalar"}, kv{"r", r})...)

	// rune is an alias for int32.
	case reflect.Int32:
		n := rv.Int()
		r := strconv.FormatInt(n, 10)
		if c := rune(n); utf8.ValidRune(c) && unicode.IsPrint(c) {
			r += fmt.Sprintf(" %q", c)
		}
		return __gluonObj(b, append(fields, kv{"k", "scalar"}, kv{"r", r})...)

	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return __gluonObj(b, append(fields, kv{"k", "nil"}, kv{"r", "nil"})...)
		}
		fields = append(fields, kv{"k", "list"}, kv{"l", rv.Len()})
		if rv.Kind() == reflect.Slice {
			fields = append(fields, kv{"c", rv.Cap()})
		}
		if depth >= maxDepth {
			return __gluonObj(b, append(fields, kv{"r", __gluonFlat(rv)})...)
		}
		if rv.Kind() == reflect.Slice {
			ref := __gluonRef{uintptr(rv.UnsafePointer()), rv.Len(), rt}
			if kind, id, ok := st.seen(ref); ok {
				return __gluonObj(b, append(fields, kv{"k", kind}, kv{"d", id})...)
			}
			id := st.enter(ref)
			items, more := __gluonList(rv, depth, st)
			st.leave(ref, id)
			fields = append(fields, kv{"i", raw(items)}, kv{"d", id})
			if more > 0 {
				fields = append(fields, kv{"m", more})
			}
			return __gluonObj(b, fields...)
		}
		items, more := __gluonList(rv, depth, st)
		fields = append(fields, kv{"i", raw(items)})
		if more > 0 {
			fields = append(fields, kv{"m", more})
		}
		return __gluonObj(b, fields...)

	case reflect.Map:
		if rv.IsNil() {
			return __gluonObj(b, append(fields, kv{"k", "nil"}, kv{"r", "nil"})...)
		}
		fields = append(fields, kv{"k", "map"}, kv{"l", rv.Len()})
		if depth >= maxDepth {
			return __gluonObj(b, append(fields, kv{"r", __gluonFlat(rv)})...)
		}
		ref := __gluonRef{uintptr(rv.UnsafePointer()), 0, rt}
		if kind, id, ok := st.seen(ref); ok {
			return __gluonObj(b, append(fields, kv{"k", kind}, kv{"d", id})...)
		}
		id := st.enter(ref)
		keys, vals, more := __gluonMap(rv, depth, st)
		st.leave(ref, id)
		fields = append(fields, kv{"ky", raw(keys)}, kv{"i", raw(vals)}, kv{"d", id})
		if more > 0 {
			fields = append(fields, kv{"m", more})
		}
		return __gluonObj(b, fields...)

	case reflect.Struct:
		fields = append(fields, kv{"k", "struct"})
		if depth >= maxDepth {
			return __gluonObj(b, append(fields, kv{"r", __gluonFlat(rv)})...)
		}
		return __gluonObj(b, append(fields, kv{"f", raw(__gluonFields(rv, depth, st))})...)

	case reflect.Pointer:
		if rv.IsNil() {
			f := append(fields, kv{"k", "nil"}, kv{"r", "nil"})
			// The classic err != nil trap: a typed nil in a non-nil interface.
			if rt.Implements(__gluonErrIface) {
				f = append(f, kv{"n", "non-nil interface holding a nil pointer — err != nil is TRUE"})
			}
			return __gluonObj(b, f...)
		}
		ref := __gluonRef{rv.Pointer(), 0, rt}
		if kind, id, ok := st.seen(ref); ok {
			return __gluonObj(b, append(fields, kv{"k", kind}, kv{"d", id})...)
		}
		if rv.Elem().Kind() == reflect.Struct && depth < maxDepth {
			id := st.enter(ref)
			inner := __gluonEncVal(nil, rv.Elem(), depth, st)
			st.leave(ref, id)
			fields = append(fields, kv{"k", "ptr"}, kv{"i", raw("[" + string(inner) + "]")}, kv{"d", id})
			return __gluonObj(b, fields...)
		}
		return __gluonObj(b, append(fields, kv{"k", "scalar"}, kv{"r", __gluonFlat(rv)})...)

	case reflect.Chan:
		if rv.IsNil() {
			return __gluonObj(b, append(fields, kv{"k", "nil"}, kv{"r", "nil"})...)
		}
		return __gluonObj(b, append(fields, kv{"k", "scalar"},
			kv{"r", rt.String()}, kv{"l", rv.Len()}, kv{"c", rv.Cap()})...)

	case reflect.Func:
		return __gluonObj(b, append(fields, kv{"k", "scalar"}, kv{"r", rt.String()})...)

	case reflect.Interface:
		if rv.IsNil() {
			return __gluonObj(b, append(fields, kv{"k", "nil"}, kv{"r", "nil"})...)
		}
		// Describe what the interface holds. Falling through would reach
		// __gluonFlat, whose %+v calls String() or Error() and collapses the
		// structure — which is exactly how a wrapped error chain disappears.
		if depth < maxDepth {
			return __gluonEncVal(b, __gluonAddressable(rv.Elem()), depth, st)
		}
	}

	return __gluonObj(b, append(fields, kv{"k", "scalar"}, kv{"r", __gluonFlat(rv)})...)
}

func __gluonList(rv reflect.Value, depth int, st *__gluonState) (string, int) {
	n := rv.Len()
	more := 0
	if n > maxItems {
		more = n - maxItems
		n = maxItems
	}
	out := []byte{'['}
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = __gluonEncVal(out, rv.Index(i), depth+1, st)
	}
	return string(append(out, ']')), more
}

// __gluonMap emits keys and values in a stable order. Go randomizes map
// iteration, and unstable output would be noise in a REPL.
func __gluonMap(rv reflect.Value, depth int, st *__gluonState) (string, string, int) {
	keys := rv.MapKeys()
	sort.Slice(keys, func(i, j int) bool {
		return __gluonFlat(keys[i]) < __gluonFlat(keys[j])
	})
	more := 0
	if len(keys) > maxItems {
		more = len(keys) - maxItems
		keys = keys[:maxItems]
	}
	kb, vb := []byte{'['}, []byte{'['}
	for i, k := range keys {
		if i > 0 {
			kb, vb = append(kb, ','), append(vb, ',')
		}
		kb = __gluonEncVal(kb, k, depth+1, st)
		// A map value is never addressable, so unexported composites inside
		// one need a fresh copy of their own.
		vb = __gluonEncVal(vb, __gluonAddressable(rv.MapIndex(k)), depth+1, st)
	}
	return string(append(kb, ']')), string(append(vb, ']')), more
}

func __gluonFields(rv reflect.Value, depth int, st *__gluonState) string {
	rt := rv.Type()
	out := []byte{'['}
	for i := 0; i < rt.NumField(); i++ {
		if i > 0 {
			out = append(out, ',')
		}
		f := rt.Field(i)
		out = append(out, '{')
		out = __gluonStr(out, "n")
		out = append(out, ':')
		out = __gluonStr(out, f.Name)
		out = append(out, ',')
		out = __gluonStr(out, "v")
		out = append(out, ':')
		fv := rv.Field(i)
		if !fv.CanInterface() && fv.CanAddr() && __gluonUnsafeOK {
			// UnsafeAddr checks only the addressable bit, never the read-only
			// one, and NewAt hands back a value without it — which is what
			// makes an unexported composite readable. fmt already reads these
			// through %+v, so this shows the same information structurally
			// rather than flattened; it is what makes a %w chain visible.
			fv = reflect.NewAt(f.Type, unsafe.Pointer(fv.UnsafeAddr())).Elem()
		}
		if fv.CanInterface() {
			out = __gluonEncVal(out, fv, depth+1, st)
		} else {
			// Interface() panics on unexported fields; scalars are still
			// readable through the kind-specific accessors.
			out = __gluonObj(out, kv{"t", f.Type.String()}, kv{"k", "scalar"},
				kv{"r", __gluonUnexported(fv)})
		}
		out = append(out, '}')
	}
	return string(append(out, ']'))
}

func __gluonUnexported(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return strconv.Quote(v.String())
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	}
	return "<unexported>"
}

// __gluonFlat is the one-line fallback used past the depth cap.
func __gluonFlat(v reflect.Value) string {
	if !v.CanInterface() {
		return __gluonUnexported(v)
	}
	if v.Kind() == reflect.String {
		return strconv.Quote(v.String())
	}
	return fmt.Sprintf("%+v", v.Interface())
}

// --- minimal JSON writing -------------------------------------------------

type kv struct {
	k string
	v any
}

// raw marks a string that is already JSON and must not be quoted.
type raw string

func __gluonObj(b []byte, fields ...kv) []byte {
	b = append(b, '{')
	for i, f := range fields {
		if i > 0 {
			b = append(b, ',')
		}
		b = __gluonStr(b, f.k)
		b = append(b, ':')
		switch val := f.v.(type) {
		case raw:
			b = append(b, val...)
		case string:
			b = __gluonStr(b, val)
		case int:
			b = strconv.AppendInt(b, int64(val), 10)
		case uint64:
			b = strconv.AppendUint(b, val, 10)
		default:
			b = __gluonStr(b, fmt.Sprint(val))
		}
	}
	return append(b, '}')
}

func __gluonStr(b []byte, s string) []byte {
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if r < 0x20 {
				b = append(b, []byte(fmt.Sprintf("\\u%04x", r))...)
				continue
			}
			b = utf8.AppendRune(b, r)
		}
	}
	return append(b, '"')
}
