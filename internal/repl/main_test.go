package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain gives the whole run one Go telemetry directory, switched off.
//
// The go command keeps its telemetry under os.UserConfigDir — on Linux that is
// $XDG_CONFIG_HOME, which many of these tests point at a t.TempDir — and on a
// fresh directory it also starts a detached upload child. A go process still
// writing there when the test ends makes the TempDir cleanup fail with
// "directory not empty". TEST_TELEMETRY_DIR is the go command's own override,
// and a mode of "off" means it writes nothing at all.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gluon-test-telemetry-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("off"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("TEST_TELEMETRY_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
