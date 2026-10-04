//go:build integration

// These tests actually invoke the Go toolchain. Run with:
//
//	go test -tags=integration ./internal/eval/
package eval

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// display renders what a program described, the way a pipe would show it.
// eval itself only carries the encoded value now; formatting lives in pretty.
func display(raw string) string {
	userOut, vals := pretty.Parse(raw)
	if len(vals) == 0 {
		return userOut
	}
	return userOut + pretty.Plain(vals)
}

func run(t *testing.T, srcs ...string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ev.Close() })

	s := &session.Session{}
	var out string
	for _, src := range srcs {
		e, err := session.Classify(src)
		if err != nil {
			t.Fatalf("Classify(%q): %v", src, err)
		}
		s.Append(e)
		res, err := ev.Eval(s)
		if err != nil {
			s.Pop()
			out = "ERROR: " + err.Error()
			continue
		}
		out = display(res.Output)
	}
	return out
}

func TestAutoImportAndUnusedSuppression(t *testing.T) {
	// Neither the slices import nor the unused x is typed by the user.
	got := run(t, "x := []int{3,1,2}", "slices.Sort(x)", "x")
	if !strings.Contains(got, "[1 2 3]") {
		t.Errorf("got %q, want sorted slice", got)
	}
	if !strings.Contains(got, "len=3 cap=3") {
		t.Errorf("got %q, want len/cap annotation", got)
	}
}

// Byte/rune confusion is a known trip-hazard, so the printer has to show the
// number and the encoding, not a fake glyph.
func TestByteRuneTripHazard(t *testing.T) {
	if got := run(t, `"héllo"[1]`); !strings.Contains(got, "195") || !strings.Contains(got, "0xc3") {
		t.Errorf("got %q, want the byte value and its hex", got)
	}
	if got := run(t, `"héllo"`); !strings.Contains(got, "len=6 bytes, 5 runes") {
		t.Errorf("got %q, want bytes-vs-runes annotation", got)
	}
}

func TestZeroResultCallIsNotPrinted(t *testing.T) {
	// slices.Sort returns nothing; wrapping it in a print must not be fatal.
	if got := run(t, "x := []int{2,1}", "slices.Sort(x)", "len(x)"); !strings.Contains(got, "(int) 2") {
		t.Errorf("got %q, want (int) 2", got)
	}
}

func TestMultiValueArity(t *testing.T) {
	if got := run(t, `strconv.Atoi("42")`); !strings.Contains(got, "(int) 42") || !strings.Contains(got, "nil") {
		t.Errorf("got %q, want both return values", got)
	}
}

func TestBadLineDoesNotPoisonSession(t *testing.T) {
	if got := run(t, "x := 5", "fooo(1)", "x * 2"); !strings.Contains(got, "(int) 10") {
		t.Errorf("got %q, want the session to survive a bad line", got)
	}
}

// Replayed output is muted at the fd level, so nondeterministic programs do
// not smear across evaluations the way an output diff would.
func TestReplayedOutputIsMuted(t *testing.T) {
	got := run(t, `m := map[string]int{"a":1,"b":2,"c":3}`,
		"for k := range m { fmt.Println(k) }", "1+1")
	if strings.Contains(got, "a") || strings.Contains(got, "b") {
		t.Errorf("got %q, want only the newest entry's output", got)
	}
	if !strings.Contains(got, "(int) 2") {
		t.Errorf("got %q, want (int) 2", got)
	}
}

func TestIdenticalOutputIsNotSwallowed(t *testing.T) {
	if got := run(t, "1+1", "1+1"); !strings.Contains(got, "(int) 2") {
		t.Errorf("got %q, want repeated identical output to still print", got)
	}
}

// The constant fast path answers from go/types without building or running
// anything. That is only safe if it produces exactly what a real run would
// have produced, so this evaluates both ways and compares.
//
// The trap it guards is quiet: 'a' is an untyped rune whose default type is
// int32, and the encoder prints int32 as "97 'a'" while a plain integer prints
// as "97". A second formatting implementation would drift on cases like that.
func TestConstantFastPathMatchesRunning(t *testing.T) {
	exprs := []string{
		"1 + 2",
		`len("héllo")`,
		"'a'",
		"'é'",
		`"ab" + "cd"`,
		"1 < 2",
		"3.0 / 4.0",
		"1.0 / 3.0",
		"math.MaxInt64",
		"math.Pi",
		"byte(200)",
		"rune(0x4e2d)",
		"int8(-5)",
		"uint64(1) << 40",
		`"tab\there"`,
		`""`,
		"0",
		"-0.0",
	}

	for _, expr := range exprs {
		t.Run(expr, func(t *testing.T) {
			fast := evalOnce(t, expr, false)
			slow := evalOnce(t, expr, true)
			if fast != slow {
				t.Errorf("constant fast path diverged\n  expr:     %s\n  fast:     %q\n  running:  %q", expr, fast, slow)
			}
			if fast == "" {
				t.Errorf("%s produced no output", expr)
			}
		})
	}
}

