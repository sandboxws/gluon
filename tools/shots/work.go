package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// held is the workspace's lock, open for as long as the process runs.
var held *os.File

// A Workspace is where a run keeps what it builds and what each window reads:
// the public gluon binary, and per window a home for gluon's own state, so a
// shot never reads or writes the person's config, scratchpads or history.
type Workspace struct {
	Root, Dir, Bin, Release string
}

// prepare builds gluon at the release the site names, into the workspace,
// which it holds for the rest of the process: two runs would share a home, a
// fixture and the fixture's ports.
func prepare(root string) (*Workspace, error) {
	dir := filepath.Join(os.TempDir(), "gluon-shots")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another shots run holds %s — let it finish", dir)
	}
	held = lock // released when the process exits
	release := "dev"
	if b, err := os.ReadFile(filepath.Join(root, "site", "pages.toml")); err == nil {
		if m := regexp.MustCompile(`(?m)^release\s*=\s*"([^"]+)"`).FindSubmatch(b); m != nil {
			release = string(m[1])
		}
	}
	ws := &Workspace{Root: root, Dir: dir, Bin: filepath.Join(dir, "bin", "gluon"), Release: release}
	if dryRun {
		fmt.Printf("go build -o %s ./cmd/gluon\n", ws.Bin)
		return ws, nil
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version="+release, "-o", ws.Bin, "./cmd/gluon")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("building gluon: %v\n%s", err, out)
	}
	return ws, nil
}

// home makes a fresh gluon home for one window and returns the environment
// that points gluon at it. config is the config.toml it starts with.
func (ws *Workspace) home(name, config string) ([]string, error) {
	h := filepath.Join(ws.Dir, "homes", name+"-"+time.Now().Format("150405.000"))
	env := []string{}
	for v, sub := range map[string]string{"XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state"} {
		d := filepath.Join(h, sub)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
		env = append(env, v+"="+d)
	}
	if err := os.MkdirAll(filepath.Join(h, "config", "gluon"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(h, "config", "gluon", "config.toml"), []byte(config), 0o644); err != nil {
		return nil, err
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		return nil, err
	}
	gobin, _ = filepath.EvalSymlinks(gobin)
	path := strings.Join([]string{filepath.Dir(ws.Bin), filepath.Dir(gobin), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
	return append(env, "PATH="+path), nil
}
