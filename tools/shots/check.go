package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// check runs each shot's lines through gluon with no window: the lines it
// would enter, piped, in the environment its window would have. What each
// printed is shown for a person to read — a shot may mean to fail, and
// :time's does — and every build it causes lands in the cache, so the shots
// that follow are fast.
func check(root string, ids []string) error {
	shots, err := pick(root, ids)
	if err != nil {
		return err
	}
	ws, err := prepare(root)
	if err != nil {
		return err
	}
	b := &Batch{ws: ws}
	if b.goenv, err = goEnv(); err != nil {
		return err
	}
	serve := false
	for _, s := range shots {
		serve = serve || s.Serve
	}
	if err := b.stage(root, serve); err != nil {
		return err
	}
	defer b.unserve()
	for _, s := range shots {
		dir, env, err := b.fresh(s, palettes[0])
		if err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		lines := entered(s)
		cmd := exec.Command(b.ws.Bin, s.Args...)
		cmd.Dir, cmd.Env = dir, env
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		runErr := cmd.Run()
		mark := "ok  "
		if runErr != nil {
			mark = "FAIL"
		}
		fmt.Printf("%s %s — %d lines\n", mark, s.ID, len(lines))
		for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
			fmt.Println("     " + l)
		}
	}
	return nil
}

// entered is what a shot submits: every Line, every pasted block, and what it
// typed before an Enter. Keys typed into a view, and a line left unentered to
// show its hint, are not lines.
func entered(s *Shot) []string {
	var out []string
	typed := ""
	for _, st := range s.Steps {
		switch st.Op {
		case "Line":
			out = append(out, st.Arg)
		case "Paste":
			out = append(out, strings.Split(strings.TrimRight(st.Arg, "\n"), "\n")...)
		case "Type":
			typed += st.Arg
		case "Enter":
			if typed != "" {
				out = append(out, typed)
			}
			typed = ""
		case "Ctrl", "Esc", "Relaunch":
			typed = ""
		}
	}
	return out
}
