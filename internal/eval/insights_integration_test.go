//go:build integration

// These tests actually invoke the Go toolchain. Run with:
//
//	go test -tags=integration ./internal/eval/
package eval

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

// TestVetIsCleanOnGluonsOwnCode is why the drop in mapMessages is a safety net
// rather than a working filter.
//
// Vet runs over the whole main package, which is gluon's embedded runtime and
// its generated package header as well as the user's entries. If either of
// those had a finding, every :vet on every session would carry a diagnostic
// about code the user did not write and cannot change — and the filter would
// be hiding a real bug of gluon's rather than guarding against one.
func TestVetIsCleanOnGluonsOwnCode(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	ev, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ev.Close() })

	// A session with one harmless line, so the program is the runtime, the
	// header and nothing else worth objecting to.
	entry, err := session.Classify("1 + 1")
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{}
	if _, err := ev.Vet(s, entry); err != nil {
		t.Fatalf("Vet: %v", err)
	}

	// Read what vet actually said, before the filter: the requirement is that
	// there is nothing to filter.
	out, _ := ev.vet()
	for _, line := range strings.Split(out, "\n") {
		m := diagRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		t.Errorf("vet objects to gluon's own generated program: %s", strings.TrimSpace(line))
	}
}
