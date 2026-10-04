package repl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/ui"
)

// TestShortVersion. A release says its number and a development build does not:
// it says a date, a commit and sometimes "+dirty", forty characters of
// provenance that would be the loudest thing on the screen and none of which is
// what somebody starting a REPL came to read.
func TestShortVersion(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"0.0.0-20260907221914-8053da02ce95+dirty", "dev · 8053da0 · dirty"},
		{"0.0.0-20260907221914-8053da02ce95", "dev · 8053da0"},
		{"1.2.3-0.20260907221914-8053da02ce95", "dev · 8053da0"},
		// Not pseudo-versions. These pass through, because a version somebody
		// stamped on purpose is already the shortest true thing.
		{"0.1.0", "0.1.0"},
		{"dev", "dev"},
		{"", ""},
	} {
		if got := shortVersion(tc.in); got != tc.want {
			t.Errorf("shortVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBannerIsNeverPaintedWhenPlain is invariant 31 at this surface. The screen
// is the first thing in a session's scrollback, so an escape leaking into it
// would be the first thing in every transcript pasted into an issue.
func TestBannerIsNeverPaintedWhenPlain(t *testing.T) {
	f := bootFacts{Version: "0.1.0", Go: "go1.27.0"}
	f.add("scratchpad", "default · 1 entry restored")
	f.add("host", "github.com/acme/inventory-api")
	for _, mode := range []string{bannerFull, bannerCompact} {
		out := renderBanner(ui.Plain(), f, 100, mode)
		if strings.ContainsRune(out, 0x1b) {
			t.Errorf("%s: plain banner carries an escape:\n%q", mode, out)
		}
	}
}

// TestBannerRowsAppearOnlyWhenTheyHaveSomethingToSay. The suppression rule is
// the design: a screen reporting "database  none" every morning would be
// furniture, and the difference between furniture and news is whether the row
// is absent or is present saying nothing.
func TestBannerRowsAppearOnlyWhenTheyHaveSomethingToSay(t *testing.T) {
	var f bootFacts
	f.add("host", "")
	f.add("database", "")
	f.add("plugins", "")
	if len(f.Rows) != 0 {
		t.Fatalf("empty values drew %d rows: %+v", len(f.Rows), f.Rows)
	}
	f.add("scratchpad", "default · empty")
	if len(f.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(f.Rows))
	}

	out := renderBanner(ui.Plain(), f, 100, bannerFull)
	for _, absent := range []string{"host", "database", "plugins", "theme"} {
		if strings.Contains(out, absent) {
			t.Errorf("banner names %q with nothing to say:\n%s", absent, out)
		}
	}
	if !strings.Contains(out, "scratchpad  default · empty") {
		t.Errorf("banner lost the one row it had:\n%s", out)
	}
}

// TestBannerOffIsSilent. off means off: a prompt, and that is it.
func TestBannerOffIsSilent(t *testing.T) {
	f := bootFacts{Version: "0.1.0", Go: "go1.27.0"}
	f.add("scratchpad", "default · empty")
	if out := renderBanner(ui.Plain(), f, 100, bannerOff); out != "" {
		t.Errorf("banner off printed %q", out)
	}
}

// TestTheWordmarkIsRectangular. The text beside the mark starts in one column
// because both rows are the same width, and lipgloss.Width is the measure
// because a Block Elements glyph is one cell where len() would say three.
func TestTheWordmarkIsRectangular(t *testing.T) {
	if a, b := lipgloss.Width(wordmarkRows[0]), lipgloss.Width(wordmarkRows[1]); a != b {
		t.Fatalf("wordmark rows are %d and %d cells wide", a, b)
	}
}

// TestTheFullBannerFitsItsThreshold. bannerMinWidth is the promise that the
// full form does not wrap, and it is a number somebody will break by adding a
// word to the tagline.
func TestTheFullBannerFitsItsThreshold(t *testing.T) {
	f := bootFacts{Version: "0.1.0", Go: "go1.27.0"}
	for _, line := range strings.Split(bannerHead(ui.Plain(), f, 100), "\n") {
		if w := lipgloss.Width(line); w >= bannerMinWidth {
			t.Errorf("masthead line is %d cells, at or over the %d threshold: %q",
				w, bannerMinWidth, line)
		}
	}
}

// TestNarrowTerminalsDropTheWordmark. The mark is a picture and a picture that
// wraps is torn in half; the facts are the half that is about this session, so
// they are the half that is kept.
func TestNarrowTerminalsDropTheWordmark(t *testing.T) {
	f := bootFacts{Version: "0.1.0", Go: "go1.27.0"}
	f.add("scratchpad", "default · empty")
	out := renderBanner(ui.Plain(), f, 40, bannerFull)
	if strings.Contains(out, wordmarkRows[0]) {
		t.Errorf("wordmark survived a 40-column terminal:\n%s", out)
	}
	if !strings.Contains(out, "scratchpad") {
		t.Errorf("narrow banner dropped the facts too:\n%s", out)
	}
	if !strings.Contains(out, "gluon 0.1.0") {
		t.Errorf("narrow banner never names the program:\n%s", out)
	}
}

// TestALongRowWrapsUnderItsOwnColumn. The shop fixture's build list turns on
// fourteen plugins, and at eighty columns their names are wider than the room
// beside the label. Left to the terminal, the row carries on at column 0,
// through the label column. Wrapped here, it carries on under itself, breaks
// only at a space, and leaves a word too long to fit whole.
func TestALongRowWrapsUnderItsOwnColumn(t *testing.T) {
	plugins := "uuid, decimal, sqlx, ent, goose, chi, cobra, do, viper, yaml, yaml.v3, msgpack, protobuf, grpc"
	got := bannerRow(ui.Plain(), "plugins", plugins, 80)
	want := "  plugins     uuid, decimal, sqlx, ent, goose, chi, cobra, do, viper, yaml,\n" +
		"              yaml.v3, msgpack, protobuf, grpc"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	for _, l := range strings.Split(got, "\n") {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("a line is %d cells wide: %q", w, l)
		}
	}

	// A style's escapes take no room, and a module path is never broken.
	host := "github.com/acme/inventory-api"
	got = bannerRow(ui.Plain(), "host", host+" \x1b[2m· attached by the pad\x1b[0m", 40)
	if !strings.Contains(got, host) {
		t.Errorf("the path was broken:\n%s", got)
	}
	if lines := strings.Split(got, "\n"); len(lines) != 2 || lipgloss.Width(lines[1]) != 14+lipgloss.Width("· attached by the pad") {
		t.Errorf("the note should sit whole on its own line, under the value:\n%q", got)
	}
	// Nothing wraps at an unknown width, or where it fits.
	if got := bannerRow(ui.Plain(), "plugins", plugins, 0); strings.Contains(got, "\n") {
		t.Errorf("wrapped at an unknown width:\n%s", got)
	}
	if got := bannerRow(ui.Plain(), "host", host, 80); strings.Contains(got, "\n") {
		t.Errorf("wrapped a row that fits:\n%s", got)
	}
}

// TestAPendingRowIsNotRewrittenOffATerminal. A carriage return into a file is a
// row nothing ever takes back, so off a terminal the question is not asked and
// only the answer prints.
func TestAPendingRowIsNotRewrittenOffATerminal(t *testing.T) {
	defer func(old func() bool) { stdoutIsTerminal = old }(stdoutIsTerminal)

	stdoutIsTerminal = func() bool { return false }
	var buf bytes.Buffer
	pending(&buf, ui.Plain(), "scratchpad", "opening default…", 80)("default · empty")
	if got := buf.String(); got != "  scratchpad  default · empty\n" {
		t.Errorf("off a terminal, pending wrote %q", got)
	}
	if strings.Contains(buf.String(), "\r") {
		t.Error("a carriage return reached a destination that is not a terminal")
	}

	stdoutIsTerminal = func() bool { return true }
	buf.Reset()
	pending(&buf, ui.Plain(), "scratchpad", "opening default…", 80)("default · empty")
	out := buf.String()
	if !strings.Contains(out, "opening default…") {
		t.Errorf("on a terminal, the question was never asked: %q", out)
	}
	if !strings.HasSuffix(out, "  scratchpad  default · empty\n") {
		t.Errorf("on a terminal, the row did not become its answer: %q", out)
	}
	if strings.Contains(out, " \n") {
		t.Errorf("the rewrite left trailing space in scrollback: %q", out)
	}
}

// TestAPendingRowWithNoAnswerIsTakenBack. A question left standing where an
// answer never came is the failure this whole screen is a fix for.
func TestAPendingRowWithNoAnswerIsTakenBack(t *testing.T) {
	defer func(old func() bool) { stdoutIsTerminal = old }(stdoutIsTerminal)
	stdoutIsTerminal = func() bool { return true }

	var buf bytes.Buffer
	pending(&buf, ui.Plain(), "database", "looking…", 80)("")
	if got := buf.String(); !strings.HasSuffix(got, "\r") {
		t.Errorf("an unanswered row was not taken back: %q", got)
	}
}

// TestTheStartupScreenLeavesNoOrphanBlank drives the real driver, because the
// three cases that got this wrong are all about which rows exist rather than
// about how one is drawn — and only writeStartup knows that.
//
// The blank under the fact block belongs to the block. A pad that will not open
// withdraws the row it drew, and a blank left standing under nothing would be
// the same leftover as the progress line this screen exists to stop leaving.
func TestTheStartupScreenLeavesNoOrphanBlank(t *testing.T) {
	defer func(old func() bool) { stdoutIsTerminal = old }(stdoutIsTerminal)
	stdoutIsTerminal = func() bool { return false }
	padRoot(t)

	for _, tc := range []struct {
		name  string
		start Start
		row   bool
	}{
		{"a pad", Start{}, true},
		{"no pad", Start{NoPad: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeStartup(&buf, testCore(t), tc.start); err != nil {
				t.Fatalf("writeStartup: %v", err)
			}
			out := buf.String()
			if strings.Contains(out, "\n\n\n") {
				t.Errorf("the screen has a doubled blank line:\n%q", out)
			}
			if !strings.Contains(out, ":help") {
				t.Errorf("the hints line is missing:\n%s", out)
			}
			if got := strings.Contains(out, "scratchpad"); got != tc.row {
				t.Errorf("scratchpad row present = %v, want %v:\n%s", got, tc.row, out)
			}
			// Every block is separated from the masthead by exactly one blank.
			if !strings.Contains(out, tagline+"\n\n") {
				t.Errorf("no blank line under the masthead:\n%q", out)
			}
		})
	}
}

// TestTheStartupScreenIsSilentWhenTurnedOff. off means off, and it must still
// do the work: the scratchpad is opened either way, because a session that
// skipped its pad to save four lines would have lost what somebody typed
// yesterday.
func TestTheStartupScreenIsSilentWhenTurnedOff(t *testing.T) {
	padRoot(t)
	c := testCore(t)
	c.cfg.Banner = "off"

	var buf bytes.Buffer
	if err := writeStartup(&buf, c, Start{}); err != nil {
		t.Fatalf("writeStartup: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("banner off printed %q", buf.String())
	}
	if c.pad == nil || c.pad.name != "default" {
		t.Errorf("banner off skipped opening the scratchpad: %+v", c.pad)
	}
}
