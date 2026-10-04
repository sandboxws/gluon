package eval

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// TestPackageDirNeverFetches. Locating a package's sources is a lookup, not a
// download: `go list` under GOPROXY=off cannot reach the network, so a module
// that was never fetched reports as absent rather than being fetched behind a
// command the user asked for documentation from. :get is the only command that
// goes online (constraint G), and this is the test that says so for go list.
func TestPackageDirNeverFetches(t *testing.T) {
	e := &Evaluator{dir: "/tmp/gluon-session-test"}
	cmd := e.listCmd(context.Background(), "{{.Dir}}", "strings")

	for _, want := range []string{"GOPROXY=off", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local"} {
		if !slices.Contains(cmd.Env, want) {
			t.Errorf("go list would run without %s", want)
		}
	}
	if cmd.Dir != e.dir {
		t.Errorf("go list would run in %q, not the session's module %q", cmd.Dir, e.dir)
	}
	// -- because a package path is data: without it a path beginning with a
	// dash would be read as a flag.
	if got := strings.Join(cmd.Args, " "); !strings.HasSuffix(got, "list -f {{.Dir}} -- strings") {
		t.Errorf("go list args are %q", got)
	}
}