// evalOnce evaluates a single expression in a fresh session, optionally with
// the type checker disabled so the toolchain is the only authority.
func evalOnce(t *testing.T, expr string, noTypecheck bool) string {
	t.Helper()
	if noTypecheck {
		t.Setenv("GLUON_NO_TYPECHECK", "1")
	} else {
		t.Setenv("GLUON_NO_TYPECHECK", "")
	}
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	s := &session.Session{}
	entry, err := session.Classify(expr)
	if err != nil {
		t.Fatalf("classify %q: %v", expr, err)
	}
	s.Append(entry)

	res, err := ev.Eval(s)
	if err != nil {
		t.Fatalf("eval %q (no-typecheck=%v): %v", expr, noTypecheck, err)
	}
	return res.Output
}

// A named type carries its own String method, so its constant must not be
// answered from go/types: time.Nanosecond is 1 as a constant but prints "1ns".
func TestNamedConstantStillRuns(t *testing.T) {
	got := display(evalOnce(t, "time.Nanosecond", false))
	if !strings.Contains(got, "1ns") {
		t.Errorf("time.Nanosecond = %q, want it to contain 1ns", got)
	}
}

// A constant appended to a session that panics must still run rather than
// being answered from go/types.
//
// The panic's own message is not visible on the second line: it belongs to a
// replayed entry, and rendering mutes everything before the newest one. What
// must survive is the non-zero exit — the evidence that the program really ran.
func TestConstantAfterPanicStillRuns(t *testing.T) {
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	s := &session.Session{}
	for _, src := range []string{`panic("boom")`, "1 + 1"} {
		entry, err := session.Classify(src)
		if err != nil {
			t.Fatal(err)
		}
		s.Append(entry)
		res, err := ev.Eval(s)
		if err != nil {
			t.Fatalf("eval %q: %v", src, err)
		}
		switch src {
		case `panic("boom")`:
			// The newest entry is unmuted, so this one does show its message.
			if !strings.Contains(res.Output, "boom") {
				t.Errorf("panic output = %q, want the message", res.Output)
			}
			if res.ExitCode == 0 {
				t.Error("a panicking program exited 0")
			}
		case "1 + 1":
			if res.ExitCode == 0 {
				t.Errorf("1+1 after a panic exited 0; the constant path skipped the run (output %q)", res.Output)
			}
		}
	}
}

// The checker reports type errors without building. The message has to keep
// naming what the user typed, not a line number in a file they never see.
func TestTypeErrorIsReportedWithSource(t *testing.T) {
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	s := &session.Session{}
	entry, err := session.Classify(`"a" + 1`)
	if err != nil {
		t.Fatal(err)
	}
	s.Append(entry)

	_, err = ev.Eval(s)
	if err == nil {
		t.Fatal(`"a" + 1 was accepted`)
	}
	if !strings.Contains(err.Error(), `"a" + 1`) {
		t.Errorf("error = %q, want it to quote the expression", err.Error())
	}
}

