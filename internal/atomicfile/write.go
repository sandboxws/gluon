// Package atomicfile writes a file through a temp file in the same directory
// and a rename, so an interrupted run leaves the previous bytes rather than
// half the new ones.
//
// It is its own package rather than a helper in internal/config, whose first
// caller wrote a config, because a writer in any package config imports could
// not reach back into config without a cycle. internal/state's doc records the
// rule this follows: a helper that gets a second caller becomes a package,
// before the second copy is written rather than after.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write replaces path's contents atomically.
//
// Never truncate-then-write: an interrupted run would leave a config that was
// fine as a config that is empty, and the file it is most likely to be pointed
// at is the one holding everything else the user configured.
//
// existed says whether the caller believes the file was already there, which
// is how the mode is chosen: an existing file keeps the permissions it has, and
// a new one is 0600 — what you have configured, typed, or solved is not the
// world's business.
func Write(path string, data []byte, existed bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if existed {
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
	}
	// The pattern names gluon and nothing else: the scratchpad writer is a
	// caller whose file is not TOML, and a temp file claiming to be one would
	// be a lie left behind by an interrupted write.
	tmp, err := os.CreateTemp(dir, ".gluon-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
