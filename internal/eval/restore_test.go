package eval

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreLeavesTheProxyOff is constraint G held mechanically. Get is the
// one method that relaxes GOPROXY; a restore that did the same would make
// "opening a scratchpad does not go online" a promise rather than a property.
func TestRestoreLeavesTheProxyOff(t *testing.T) {
	e := &Evaluator{dir: t.TempDir(), attached: writeSumHost(t, "")}
	if err := e.writeMod(); err != nil {
		t.Fatal(err)
	}

	if err := e.Restore([]string{"example.com/dep v1.2.3"},
		[]byte("example.com/dep v1.2.3 h1:abc=\nexample.com/dep v1.2.3/go.mod h1:def=\n")); err != nil {
		t.Fatal(err)
	}

	mod, err := os.ReadFile(filepath.Join(e.dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "example.com/dep v1.2.3") {
		t.Errorf("the recorded requirement is not in go.mod:\n%s", mod)
	}
	sum, err := os.ReadFile(filepath.Join(e.dir, "go.sum"))
	if err != nil {
		t.Fatalf("no go.sum after a restore that recorded one: %v", err)
	}
	if !strings.Contains(string(sum), "h1:abc=") {
		t.Errorf("the recorded sums are not there:\n%s", sum)
	}

	// The environment the next build runs under is the hermetic one, and the
	// only GOPROXY in it is off. Nothing about restoring may add a second.
	var proxies []string
	for _, kv := range e.env() {
		if strings.HasPrefix(kv, "GOPROXY=") {
			proxies = append(proxies, kv)
		}
	}
	if len(proxies) != 1 || proxies[0] != "GOPROXY=off" {
		t.Errorf("GOPROXY after a restore = %v, want exactly [GOPROXY=off]", proxies)
	}
}

// TestRestoreUnionsTheHostsSums: the attached host's sums are already beside
// the go.mod, and the pad's are added to them rather than replacing them —
// dropping the host's would break a session that builds today.
func TestRestoreUnionsTheHostsSums(t *testing.T) {
	const hostSum = "example.com/hostdep v0.1.0 h1:host=\n"
	e := &Evaluator{dir: t.TempDir(), attached: writeSumHost(t, hostSum)}
	if err := e.writeMod(); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore([]string{"example.com/dep v1.2.3"},
		[]byte("example.com/dep v1.2.3 h1:abc=\n")); err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(e.dir, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"h1:host=", "h1:abc="} {
		if !strings.Contains(string(sum), want) {
			t.Errorf("%s is missing from the merged go.sum:\n%s", want, sum)
		}
	}
}

// TestAChecksumConflictIsRefusedRatherThanMerged: two hashes for one version is
// a real signal, not a merge to resolve.
func TestAChecksumConflictIsRefusedRatherThanMerged(t *testing.T) {
	const hostSum = "example.com/dep v1.2.3 h1:one=\n"
	e := &Evaluator{dir: t.TempDir(), attached: writeSumHost(t, hostSum)}
	if err := e.writeMod(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(e.dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}

	err = e.Restore([]string{"example.com/dep v1.2.3"},
		[]byte("example.com/dep v1.2.3 h1:two=\n"))
	if !errors.Is(err, ErrRestoreConflict) {
		t.Fatalf("Restore with contradicting sums = %v, want ErrRestoreConflict", err)
	}
	if !strings.Contains(err.Error(), "example.com/dep") {
		t.Errorf("the error does not name the module: %v", err)
	}

	// Refused before anything was written: the module is as it was.
	after, err := os.ReadFile(filepath.Join(e.dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("a refused restore still rewrote go.mod")
	}
	sum, err := os.ReadFile(filepath.Join(e.dir, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sum) != hostSum {
		t.Errorf("a refused restore rewrote go.sum:\n%s", sum)
	}
}

// TestRestoreWithNothingRecordedIsANoOp: a standalone session records no
// requirements, and opening it must not invalidate caches it did not change.
func TestRestoreWithNothingRecordedIsANoOp(t *testing.T) {
	e := &Evaluator{dir: t.TempDir(), attached: writeSumHost(t, "")}
	if err := e.writeMod(); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(nil, nil); err != nil {
		t.Fatalf("Restore with nothing recorded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "go.sum")); !os.IsNotExist(err) {
		t.Errorf("a no-op restore wrote a go.sum: %v", err)
	}
}
