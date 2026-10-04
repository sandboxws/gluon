// Command shots takes gluon's screenshots from real Ghostty windows.
//
// Every shot is taken in a new window this tool opens — one window, one tab —
// in the Ghostty already running. It never quits Ghostty, never touches a
// window it did not open, and closes the ones it did by letting their
// program exit. See tapes/README.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usage = `usage: shots [-dry-run] <command>

  run [-palette go|gruv] [id...]   take the shots, every one or those named
  compose [id...]                  make the docs' and the promo images from them
  check [id...]                    run each shot's lines through gluon, no window
  preflight                        check the permissions and tools, no window
  list                             the shots, and the images each takes
  spike                            check every primitive in one throwaway window`

func main() {
	flag.BoolVar(&dryRun, "dry-run", false, "print every call instead of making it")
	flag.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	flag.Parse()
	root, err := repoRoot()
	if err != nil {
		fail(err)
	}
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	switch args[0] {
	case "spike":
		err = spike(root)
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		only := fs.String("palette", "", "take only this palette: go or gruv")
		_ = fs.Parse(args[1:])
		pals := palettes
		if *only != "" {
			p, ok := paletteNamed(*only)
			if !ok {
				fail(fmt.Errorf("no palette %s", *only))
			}
			pals = []Palette{p}
		}
		err = runShots(root, fs.Args(), pals)
	case "compose":
		err = compose(root, args[1:])
	case "check":
		err = check(root, args[1:])
	case "preflight":
		err = preflight()
	case "list":
		err = list(root)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fail(err)
	}
}

// list prints every shot and the images it takes.
func list(root string) error {
	shots, err := loadShots(root)
	if err != nil {
		return err
	}
	for _, s := range shots {
		promo := strings.Join(s.Promo, " ")
		fmt.Printf("%-18s %3dx%-3d %-14s %s\n", s.ID, s.Cols, s.Rows, promo, s.Alt)
		for _, id := range s.Images()[1:] {
			fmt.Printf("  %-16s %s\n", id, s.Also[id])
		}
	}
	return nil
}

// repoRoot is the gluon checkout this tool lives in.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "cmd", "gluon", "main.go")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", errors.New("run shots from inside the gluon checkout")
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "shots:", err)
	os.Exit(1)
}
