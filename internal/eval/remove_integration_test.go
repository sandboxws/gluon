//go:build integration

package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

// The removal itself is `go get @none`, so it needs the real toolchain and a
// module in the cache — which is what puts it in this tier rather than beside
// the refusals, which never reach the toolchain at all.
func TestRemoveDropsTheRequirementAndEverythingDerivedFromIt(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	if _, err := e.Get("github.com/google/uuid"); err != nil {
		t.Skip("cannot fetch the test module:", err)
	}
	reqs, err := e.Requires()
	if err != nil {
		t.Fatal(err)
	}
	if !requires(reqs, "github.com/google/uuid") {
		t.Fatalf("after :get, Requires() = %v", reqs)
	}

	// A result in the cache is the one invariant 18 cares most about: it is
	// keyed on program text alone, so the same line after a removal must not
	// be answered from before it.
	s := &session.Session{}
	s.Append(session.Entry{Kind: session.KindExpr, Src: "1 + 1"})
	if _, err := e.Eval(s); err != nil {
		t.Fatal(err)
	}
	if e.CacheLen() == 0 {
		t.Fatal("nothing cached, so the drop cannot be observed")
	}

	changed, err := e.Remove("github.com/google/uuid")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(changed) == 0 || !strings.HasPrefix(changed[0], "removed github.com/google/uuid v") {
		t.Errorf("Remove reported %v, want the requirement it removed with its version", changed)
	}
	if reqs, _ := e.Requires(); requires(reqs, "github.com/google/uuid") {
		t.Errorf("the requirement survived removal: %v", reqs)
	}
	if e.CacheLen() != 0 {
		t.Errorf("result cache holds %d entries after a removal, want 0", e.CacheLen())
	}
	// go.sum is the toolchain's to keep consistent, which is why this is
	// `go get @none` and not a modfile edit. It is not pruned — that is
	// `go mod tidy`, and a tidy is an explicit non-goal — so what is asserted
	// is the thing that matters: the module still builds afterwards.
	if _, err := e.Eval(s); err != nil {
		t.Errorf("the session stopped building after a removal: %v", err)
	}
}

// Removal must not reach the network — :get is the only command that does
// (constraint G). A module that is not in the cache therefore fails rather
// than fetching, and the message has to say which module it could not resolve.
func TestRemoveDoesNotGoOnline(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	// Written by hand: nothing has fetched this, so it is not in the cache.
	if err := os.WriteFile(filepath.Join(e.dir, "go.mod"),
		[]byte("module gluon.local/session\n\ngo 1.25\n\nrequire example.com/absent v1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = e.Remove("example.com/absent")
	if err == nil {
		t.Fatal("removing an unresolvable module succeeded, so something went looking for it")
	}
	if !strings.Contains(err.Error(), "GOPROXY=off") {
		t.Errorf("error does not say the proxy was off: %v", err)
	}
}