// Rendering emits a //line directive per entry so the compiler, go/types and
// panic tracebacks all report positions against what the user typed. gofmt
// then moves those directives, and it moves them differently depending on
// where they are: inline before a statement, hoisted onto its own line before
// a declaration. render.ColumnShift and render.DeclLineShift compensate.
//
// This test exists so a change in gofmt's behaviour fails loudly here rather
// than quietly pointing every error at the wrong character.
func TestDiagnosticsPointAtWhatTheUserTyped(t *testing.T) {
	tests := []struct {
		name string
		// lines are typed in order; the last one is expected to fail.
		lines []string
		// wantLine is the source line the error should quote, and wantUnder
		// the token the caret should sit under.
		wantLine  string
		wantUnder string
	}{
		{
			name:      "single-line statement",
			lines:     []string{"x := 1", `x + "b"`},
			wantLine:  `x + "b"`,
			wantUnder: "x",
		},
		{
			name:      "column inside a single-line statement",
			lines:     []string{"var y int = nope"},
			wantLine:  "var y int = nope",
			wantUnder: "nope",
		},
		{
			name: "multi-line declaration",
			lines: []string{
				"func f(a int) int {\n\treturn a + bogus\n}",
			},
			wantLine:  "\treturn a + bogus",
			wantUnder: "bogus",
		},
		{
			name: "multi-line statement",
			lines: []string{
				"if true {\n\tz := missing\n\t_ = z\n}",
			},
			wantLine:  "\tz := missing",
			wantUnder: "missing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer ev.Close()

			s := &session.Session{}
			var evalErr error
			for _, line := range tc.lines {
				entry, cerr := session.Classify(line)
				if cerr != nil {
					t.Fatalf("classify %q: %v", line, cerr)
				}
				s.Append(entry)
				if _, evalErr = ev.Eval(s); evalErr != nil {
					s.Pop()
					break
				}
			}
			if evalErr == nil {
				t.Fatal("expected the last line to fail")
			}

			got := evalErr.Error()
			assertCaret(t, got, tc.wantLine, tc.wantUnder)
		})
	}
}

// assertCaret checks that the quoted source line is the right one and that the
// caret beneath it sits under the expected token.
func assertCaret(t *testing.T, got, wantLine, wantUnder string) {
	t.Helper()
	lines := strings.Split(got, "\n")
	for i, line := range lines {
		quoted, ok := strings.CutPrefix(line, "    ")
		if !ok || quoted != wantLine {
			continue
		}
		if i+1 >= len(lines) {
			t.Fatalf("no caret line after the quoted source:\n%s", got)
		}
		caret, ok := strings.CutPrefix(lines[i+1], "    ")
		if !ok {
			t.Fatalf("caret line is not indented like the source:\n%s", got)
		}
		at := strings.IndexByte(caret, '^')
		if at < 0 {
			t.Fatalf("no caret found:\n%s", got)
		}
		if at >= len(quoted) {
			t.Fatalf("caret at %d is past the end of %q:\n%s", at, quoted, got)
		}
		if under := quoted[at:]; !strings.HasPrefix(under, wantUnder) {
			t.Errorf("caret points at %q, want it under %q:\n%s", under, wantUnder, got)
		}
		return
	}
	t.Fatalf("the error does not quote %q:\n%s", wantLine, got)
}

// A panic names the synthetic files the //line directives created, which mean
// nothing on their own. Every frame in the session's own code is replaced with
// the source it stands for.
func TestPanicTracebackNamesTheSource(t *testing.T) {
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	s := &session.Session{}
	var res Result
	for _, line := range []string{"x := []int{1, 2, 3}", "x[10]"} {
		entry, cerr := session.Classify(line)
		if cerr != nil {
			t.Fatal(cerr)
		}
		s.Append(entry)
		if res, err = ev.Eval(s); err != nil {
			t.Fatalf("eval %q: %v", line, err)
		}
	}

	if res.ExitCode == 0 {
		t.Fatal("indexing past the end did not panic")
	}
	if !strings.Contains(res.Output, "index out of range") {
		t.Errorf("output = %q, want the panic message", res.Output)
	}
	if !strings.Contains(res.Output, "x[10]") {
		t.Errorf("output = %q, want the frame replaced with its source", res.Output)
	}
	for _, leak := range []string{"gluon-in-", "/var/folders", "/private"} {
		if strings.Contains(res.Output, leak) {
			t.Errorf("output leaks %q, which the user has never seen:\n%s", leak, res.Output)
		}
	}
}

