// Package docgen renders gluon's documentation site.
//
// site/ is what is written; docs/ is what GitHub Pages serves, from the main
// branch's /docs directory, with no workflow. A page is an html/template in
// site/, and every page is rendered against one Model: the site's own facts
// from site/pages.toml and — as the reference pages arrive — the command,
// plugin, settings, CLI and MCP registries, read out of the code that defines
// them rather than typed a second time.
//
// Nothing here is linked into the gluon binary. cmd/gluon's TestDocs builds
// the Model and calls Build, so generating the site and checking that the
// committed one is current are the same code path: `just docs` writes, and
// `just test` fails while docs/ says something the code no longer does.
//
// The template delimiters are {% and %}, because the pages quote Go templates
// literally — a TOML plugin's rewrite is `{{.Arg}}.ToSQL()` — and html/template
// would read those as actions.
package docgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Site is the site's own facts, from pages.toml.
type Site struct {
	// BaseURL is where the site is served, with its trailing slash. Only the
	// tags a crawler reads — og:url, og:image — are absolute; every link
	// between pages is relative, so the site reads the same served from
	// anywhere, and a custom domain later is one line here.
	BaseURL string `toml:"base_url"`
	Repo    string `toml:"repo"`
	Release string `toml:"release"`
	// Public says the repository can be linked to and installed from. Until
	// it is, the install line and the source link are not rendered at all,
	// rather than rendered and hidden.
	Public bool `toml:"public"`
	// Go is the oldest Go that builds gluon — its go.mod's go directive, to
	// the minor — read rather than typed, so a page cannot quote an old one.
	Go string `toml:"-"`
}

// Page is one page of the site.
type Page struct {
	// Path is where the page is served, under docs/.
	Path string `toml:"path"`
	// Source is the template it is rendered from, under site/.
	Source  string `toml:"source"`
	Title   string `toml:"title"`
	Lead    string `toml:"lead"`
	Eyebrow string `toml:"eyebrow"`
	// Section groups guide and reference pages for their navigation.
	Section string `toml:"section"`
	// Layout says the source is a page body, wrapped in site/layout.html.
	// The landing page is its own document and is not.
	Layout bool `toml:"layout"`
}

// Model is everything a page may read.
type Model struct {
	Site  Site
	Pages []Page
	// Reg is the registries, read: what the reference pages range over, and
	// what a guide's helpers check a name against.
	Reg Registry
	// Track notes which commands the guides work through. Build makes one if
	// it is nil; a caller that wants to read it afterwards passes its own.
	Track *Tracker
	// Shots is every screenshot's text, by image, from tapes/*.shot.
	Shots map[string]string
}

// pagesFile is pages.toml's shape.
type pagesFile struct {
	Site  Site   `toml:"site"`
	Pages []Page `toml:"page"`
}

// Load reads site/pages.toml under root. Pages come from that list and never
// from a glob, so a file in site/ nobody listed — the maintainer's own notes —
// is never published.
func Load(root string) (Model, error) {
	var f pagesFile
	md, err := toml.DecodeFile(filepath.Join(root, "site", "pages.toml"), &f)
	if err != nil {
		return Model{}, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		return Model{}, fmt.Errorf("site/pages.toml: unknown key %s", und[0])
	}
	if f.Site.BaseURL == "" || !strings.HasSuffix(f.Site.BaseURL, "/") {
		return Model{}, fmt.Errorf("site/pages.toml: base_url must be set and end in a slash")
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return Model{}, err
	}
	m := goDirective.FindSubmatch(mod)
	if m == nil {
		return Model{}, fmt.Errorf("go.mod has no go directive")
	}
	f.Site.Go = string(m[1])
	shots, err := loadShotText(root)
	if err != nil {
		return Model{}, err
	}
	return Model{Site: f.Site, Pages: f.Pages, Shots: shots}, nil
}

// goDirective is go.mod's go line, to the minor version.
var goDirective = regexp.MustCompile(`(?m)^go (\d+\.\d+)`)

// marker is what makes a file under docs/ recognisably generated, so a stale
// one can be removed and a hand edit to one is caught.
const marker = "<!-- generated from site/ by `just docs`: edit the source, not this file -->"

// Generated reports whether a file under docs/ is one Build writes.
func Generated(content []byte) bool { return bytes.Contains(content, []byte(marker)) }

// Build renders every page and returns the files it produced, keyed by their
// path under root.
func Build(m Model, root string) (map[string][]byte, error) {
	if m.Track == nil {
		m.Track = NewTracker()
	}
	out := map[string][]byte{
		// Pages serves the directory as it is: no Jekyll pass to rewrite or
		// drop files whose names begin with an underscore.
		"docs/.nojekyll": {},
	}
	for _, p := range m.Pages {
		b, err := render(m, p, root)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Source, err)
		}
		out[path.Join("docs", p.Path)] = b
	}
	readme, err := spliceReadme(m, root)
	if err != nil {
		return nil, err
	}
	out["README.md"] = readme
	return out, nil
}

