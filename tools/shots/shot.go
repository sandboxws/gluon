package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A Shot is one tapes/*.shot file: what the window runs, the keys it is sent,
// and where it is captured — once per palette. The format is a small subset
// of VHS's, plus what a screenshot of a real project needs.
//
//	Shot hero                         the id: file names, and {% shot "hero" %}
//	Alt the startup screen …          the image's text, required
//	Also hero-2 what it shows         a second image the shot takes, and its text
//	Caption …                         a line to post it with
//	Headline …                        the promo images' headline
//	Promo 16:9 4:5 1:1 og             which promo images to compose
//	Varies                            its numbers change between runs
//	Host shop                         run in a fresh copy of testdata/shop
//	Serve                             with its servers started
//	Env KEY=value                     for gluon; $HOST is the project's copy
//	Setup gluon init -write -local    a shell line run first, where gluon starts
//	History :t x                      a line already in the history
//	Args -host .                      gluon's arguments
//	Banner full                       the startup screen, config's banner
//	Config value.form line            a setting, written by gluon's :settings
//	Grid 80x24                        the window's size in cells
//	Line x := 1                       typed, entered, and waited for
//	Type "x := 1"   Paste << … >>     text, typed or pasted
//	Keys "/http"                      text a key at a time, as a view reads it
//	Enter Tab Esc Backspace Up Down Left Right PgUp PgDn   Ctrl r
//	Wait /regex/    Wait Prompt       for the program, after the last input
//	Sleep 500ms
//	Screenshot [image]                capture the window now
//	Relaunch                          quit and start gluon again, same home
type Shot struct {
	ID, File               string
	Alt, Caption, Headline string
	Also                   map[string]string // the other images' ids, and their text
	Promo                  []string
	Varies                 bool
	Host                   string
	Serve                  bool
	Env, Setup, History    []string
	Args                   []string
	Banner                 string
	Config                 []string
	Cols, Rows             int
	Steps                  []Step
}

// Images is every image the shot takes: its own id first.
func (s *Shot) Images() []string {
	out := []string{s.ID}
	var also []string
	for id := range s.Also {
		also = append(also, id)
	}
	sort.Strings(also)
	return append(out, also...)
}

// AltOf is an image's text.
func (s *Shot) AltOf(image string) string {
	if image == s.ID {
		return s.Alt
	}
	return s.Also[image]
}

// A Step is one line of a shot's script.
type Step struct {
	Op, Arg string
	Line    int
}

var ops = map[string]bool{
	"Type": true, "Paste": true, "Enter": true, "Tab": true, "Esc": true, "Backspace": true,
	"Up": true, "Down": true, "Left": true, "Right": true, "PgUp": true, "PgDn": true, "Ctrl": true,
	"Wait": true, "Sleep": true, "Screenshot": true, "Relaunch": true, "Line": true, "Keys": true,
}

var imageID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// loadShots reads every tapes/*.shot, sorted by id.
func loadShots(root string) ([]*Shot, error) {
	files, err := filepath.Glob(filepath.Join(root, "tapes", "*.shot"))
	if err != nil {
		return nil, err
	}
	var out []*Shot
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		s, err := parseShot(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		s.File = f
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// parseShot reads one shot file.
func parseShot(text string) (*Shot, error) {
	s := &Shot{Cols: 80, Rows: 24, Banner: "off", Also: map[string]string{}}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		n := i + 1
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		word, rest, _ := strings.Cut(l, " ")
		rest = strings.TrimSpace(rest)
		switch word {
		case "Shot":
			if !imageID.MatchString(rest) {
				return nil, fmt.Errorf("line %d: a shot's id is lowercase words and dashes", n)
			}
			s.ID = rest
		case "Alt":
			s.Alt = rest
		case "Also":
			id, alt, _ := strings.Cut(rest, " ")
			if !imageID.MatchString(id) || strings.TrimSpace(alt) == "" {
				return nil, fmt.Errorf("line %d: Also takes an image id and its text", n)
			}
			s.Also[id] = strings.TrimSpace(alt)
		case "Env":
			if k, _, ok := strings.Cut(rest, "="); !ok || !envName.MatchString(k) {
				return nil, fmt.Errorf("line %d: Env takes KEY=value", n)
			}
			s.Env = append(s.Env, rest)
		case "Setup":
			s.Setup = append(s.Setup, rest)
		case "History":
			s.History = append(s.History, rest)
		case "Caption":
			s.Caption = rest
		case "Headline":
			s.Headline = rest
		case "Promo":
			s.Promo = strings.Fields(rest)
			for _, p := range s.Promo {
				if _, ok := promoSizes[p]; !ok {
					return nil, fmt.Errorf("line %d: no promo size %q", n, p)
				}
			}
		case "Varies":
			s.Varies = true
		case "Host":
			s.Host = rest
		case "Serve":
			s.Serve = true
		case "Args":
			s.Args = strings.Fields(rest)
		case "Banner":
			s.Banner = rest
		case "Config":
			if k, v, ok := strings.Cut(rest, " "); !ok || k == "" || strings.TrimSpace(v) == "" {
				return nil, fmt.Errorf("line %d: Config takes a setting and its value", n)
			}
			s.Config = append(s.Config, rest)
		case "Grid":
			if _, err := fmt.Sscanf(rest, "%dx%d", &s.Cols, &s.Rows); err != nil {
				return nil, fmt.Errorf("line %d: Grid takes COLSxROWS", n)
			}
		case "Paste":
			if rest != "<<" {
				return nil, fmt.Errorf("line %d: Paste takes a << block", n)
			}
			var block []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != ">>"; i++ {
				block = append(block, lines[i])
			}
			if i >= len(lines) {
				return nil, fmt.Errorf("line %d: the Paste block is never closed", n)
			}
			s.Steps = append(s.Steps, Step{Op: "Paste", Arg: strings.Join(block, "\n") + "\n", Line: n})
		default:
			if !ops[word] {
				return nil, fmt.Errorf("line %d: unknown %q", n, word)
			}
			st := Step{Op: word, Arg: rest, Line: n}
			switch word {
			case "Line":
				if rest == "" {
					return nil, fmt.Errorf("line %d: Line takes what to type", n)
				}
			case "Type", "Keys":
				v, err := unquote(rest)
				if err != nil {
					return nil, fmt.Errorf("line %d: %v", n, err)
				}
				st.Arg = v
			case "Sleep":
				if _, err := time.ParseDuration(rest); err != nil {
					return nil, fmt.Errorf("line %d: %v", n, err)
				}
			case "Ctrl":
				if len(rest) != 1 || rest[0] < 'a' || rest[0] > 'z' {
					return nil, fmt.Errorf("line %d: Ctrl takes one letter", n)
				}
			case "Wait":
				if rest != "Prompt" && !(len(rest) > 2 && strings.HasPrefix(rest, "/") && strings.HasSuffix(rest, "/")) {
					return nil, fmt.Errorf("line %d: Wait takes /regex/ or Prompt", n)
				}
			}
			s.Steps = append(s.Steps, st)
		}
	}
	switch {
	case s.ID == "":
		return nil, fmt.Errorf("no Shot line")
	case s.Alt == "":
		return nil, fmt.Errorf("shot %s has no Alt: every image says what it shows", s.ID)
	}
	taken := map[string]bool{}
	for i, st := range s.Steps {
		if st.Op != "Screenshot" {
			continue
		}
		if st.Arg == "" {
			s.Steps[i].Arg = s.ID
		} else if _, ok := s.Also[st.Arg]; !ok && st.Arg != s.ID {
			return nil, fmt.Errorf("line %d: no image %s — declare it with Also", st.Line, st.Arg)
		}
		taken[s.Steps[i].Arg] = true
	}
	for _, id := range s.Images() {
		if !taken[id] {
			return nil, fmt.Errorf("shot %s never takes image %s", s.ID, id)
		}
	}
	return s, nil
}

// unquote reads a Type argument: a Go string, or a backquoted raw one.
func unquote(s string) (string, error) {
	if strings.HasPrefix(s, "`") {
		if !strings.HasSuffix(s, "`") || len(s) < 2 {
			return "", fmt.Errorf("an unclosed backquote")
		}
		return s[1 : len(s)-1], nil
	}
	return strconv.Unquote(s)
}

// promoSizes are the promo images, as width and height in pixels.
var promoSizes = map[string][2]int{
	"16:9": {1600, 900},
	"4:5":  {1080, 1350},
	"1:1":  {1080, 1080},
	"og":   {1200, 630},
}