// TestTransientDoesNotPoisonImportCache pins the reason EvalTransient restores
// evaluator state. A transient that needs an import the session does not have
// would otherwise leave it in the cached block, and the next ordinary line
// would write that block verbatim and fail to build with "imported and not
// used" before recovering the expensive way.
func TestTransientDoesNotPoisonImportCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ev.Close() })

	s := &session.Session{}
	e, err := session.Classify(`x := 1`)
	if err != nil {
		t.Fatal(err)
	}
	s.Append(e)
	if _, err := ev.Eval(s); err != nil {
		t.Fatal(err)
	}

	before := append([]render.ImportSpec(nil), ev.imports...)
	wasResolved := ev.resolved

	// strconv is not in the session; the transient is the only thing needing it.
	transient, err := session.Classify(`strconv.Itoa(x)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.EvalTransient(s, transient); err != nil {
		t.Fatalf("EvalTransient: %v", err)
	}

	if ev.resolved != wasResolved {
		t.Errorf("resolved changed: got %v, want %v", ev.resolved, wasResolved)
	}
	if len(ev.imports) != len(before) {
		t.Fatalf("import cache changed: got %v, want %v", ev.imports, before)
	}
	for i := range before {
		if ev.imports[i] != before[i] {
			t.Fatalf("import cache changed: got %v, want %v", ev.imports, before)
		}
	}

	// And the next ordinary line still evaluates without a recovery build.
	next, err := session.Classify(`x + 1`)
	if err != nil {
		t.Fatal(err)
	}
	s.Append(next)
	res, err := ev.Eval(s)
	if err != nil {
		t.Fatalf("line after transient: %v", err)
	}
	if got := display(res.Output); !strings.Contains(got, "2") {
		t.Errorf("after transient, got %q, want it to contain 2", got)
	}
}

// Analyze appends the inspected expression and pops it, but it also renders,
// and rendering resolves imports. :t strings.Builder on a session that never
// imported strings used to leave it in the cached block, so the next ordinary
// line wrote it verbatim and failed to build.
func TestAnalyzeDoesNotPoisonImportCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ev.Close() })

	s := &session.Session{}
	e, err := session.Classify("x := 1")
	if err != nil {
		t.Fatal(err)
	}
	s.Append(e)
	if _, err := ev.Eval(s); err != nil {
		t.Fatal(err)
	}

	before := append([]render.ImportSpec(nil), ev.imports...)
	if _, err := ev.Analyze(s, "strings.Builder{}"); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(ev.imports) != len(before) {
		t.Fatalf("import cache changed: got %v, want %v", ev.imports, before)
	}

	next, err := session.Classify("x + 1")
	if err != nil {
		t.Fatal(err)
	}
	s.Append(next)
	res, err := ev.Eval(s)
	if err != nil {
		t.Fatalf("line after Analyze: %v", err)
	}
	if got := display(res.Output); !strings.Contains(got, "2") {
		t.Errorf("got %q, want 2", got)
	}
}

// drainWaiting is a function for a session to declare, so that a goroutine
// can wait for the drain itself: it reports main asleep inside the drain's
// wait, which main enters only after counting a goroutine of the session's
// still running. A goroutine that finishes only once this is true is one the
// drain cannot miss.
//
// The tests below used to race main instead. A goroutine that prints at once
// could print and exit before the drain counted it — on a busy CI runner it
// did — and the drain then rightly said nothing, because there was nothing
// left to wait for. A sleep before printing only made that rarer.
var drainWaiting = `func drainWaiting() bool {
	buf := make([]byte, 1<<20)
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		if strings.Contains(g, "time.Sleep(") && strings.Contains(g, "main.` + render.DrainFunc + `(") {
			return true
		}
	}
	return false
}`

// A goroutine's output used to be lost entirely: main returned before it was
// ever scheduled.
func TestGoroutineOutputIsNotLost(t *testing.T) {
	got := run(t, drainWaiting,
		`go func() { for !drainWaiting() { time.Sleep(time.Millisecond) }; fmt.Println("from goroutine") }()`)
	if !strings.Contains(got, "from goroutine") {
		t.Errorf("got %q, want the goroutine's output", got)
	}
	// Waiting is a divergence from real Go, so it has to be stated.
	if !strings.Contains(got, "gluon waited") {
		t.Errorf("got %q, want the wait to be reported", got)
	}
}

// TestALibrarysGoroutineIsNotWaitedFor: a goroutine a library keeps for as
// long as the value that owns it — os/signal's here, database/sql's for every
// open *sql.DB — is not one a line started, and waiting on it cost the whole
// deadline on every later line, to report it.
func TestALibrarysGoroutineIsNotWaitedFor(t *testing.T) {
	got := run(t, `signal.Notify(make(chan os.Signal, 1), os.Interrupt)`, `fmt.Sprint("after")`)
	if strings.Contains(got, "gluon waited") {
		t.Errorf("got %q, want no wait for os/signal's own goroutine", got)
	}
}

// TestALinesGoroutineIsStillWaitedFor is the other half: a goroutine the
// session started, even from inside a function it declared, is waited for
// and said.
func TestALinesGoroutineIsStillWaitedFor(t *testing.T) {
	got := run(t, drainWaiting,
		`func spawn() { go func() { for !drainWaiting() { time.Sleep(time.Millisecond) }; fmt.Println("late") }() }`,
		`spawn()`)
	if !strings.Contains(got, "late") || !strings.Contains(got, "gluon waited") {
		t.Errorf("got %q, want the goroutine's output and the wait said", got)
	}
}

func TestNoGoroutineMeansNoNote(t *testing.T) {
	if got := run(t, "1+1"); strings.Contains(got, "gluon waited") {
		t.Errorf("got %q, want no note when nothing was spawned", got)
	}
}

// A cyclic list used to render as four distinct nodes ending in a hex address,
// which reads as a chain rather than a loop.
func TestCyclicValueIsReportedAsCyclic(t *testing.T) {
	got := run(t,
		"type Node struct { Val int; Next *Node }",
		"a := &Node{Val: 1}",
		"b := &Node{Val: 2}",
		"a.Next = b",
		"b.Next = a",
		"a",
	)
	if !strings.Contains(got, "cycle") || !strings.Contains(got, "cyclic") {
		t.Errorf("got %q, want the cycle named", got)
	}
	if strings.Contains(got, "0x") {
		t.Errorf("got %q, want no raw address", got)
	}
}

// Sharing terminates when printed, so the danger is the opposite one: it looks
// like two objects when mutating through either is visible through the other.
func TestSharedPointerIsReportedAsShared(t *testing.T) {
	got := run(t, "type P struct { X int }", "p := &P{X: 1}", "[]*P{p, p}")
	if !strings.Contains(got, "shared") {
		t.Errorf("got %q, want the sharing named", got)
	}
}

func TestOrdinaryValuesCarryNoLabels(t *testing.T) {
	got := run(t, "type P struct { X int }", "[]*P{{X:1},{X:2}}")
	if strings.Contains(got, "#") {
		t.Errorf("got %q, want no identity labels", got)
	}
}

// %w is the Go error idiom, and the wrapped error used to render as
// <unexported> — the whole chain invisible.
func TestWrappedErrorChainIsVisible(t *testing.T) {
	got := run(t, `fmt.Errorf("layer2: %w", fmt.Errorf("layer1: %w", os.ErrNotExist))`)
	for _, want := range []string{"layer2", "layer1", "file does not exist"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "<unexported>") {
		t.Errorf("got %q, want the chain rather than <unexported>", got)
	}
}

func TestSelfReferentialMapTerminates(t *testing.T) {
	got := run(t, `m := map[string]any{}`, `m["self"] = m`, "m")
	if !strings.Contains(got, "cycle") {
		t.Errorf("got %q, want the cycle named", got)
	}
}

// it is the previous line's value, and the whole session replays on every
// line, so each entry that says it must resolve to its own predecessor.
func TestItChainsAcrossEntries(t *testing.T) {
	if got := run(t, "1+1", "it*2", "it+1"); !strings.Contains(got, "5") {
		t.Errorf("got %q, want 5", got)
	}
}

func TestItSurvivesATypeChange(t *testing.T) {
	if got := run(t, "2+2", `"hi"`, `it + "!"`); !strings.Contains(got, "hi!") {
		t.Errorf("got %q, want hi!", got)
	}
}

func TestOrdinalAddressesAnEarlierValue(t *testing.T) {
	if got := run(t, `"hi"`, "1+1", `_1 + "!"`); !strings.Contains(got, "hi!") {
		t.Errorf("got %q, want hi!", got)
	}
}

func TestOrdinalsSkipVoidCalls(t *testing.T) {
	got := run(t, "x := []int{3,1,2}", "len(x)", "slices.Sort(x)", "it * 2")
	if !strings.Contains(got, "6") {
		t.Errorf("got %q, want 6 — the void call should consume no ordinal", got)
	}
}

// Binding a value on the way past must not evaluate it twice.
func TestBindingEvaluatesOnce(t *testing.T) {
	got := run(t,
		"n := 0",
		"func() int { n++; return n }()",
		"it",
		"n",
	)
	if !strings.Contains(got, "1") || strings.Contains(got, "2") {
		t.Errorf("got %q, want n to still be 1", got)
	}
}

func TestUserBoundItWins(t *testing.T) {
	if got := run(t, "1+1", `it := "mine"`, `it + "!"`); !strings.Contains(got, "mine!") {
		t.Errorf("got %q, want mine!", got)
	}
}

// The refusal names the ordinal the user typed, and the session survives it.
func TestMultiValueOrdinalIsRefused(t *testing.T) {
	if got := run(t, `strconv.Atoi("12")`, "it + 1"); !strings.Contains(got, "_1") {
		t.Errorf("got %q, want a refusal naming _1", got)
	}
	if got := run(t, `strconv.Atoi("12")`, "it + 1", "1+1"); !strings.Contains(got, "2") {
		t.Errorf("got %q, want the session to survive the refusal", got)
	}
}

// :layout is a pure type-checker answer, so this pins the arithmetic rather
// than the toolchain: the same struct reordered is 8 bytes smaller.
func TestStructLayoutMatchesUnsafeSizeof(t *testing.T) {
	got := run(t,
		"type P struct { A bool; B int64; C bool; D string }",
		"unsafe.Sizeof(P{})",
	)
	if !strings.Contains(got, "40") {
		t.Errorf("got %q, want 40", got)
	}
	got = run(t,
		"type Q struct { B int64; D string; A bool; C bool }",
		"unsafe.Sizeof(Q{})",
	)
	if !strings.Contains(got, "32") {
		t.Errorf("got %q, want 32", got)
	}
}

// :esc's whole correctness rests on not reporting gluon's own printer.
// __gluonPrint is variadic ...any, so passing a value to it forces the value
// to the heap; rendering through EscSink is what makes the answer a property
// of the user's expression.
//
// The controls differ only in a constant: 32 bytes fits the implicit stack
// limit and 1 MiB does not, so neither depends on inlining budget or session
// state.
func TestEscapeAnalysisIsNotAboutThePrinter(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ev.Close() })

	esc := func(expr string) string {
		e, err := session.Classify(expr)
		if err != nil {
			t.Fatal(err)
		}
		res, err := ev.EscapeAnalysis(&session.Session{}, e)
		if err != nil {
			t.Fatalf("EscapeAnalysis(%q): %v", expr, err)
		}
		var b strings.Builder
		for _, m := range append(res.Here, res.Elsewhere...) {
			b.WriteString(m.Text + "\n")
		}
		return b.String()
	}

	if got := esc("make([]byte, 32)"); !strings.Contains(got, "does not escape") {
		t.Errorf("small allocation: got %q, want it to stay on the stack", got)
	}
	if got := esc("make([]byte, 1<<20)"); !strings.Contains(got, "escapes to heap") {
		t.Errorf("large allocation: got %q, want it on the heap", got)
	}

	// The anti-regression half: through the printer the same small allocation
	// escapes. If someone ever "simplifies" EscSink back into a ...any call,
	// this is what fails.
	s := &session.Session{}
	e, err := session.Classify("make([]byte, 32)")
	if err != nil {
		t.Fatal(err)
	}
	s.Append(e)
	if _, _, err := ev.writeAs(s, render.PrintSink, len(s.Entries)-1); err != nil {
		t.Fatal(err)
	}
	out, _ := ev.buildWith("-e -m", os.DevNull)
	escLines, _ := splitEscape(out)
	var viaPrinter string
	for _, l := range escLines {
		if strings.Contains(l, "gluon-in-0.go") {
			viaPrinter += l + "\n"
		}
	}
	if !strings.Contains(viaPrinter, "escapes to heap") {
		t.Errorf("through the printer, got %q — the control proves nothing "+
			"unless the printer does force an escape", viaPrinter)
	}
}

// -m output must never be mistaken for build errors: explain runs diagRe over
// the whole buffer, and every escape line matches it.
func TestSplitEscapeSeparatesDiagnostics(t *testing.T) {
	out := strings.Join([]string{
		"# github.com/sandboxws/gluon/session",
		"gluonrt.go:72:19: leaking param content: vs",
		"gluon-in-0.go:1:1: make([]byte, 32) does not escape",
		"gluon-in-0.go:1:6: can inline something",
		"gluon-in-1.go:1:1: undefined: bogus",
	}, "\n")
	esc, rest := splitEscape(out)
	if len(esc) != 2 {
		t.Errorf("got %d escape lines, want 2: %v", len(esc), esc)
	}
	if !strings.Contains(rest, "undefined: bogus") {
		t.Errorf("the real error was swallowed: %q", rest)
	}
	if strings.Contains(rest, "does not escape") {
		t.Errorf("an escape message leaked into the errors: %q", rest)
	}
}