// spliceReadme puts what is generated into README.md between its markers, and
// leaves every other byte of the file as it was: the command list between
// the gluon:help markers, a recorded session under each
// <!-- gluon:session NAME --> up to the <!-- gluon:end --> after it, a
// screenshot under each <!-- gluon:shot NAME -->, and the guides under
// <!-- gluon:guides -->.
func spliceReadme(m Model, root string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return nil, err
	}
	const start, end = "<!-- gluon:help:start -->", "<!-- gluon:help:end -->"
	i, j := bytes.Index(b, []byte(start)), bytes.Index(b, []byte(end))
	if i < 0 || j < i {
		return nil, fmt.Errorf("README.md: the %s … %s markers are missing", start, end)
	}
	block := start + "\n" +
		"<!-- generated by `just docs` from the command registry: edit the registry, not this -->\n" +
		"```\n" + m.Reg.HelpText + "\n```\n"
	b = append(append(append([]byte{}, b[:i]...), block...), b[j:]...)

	var failed error
	b = readmeBlock.ReplaceAllFunc(b, func(match []byte) []byte {
		sub := readmeBlock.FindSubmatch(match)
		what, name := string(sub[1]), string(sub[2])
		var body string
		switch what {
		case "session":
			out, err := os.ReadFile(filepath.Join(root, "site", "sessions", name+".out"))
			if err != nil {
				failed = fmt.Errorf("README.md: session %q has not been recorded: %w", name, err)
				return match
			}
			body = "```\n" + PlainTranscript(string(out)) + "```\n"
		case "shot":
			// The default palette's image, since a README cannot switch, at
			// the size the page shows it.
			sh, err := m.shot(root, name)
			if err != nil {
				failed = fmt.Errorf("README.md: %w", err)
				return match
			}
			src, _, _ := strings.Cut(sh.Src["go"], "?")
			body = fmt.Sprintf("<img src=\"docs/%s\" width=\"%d\" height=\"%d\" alt=\"%s\">\n", src, sh.Width, sh.Height, html.EscapeString(sh.Alt))
		case "guides":
			var lb strings.Builder
			for _, p := range m.Pages {
				if p.Section == "Guides" && p.Path != "guide/index.html" {
					fmt.Fprintf(&lb, "- **[%s](%s%s)** — %s\n", p.Title, m.Site.BaseURL, p.Path, p.Lead)
				}
			}
			body = lb.String()
		}
		open := "<!-- gluon:" + what
		if name != "" {
			open += " " + name
		}
		return []byte(open + " -->\n" + body + "<!-- gluon:end -->")
	})
	if failed != nil {
		return nil, failed
	}
	return b, nil
}

// readmeBlock is one generated block of the README: its opening marker, what
// it holds, and everything up to the end marker, which is replaced.
var readmeBlock = regexp.MustCompile(`(?s)<!-- gluon:(session|shot|guides)(?: ([a-z0-9-]+))? -->\n.*?<!-- gluon:end -->`)

// render executes one page's template.
func render(m Model, p Page, root string) ([]byte, error) {
	files := []string{filepath.Join(root, "site", p.Source)}
	if p.Layout {
		files = append([]string{
			filepath.Join(root, "site", "layout.html"),
			filepath.Join(root, "site", "partials.html"),
		}, files...)
	}
	t := template.New(filepath.Base(files[0])).Delims("{%", "%}").Funcs(funcs(m, p, root))
	t, err := t.ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(files[0])
	if p.Layout {
		name = "layout"
	}
	data := pageData{Site: m.Site, Page: p, Model: m, Reg: m.Reg}
	for _, q := range m.Pages {
		if q.Section == "" || q.Section != p.Section {
			continue
		}
		data.Section = append(data.Section, q)
		if q.Path == p.Path {
			data.Here = len(data.Section) - 1
		}
	}
	if n := len(data.Section); n > 0 {
		if data.Here > 0 {
			prev := data.Section[data.Here-1]
			data.Prev = &prev
		}
		if data.Here < n-1 {
			next := data.Section[data.Here+1]
			data.Next = &next
		}
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		return nil, err
	}
	return mark(b.Bytes()), nil
}

// pageData is what a template's dot is.
type pageData struct {
	Site  Site
	Page  Page
	Model Model
	Reg   Registry
	// Section is the pages beside this one, in order; Here is this one's place
	// among them, and Prev and Next its neighbours.
	Section    []Page
	Here       int
	Prev, Next *Page
}

// mark puts the generated marker after the doctype — a comment before it is
// allowed, and one after it is where a reader opening the file looks first.
func mark(b []byte) []byte {
	if i := bytes.IndexByte(b, '\n'); i >= 0 && bytes.HasPrefix(bytes.ToLower(b), []byte("<!doctype")) {
		return append(append(append([]byte{}, b[:i+1]...), []byte(marker+"\n")...), b[i+1:]...)
	}
	return append([]byte(marker+"\n"), b...)
}

