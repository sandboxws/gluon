package release

import (
	"regexp"
	"strings"
)

// parseGodebug reads the GODEBUG History out of $GOROOT/doc/godebug.md.
//
// The file's shape, verified against a 1.27 distribution: an "## GODEBUG
// History" heading, then one "### Go 1.N" subsection per release, each holding
// paragraphs separated by blank lines. One paragraph is one behaviour change.
//
// Only the history section is read. The two sections above it describe what
// GODEBUG is and what the current defaults are, which is documentation about
// the mechanism rather than about any release, and folding them into a release
// would attribute them to whichever heading happened to come next.
//
// The paragraphs are returned as they were written. gluon links no markdown
// renderer — :guide prints its cheatsheets raw for the same reason — so a
// half-hearted one here would be a new dependency to make prose slightly
// prettier, which is not a trade this package gets to make.
func parseGodebug(text string) map[string][]Behaviour {
	out := map[string][]Behaviour{}
	if text == "" {
		return out
	}
	history := historySection(text)
	if history == "" {
		return out
	}
	version := ""
	var para []string
	flush := func() {
		defer func() { para = nil }()
		body := strings.TrimSpace(strings.Join(para, "\n"))
		if version == "" || body == "" {
			return
		}
		out[version] = append(out[version], Behaviour{Setting: setting(body), Text: body})
	}
	for _, line := range strings.Split(history, "\n") {
		if v, ok := releaseHeading(line); ok {
			flush()
			version = v
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		para = append(para, line)
	}
	flush()
	return out
}

// historySection is everything below the "## GODEBUG History" heading.
func historySection(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") && strings.Contains(line, "GODEBUG History") {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return ""
}

// headingRe matches "### Go 1.24", with or without a {#anchor}.
var headingRe = regexp.MustCompile(`^###\s+Go\s+(\d+\.\d+)\s*(\{#[^}]*\})?\s*$`)

func releaseHeading(line string) (string, bool) {
	m := headingRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// settingRe is a GODEBUG key as the prose spells it: `asynctimerchan=1` or
// plain `fips140`, in backticks.
var settingRe = regexp.MustCompile("`([a-z0-9]+)(?:=[^`]*)?`")

// setting is the GODEBUG key a paragraph is about, when it names one.
//
// The key is taken from the first backticked lower-case word, because the
// paragraphs lead with the change and name the setting inside it — "Go 1.24
// added a new `fips140` setting that controls…". A paragraph that names none is
// still a behaviour change worth reading, so this returns "" rather than
// refusing the entry.
func setting(body string) string {
	m := settingRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}
