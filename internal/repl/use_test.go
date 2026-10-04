package repl

import (
	"strings"
	"testing"
)

// The index moved off the attach and onto first use. :use is a first use — it
// reports the count and the names — so its output has to be exactly what it
// was, and that is the whole of what the laziness is allowed to change.
func TestUseStillReportsTheCountAndTheNames(t *testing.T) {
	c := testCore(t)
	dir := hostDir(t)

	res := c.Submit(":use " + dir)
	if res.Err {
		t.Fatalf(":use: %s", res.Out)
	}
	for _, want := range []string{
		"attached to example.com/proj",
		"1 importable package(s)",
		": greet",
	} {
		if !strings.Contains(res.Out, want) {
			t.Errorf(":use output is missing %q:\n%s", want, res.Out)
		}
	}
	// node_modules and .git are skipped by the walk, so a report naming them
	// would mean the walk never ran and something else answered.
	for _, never := range []string{"pkg", "hook"} {
		if strings.Contains(res.Out, ": "+never) || strings.Contains(res.Out, ", "+never) {
			t.Errorf(":use offered %q, which the walk skips:\n%s", never, res.Out)
		}
	}
}

// An attached session that names a host package must still resolve it to the
// host's, which is the whole reason the index exists — building it later must
// not mean building it too late.
func TestTheHostIndexStillSettlesAQualifier(t *testing.T) {
	c := testCore(t)
	dir := hostDir(t)
	if _, err := c.Attach(dir); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got := c.completionPackages()["greet"]; got != "example.com/proj/internal/greet" {
		t.Errorf("greet resolves to %q, want the host's package", got)
	}
}

// :use -off drops the host, so the packages it offered must go with it.
func TestDetachingForgetsTheHostsPackages(t *testing.T) {
	c := testCore(t)
	dir := hostDir(t)
	if _, err := c.Attach(dir); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if res := c.Submit(":use -off"); res.Err {
		t.Fatalf(":use -off: %s", res.Out)
	}
	if got := c.completionPackages()["greet"]; got != "" {
		t.Errorf("greet still resolves to %q after detaching", got)
	}
	if ix, err := c.ev.Index(); ix != nil || err != nil {
		t.Errorf("Index() after detaching = (%v, %v), want (nil, nil)", ix, err)
	}
}