// funcs are the helpers a page's template may call.
func funcs(m Model, p Page, root string) template.FuncMap {
	return template.FuncMap{
		// rel is a link from this page to another path under docs/.
		"rel": func(target string) string { return rel(p.Path, target) },
		// asset links a file under docs/ with its content hash, so a browser
		// holding an old stylesheet fetches the new one the day it changes.
		"asset": func(target string) (string, error) {
			b, err := os.ReadFile(filepath.Join(root, "docs", target))
			if err != nil {
				return "", err
			}
			sum := sha256.Sum256(b)
			return rel(p.Path, target) + "?v=" + hex.EncodeToString(sum[:4]), nil
		},
		// abs is an absolute URL, for the tags a crawler reads.
		"abs": func(target string) string { return m.Site.BaseURL + strings.TrimPrefix(target, "/") },
		// anchor is where a command's entry is on the commands page.
		"anchor": func(name string) string { return slug(strings.TrimPrefix(name, ":")) },
		// here says a page in the section is this one.
		"here": func(q Page) bool { return q.Path == p.Path },
		// session is a recorded transcript, from site/sessions/<name>.out,
		// with a caption over it when one is given.
		"session": func(name string, caption ...string) (template.HTML, error) {
			return m.Track.session(root, name, p, caption...)
		},
		// cmd links a command's entry on the commands page, or one of its
		// flags' rows. A name the registry does not have fails the build, so
		// a guide cannot link a command that was renamed or a flag that went.
		"cmd": func(name string, flag ...string) (template.HTML, error) {
			d, ok := m.Reg.command(name)
			if !ok {
				return "", fmt.Errorf("cmd %q: there is no such command", name)
			}
			anchor, text := d.Anchor, name
			for _, f := range flag {
				r, ok := d.flag(f)
				if !ok {
					return "", fmt.Errorf("cmd %q %q: %s has no flag %s", name, f, d.Name, f)
				}
				anchor, text = r.Anchor, name+" "+f
			}
			return link(rel(p.Path, "reference/commands.html")+"#"+anchor, text), nil
		},
		// setting links a key's entry on the settings page.
		"setting": func(key string) (template.HTML, error) {
			for _, s := range m.Reg.Settings {
				if s.Key == key {
					return link(rel(p.Path, "reference/settings.html")+"#"+s.Anchor, key), nil
				}
			}
			return "", fmt.Errorf("setting %q: there is no such setting", key)
		},
		// plugin links a plugin's entry on the plugins page.
		"plugin": func(name string) (template.HTML, error) {
			for _, d := range m.Reg.Plugins {
				if d.Name == name {
					return link(rel(p.Path, "reference/plugins.html")+"#"+d.Anchor, name), nil
				}
			}
			return "", fmt.Errorf("plugin %q: there is no such plugin", name)
		},
		// commandsOn is how many commands a guide page works through.
		"commandsOn": func(path string) int {
			n := 0
			for _, pages := range m.Track.Worked {
				for _, q := range pages {
					if q == path {
						n++
					}
				}
			}
			return n
		},
		// worked is the guide pages that work a command through, for the
		// commands page's links back to them. The guides are rendered first,
		// so by the time a reference page asks, every guide has been seen.
		"worked": func(name string) []Page {
			var out []Page
			for _, path := range m.Track.Worked[name] {
				for _, q := range m.Pages {
					if q.Path == path {
						out = append(out, q)
					}
				}
			}
			return out
		},
		// shot is a screenshot in both palettes, under a caption when one is
		// given. One that has not been taken fails the build.
		"shot": func(id string, caption ...string) (template.HTML, error) {
			return m.figure(root, p, id, caption...)
		},
		// og is the card a link to the site unfurls into, as the absolute URL
		// a crawler needs.
		"og": func() (string, error) {
			if _, err := os.Stat(filepath.Join(root, "docs", "img", "og.png")); err != nil {
				return "", fmt.Errorf("docs/img/og.png is missing — `just promo` composes it: %w", err)
			}
			return m.Site.BaseURL + "img/og.png", nil
		},
		// hero is a recorded transcript as the landing page's terminal plays
		// it, line by line.
		"hero": func(name string) (template.HTML, error) { return m.Track.hero(root, name) },
		// wrap lays words out in lines no wider than width, for a list of
		// names drawn as a terminal would print it.
		"wrap": wrapWords,
		// go is a Go snippet, painted the way gluon paints it.
		"go": Go,
	}
}

// wrapWords joins words two spaces apart into lines of at most width runes.
func wrapWords(words []string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range words {
		switch {
		case cur == "":
			cur = w
		case len([]rune(cur))+2+len([]rune(w)) > width:
			lines = append(lines, cur)
			cur = w
		default:
			cur += "  " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// link is a link whose text is code: a command, a setting, a plugin.
func link(href, text string) template.HTML {
	return template.HTML(`<a href="` + html.EscapeString(href) + `"><code>` + html.EscapeString(text) + `</code></a>`)
}

// rel is the relative path from the page at from to target, both under docs/.
func rel(from, target string) string {
	depth := strings.Count(from, "/")
	return strings.Repeat("../", depth) + target
}
