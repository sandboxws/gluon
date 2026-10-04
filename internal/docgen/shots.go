package docgen

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A Shot is a screenshot the docs show: one moment in a real Ghostty window,
// captured by tools/shots once in each of the site's palettes, so the page
// can show the one its reader chose. Its words are the shot file's — the Alt
// line in tapes/<shot>.shot that takes it — and its size is the image's own:
// neither is typed again here.
type Shot struct {
	ID, Alt string
	// Width and Height are in CSS pixels: half the image's, which is captured
	// at the display's two pixels to the point.
	Width, Height int
	// Src is each palette's image, under docs/.
	Src map[string]string
}

// shotPalettes are the site's palettes, as the images are named for them.
var shotPalettes = []string{"go", "gruv"}

// loadShotText reads every image's text out of tapes/*.shot: a shot's own
// image is its Shot line's id with its Alt line, and each Also line names
// another image the shot takes, with its text.
func loadShotText(root string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "tapes", "*.shot"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		id := ""
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		for sc.Scan() {
			word, rest, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
			rest = strings.TrimSpace(rest)
			switch word {
			case "Shot":
				id = rest
			case "Alt":
				if id == "" {
					return nil, fmt.Errorf("%s: Alt before Shot", filepath.Base(f))
				}
				out[id] = rest
			case "Also":
				also, alt, _ := strings.Cut(rest, " ")
				out[also] = strings.TrimSpace(alt)
			}
		}
	}
	return out, nil
}

// shot reads one image: its text from the shot files, and both palettes'
// WebPs from docs/img/shots, which must exist and be the same size — the page
// swaps one for the other, and nothing below it may move when it does.
func (m Model) shot(root, id string) (Shot, error) {
	alt, ok := m.Shots[id]
	if !ok {
		return Shot{}, fmt.Errorf("shot %q: no tapes/*.shot takes it", id)
	}
	s := Shot{ID: id, Alt: alt, Src: map[string]string{}}
	for _, p := range shotPalettes {
		rel := "img/shots/" + id + "." + p + ".webp"
		b, err := os.ReadFile(filepath.Join(root, "docs", filepath.FromSlash(rel)))
		if err != nil {
			return Shot{}, fmt.Errorf("shot %q has not been taken in the %s palette — `just shots %s`: %w", id, p, id, err)
		}
		w, h, err := webpSize(b)
		if err != nil {
			return Shot{}, fmt.Errorf("%s: %w", rel, err)
		}
		if s.Width != 0 && (w/2 != s.Width || h/2 != s.Height) {
			return Shot{}, fmt.Errorf("shot %q is %dx%d in one palette and %dx%d in the other — compose them again", id, s.Width*2, s.Height*2, w, h)
		}
		s.Width, s.Height = w/2, h/2
		sum := sha256.Sum256(b)
		s.Src[p] = rel + "?v=" + hex.EncodeToString(sum[:4])
	}
	return s, nil
}

// figure is a shot as a page shows it: both palettes' images, of which the
// stylesheet shows the one the page is in, under a bar that says what it is.
// A hidden image that is loaded lazily is never fetched, so a reader pays for
// one palette.
func (m Model) figure(root string, p Page, id string, caption ...string) (template.HTML, error) {
	s, err := m.shot(root, id)
	if err != nil {
		return "", err
	}
	bar := "a real Ghostty window"
	if len(caption) > 0 && caption[0] != "" {
		bar = caption[0]
	}
	var b strings.Builder
	b.WriteString(`<figure class="shot">`)
	b.WriteString(`<figcaption class="cmd-bar"><span class="ck">Screenshot</span><span class="cv">` + html.EscapeString(bar) + `</span></figcaption>`)
	for _, pal := range shotPalettes {
		// Linked to itself: a page shows a shot at the width of its column,
		// and the image is twice that.
		src := html.EscapeString(rel(p.Path, s.Src[pal]))
		fmt.Fprintf(&b, `<a href="%s"><img class="v-%s" src="%s" width="%d" height="%d" alt="%s" loading="lazy" decoding="async"></a>`,
			src, pal, src, s.Width, s.Height, html.EscapeString(s.Alt))
	}
	b.WriteString(`</figure>`)
	return template.HTML(b.String()), nil
}

// webpSize is a WebP's width and height in pixels, from its header: the
// lossless form tools/shots writes, the extended form, and the lossy one.
func webpSize(b []byte) (int, int, error) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, errors.New("not a WebP")
	}
	switch string(b[12:16]) {
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, errors.New("a VP8L chunk without its signature")
		}
		bits := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, nil
	case "VP8X":
		return 1 + (int(b[24]) | int(b[25])<<8 | int(b[26])<<16), 1 + (int(b[27]) | int(b[28])<<8 | int(b[29])<<16), nil
	case "VP8 ":
		return int(uint16(b[26])|uint16(b[27])<<8) & 0x3fff, int(uint16(b[28])|uint16(b[29])<<8) & 0x3fff, nil
	}
	return 0, 0, fmt.Errorf("a WebP whose first chunk is %q", b[12:16])
}
