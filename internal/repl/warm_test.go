package repl

import (
	"testing"
	"time"
)

// A fetch that failed added nothing, so there is nothing to warm. Starting the
// goroutine anyway would spend a `go list` on a path the session does not have
// — and, for a typo, on whatever that path happens to name.
func TestAFailedGetWarmsNothing(t *testing.T) {
	c := testCore(t)
	// Malformed rather than merely absent: the toolchain refuses it without
	// reaching the network, so this stays in the fast tier.
	res := c.Submit(":get Not/A/Module")
	if !res.Err {
		t.Fatalf(":get on a malformed module path succeeded: %q", res.Out)
	}
	// The warm-up is a goroutine, so an assertion that it did not run has to
	// give it the time in which it would have.
	time.Sleep(200 * time.Millisecond)
	if pkg := c.ev.Loaded("Not/A/Module"); pkg != nil {
		t.Errorf("a failed :get warmed %v", pkg)
	}
}

// The warmed path is the module's, not the argument's: `:get uuid@v1.6.0`
// requires github.com/google/uuid, and that is the import path a line names.
func TestModulePathDropsTheVersion(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"github.com/google/uuid", "github.com/google/uuid"},
		{"github.com/google/uuid@v1.6.0", "github.com/google/uuid"},
		{"github.com/google/uuid@latest", "github.com/google/uuid"},
		{"", ""},
	} {
		if got := modulePath(tc.in); got != tc.want {
			t.Errorf("modulePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
