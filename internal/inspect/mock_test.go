package inspect

import (
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// iface resolves one interface out of a session, the way :mock does.
func iface(t *testing.T, name string, decls ...string) (string, string) {
	t.Helper()
	r, ops := probe(t, []string{name}, decls...)
	in, err := ops[0].Iface()
	if err != nil {
		t.Fatalf("%s is not an interface: %v", name, err)
	}
	mock, err := Mock(r.Pkg, name, in, false)
	if err != nil {
		t.Fatalf("Mock: %v", err)
	}
	spy, err := Mock(r.Pkg, name, in, true)
	if err != nil {
		t.Fatalf("Spy: %v", err)
	}
	return mock, spy
}

var spaces = regexp.MustCompile(`[ \t]+`)

// holds is strings.Contains through gofmt's alignment: a struct field's type
// is padded out to line up with its neighbours, and the test is about what the
// field is, not about how wide the widest one happened to be.
func holds(src, want string) bool {
	return strings.Contains(spaces.ReplaceAllString(src, " "), spaces.ReplaceAllString(want, " "))
}

// parses is the floor under every one of these: source a person is meant to
// paste has to be Go before it is anything else.
func parses(t *testing.T, src string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "", "package main\n\n"+src, 0); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, src)
	}
}

// TestMockCoversTheMethodSet: one function field and one dispatching method
// per method the interface requires.
func TestMockCoversTheMethodSet(t *testing.T) {
	mock, _ := iface(t, "Store", "type Store interface { Get(k string) (string, error); Put(k, v string) error; Len() int }")
	parses(t, mock)

	for _, want := range []string{
		"type StoreMock struct {",
		"GetFunc func(k string) (string, error)",
		"PutFunc func(k string, v string) error",
		"LenFunc func() int",
		"func (m *StoreMock) Get(k string) (string, error) {",
		"func (m *StoreMock) Put(k string, v string) error {",
		"func (m *StoreMock) Len() int {",
		"return m.GetFunc(k)",
		"return m.PutFunc(k, v)",
	} {
		if !holds(mock, want) {
			t.Errorf("mock is missing %q:\n%s", want, mock)
		}
	}
}

// TestMockCoversPromotedMethods. types.NewMethodSet already walks embedded
// interfaces, which is the whole reason :mock uses it rather than ranging over
// the interface's explicit methods.
func TestMockCoversPromotedMethods(t *testing.T) {
	mock, _ := iface(t, "ReadCloser",
		"type Reader interface{ Read(p []byte) (int, error) }",
		"type ReadCloser interface { Reader; Close() error }")
	parses(t, mock)

	for _, want := range []string{"ReadFunc func(p []byte) (int, error)", "CloseFunc func() error"} {
		if !holds(mock, want) {
			t.Errorf("mock is missing the embedded interface's %q:\n%s", want, mock)
		}
	}
}

// TestMockOfAStdlibInterface keeps the qualifier honest: a type from another
// package keeps its package name, which is what makes the source paste-able.
func TestMockOfAStdlibInterface(t *testing.T) {
	mock, _ := iface(t, "io.ReadWriter", "var _ = io.Discard")
	parses(t, mock)
	if !strings.Contains(mock, "type ReadWriterMock struct {") {
		t.Errorf("mock is not named after the interface:\n%s", mock)
	}
	if !strings.Contains(mock, "mock implementation of io.ReadWriter") {
		t.Errorf("mock does not say what it implements:\n%s", mock)
	}
}

// TestUnsetMethodPanicsNamingItself is the decision the whole shape rests on:
// zero values returned silently are how a mock makes a test pass for the wrong
// reason, and the panic is the only outcome that cannot be mistaken for a real
// result.
func TestUnsetMethodPanicsNamingItself(t *testing.T) {
	mock, spy := iface(t, "Store", "type Store interface{ Get(k string) (string, error) }")
	for _, src := range []string{mock, spy} {
		if !holds(src, `panic("StoreMock.Get was called with no GetFunc set")`) &&
			!holds(src, `panic("StoreSpy.Get was called with no GetFunc set")`) {
			t.Errorf("the unset path does not panic naming the method:\n%s", src)
		}
	}
}

// TestSpyRecordsCallsAndCounts. The count is len(Calls) read back by a method
// rather than a second field, so the two can never disagree.
func TestSpyRecordsCallsAndCounts(t *testing.T) {
	_, spy := iface(t, "Store", "type Store interface { Get(k string) (string, error); Ping() }")
	parses(t, spy)

	for _, want := range []string{
		"GetCalls []struct {",
		"K string",
		"m.GetCalls = append(m.GetCalls, struct {",
		"}{k})",
		"func (m *StoreSpy) GetCount() int { return len(m.GetCalls) }",
		// A method with no parameters still records that it was called.
		"PingCalls []struct{}",
		"m.PingCalls = append(m.PingCalls, struct{}{})",
		"func (m *StoreSpy) PingCount() int { return len(m.PingCalls) }",
	} {
		if !holds(spy, want) {
			t.Errorf("spy is missing %q:\n%s", want, spy)
		}
	}
}

// TestVariadicAndUnnamedParameters covers the two ways an interface method's
// parameters are not simply names with types: an interface may declare none at
// all, and the last one may be variadic.
func TestVariadicAndUnnamedParameters(t *testing.T) {
	mock, spy := iface(t, "Logger", "type Logger interface { Logf(format string, args ...any); Emit(int, string) }")
	parses(t, mock)
	parses(t, spy)

	for _, want := range []string{
		"LogfFunc func(format string, args ...any)",
		"m.LogfFunc(format, args...)",
		// Unnamed parameters become positional, because a blank one cannot be
		// forwarded.
		"EmitFunc func(a0 int, a1 string)",
		"m.EmitFunc(a0, a1)",
	} {
		if !holds(mock, want) {
			t.Errorf("mock is missing %q:\n%s", want, mock)
		}
	}
	// What was passed to a variadic method is a slice, whatever the
	// declaration spells.
	if !holds(spy, "Args []any") {
		t.Errorf("spy does not record the variadic arguments as a slice:\n%s", spy)
	}
}

// TestMockRefusesWhatItCannotImplement. An unexported method can only be
// supplied by the package that declared it, so a struct with the field would
// read right and not compile.
func TestMockRefusesWhatItCannotImplement(t *testing.T) {
	r, ops := probe(t, []string{"testing.TB"}, "var _ = testing.Verbose")
	in, err := ops[0].Iface()
	if err != nil {
		t.Fatalf("testing.TB is not an interface: %v", err)
	}
	if _, err := Mock(r.Pkg, "testing.TB", in, false); err == nil {
		t.Error("Mock generated a type for an interface with an unexported method")
	} else if !strings.Contains(err.Error(), "unexported") {
		t.Errorf("error = %q, want it to say the method is unexported", err)
	}
}

// TestMockOfAnEmptyInterface says so rather than emitting a struct with
// nothing in it.
func TestMockOfAnEmptyInterface(t *testing.T) {
	r, ops := probe(t, []string{"Any"}, "type Any interface{}")
	in, err := ops[0].Iface()
	if err != nil {
		t.Fatalf("Any is not an interface: %v", err)
	}
	if _, err := Mock(r.Pkg, "Any", in, false); err == nil {
		t.Error("Mock generated a type for an interface with no methods")
	} else if !strings.Contains(err.Error(), "no methods") {
		t.Errorf("error = %q, want it to say the interface has no methods", err)
	}
}
