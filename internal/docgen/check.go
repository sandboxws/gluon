package docgen

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The checks TestDocs runs over everything Build renders. They are here rather
// than left to a browser because a page that is wrong in a way no browser
// complains about — a duplicated id a link lands on the first of, a fragment
// that names nothing — is exactly the page nobody notices is wrong.

// void elements never close.
var void = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true,
}

// page is one rendered HTML file, walked once for both checks.
type page struct {
	ids   map[string]int
	hrefs []string
	errs  []string
}

// walk reads an HTML page with the standard library's forgiving XML decoder
// and its own element stack: every element closed and in order, every id
// once, every image with its text and its size.
func walk(b []byte) page {
	p := page{ids: map[string]int{}}
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	var stack []string
	skip := "" // inside <script> or <style>, whose text is not markup
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			p.errs = append(p.errs, err.Error())
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if skip != "" {
				continue
			}
			attrs := map[string]string{}
			for _, a := range t.Attr {
				attrs[strings.ToLower(a.Name.Local)] = a.Value
			}
			if id, ok := attrs["id"]; ok {
				p.ids[id]++
			}
			if href, ok := attrs["href"]; ok && (name == "a" || name == "link") {
				p.hrefs = append(p.hrefs, href)
			}
			if src, ok := attrs["src"]; ok {
				p.hrefs = append(p.hrefs, src)
			}
			if name == "img" {
				for _, need := range []string{"alt", "width", "height"} {
					if _, ok := attrs[need]; !ok {
						p.errs = append(p.errs, fmt.Sprintf("<img src=%q> has no %s", attrs["src"], need))
					}
				}
			}
			if name == "script" || name == "style" {
				skip = name
			}
			if !void[name] {
				stack = append(stack, name)
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if skip != "" && name != skip {
				continue
			}
			skip = ""
			if void[name] {
				continue
			}
			if len(stack) == 0 || stack[len(stack)-1] != name {
				open := "nothing"
				if len(stack) > 0 {
					open = "<" + stack[len(stack)-1] + ">"
				}
				p.errs = append(p.errs, fmt.Sprintf("</%s> closes %s", name, open))
				continue
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		p.errs = append(p.errs, "unclosed <"+strings.Join(stack, "> <")+">")
	}
	for id, n := range p.ids {
		if n > 1 {
			p.errs = append(p.errs, fmt.Sprintf("id %q is used %d times", id, n))
		}
	}
	sort.Strings(p.errs)
	return p
}

// Check lints every rendered page and follows every link between them. files
// is what Build returned; root is where the hand-written files under docs/ —
// the stylesheet, the script, the images — are looked for.
func Check(files map[string][]byte, root string) []string {
	pages := map[string]page{}
	for name, b := range files {
		if strings.HasSuffix(name, ".html") {
			pages[name] = walk(b)
		}
	}
	var errs []string
	names := make([]string, 0, len(pages))
	for n := range pages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		p := pages[name]
		for _, e := range p.errs {
			errs = append(errs, name+": "+e)
		}
		for _, href := range p.hrefs {
			if e := follow(name, href, pages, root); e != "" {
				errs = append(errs, name+": "+e)
			}
		}
	}
	return errs
}

// follow checks one link from the page at from: a relative path must name a
// file, and a fragment must name an id on the page it points into. Links off
// the site, and the absolute ones a crawler reads, are not this check's.
func follow(from, href string, pages map[string]page, root string) string {
	u, err := url.Parse(href)
	if err != nil {
		return fmt.Sprintf("%q does not parse: %v", href, err)
	}
	if u.Scheme != "" || u.Host != "" || strings.HasPrefix(href, "//") {
		return ""
	}
	target := from
	if u.Path != "" {
		target = path.Clean(path.Join(path.Dir(from), u.Path))
		if strings.HasSuffix(u.Path, "/") {
			target = path.Join(target, "index.html")
		}
	}
	p, isPage := pages[target]
	if !isPage {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(target))); err != nil {
			return fmt.Sprintf("%q names %s, which does not exist", href, target)
		}
	}
	if u.Fragment != "" && isPage && p.ids[u.Fragment] == 0 {
		return fmt.Sprintf("%q names #%s, which %s does not have", href, u.Fragment, target)
	}
	return ""
}

// CheckReadme follows every link in README.md that points into the site: the
// README is read on GitHub, so its links are absolute, and an absolute link
// is one Check does not follow. Each must name a page Build rendered, and a
// fragment an id on it.
func CheckReadme(readme []byte, baseURL string, files map[string][]byte) []string {
	pages := map[string]page{}
	for name, b := range files {
		if strings.HasSuffix(name, ".html") {
			pages[name] = walk(b)
		}
	}
	var errs []string
	for _, m := range siteLink.FindAllSubmatch(readme, -1) {
		href := string(m[1])
		if !strings.HasPrefix(href, baseURL) {
			continue
		}
		rest := strings.TrimPrefix(href, baseURL)
		target, frag, _ := strings.Cut(rest, "#")
		if target == "" || strings.HasSuffix(target, "/") {
			target += "index.html"
		}
		p, ok := pages[path.Join("docs", target)]
		switch {
		case !ok:
			errs = append(errs, fmt.Sprintf("README.md: %s names docs/%s, which is not a page the site has", href, target))
		case frag != "" && p.ids[frag] == 0:
			errs = append(errs, fmt.Sprintf("README.md: %s names #%s, which docs/%s does not have", href, frag, target))
		}
	}
	return errs
}

// siteLink is a Markdown link's target.
var siteLink = regexp.MustCompile(`\]\((https?://[^)\s]+)\)`)
