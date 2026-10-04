package docgen

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// losslessWebP is the header of a lossless WebP of a size — all a page reads.
func losslessWebP(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2f")
	var dims [4]byte
	binary.LittleEndian.PutUint32(dims[:], uint32(w-1)|uint32(h-1)<<14)
	b = append(b, dims[:]...)
	return append(b, make([]byte, 16)...)
}

func TestAWebPSaysItsOwnSize(t *testing.T) {
	for _, wh := range [][2]int{{1448, 936}, {1, 1}, {16384, 2}} {
		w, h, err := webpSize(losslessWebP(wh[0], wh[1]))
		if err != nil || w != wh[0] || h != wh[1] {
			t.Errorf("a %dx%d image read as %dx%d (%v)", wh[0], wh[1], w, h, err)
		}
	}
	if _, _, err := webpSize([]byte("\x89PNG\r\n\x1a\n and more than thirty bytes of it")); err == nil {
		t.Error("a PNG read as a WebP")
	}
}

// shotRoot is a checkout with one shot file, taking two images, and the images
// it names written at the sizes given, by palette.
func shotRoot(t *testing.T, sizes map[string][2]int) Model {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"tapes", filepath.Join("docs", "img", "shots")} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shot := "Shot hero\nAlt the startup screen\nAlso hero-2 the second image\nScreenshot\nScreenshot hero-2\n"
	if err := os.WriteFile(filepath.Join(root, "tapes", "hero.shot"), []byte(shot), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, wh := range sizes {
		if err := os.WriteFile(filepath.Join(root, "docs", "img", "shots", name+".webp"), losslessWebP(wh[0], wh[1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	text, err := loadShotText(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return Model{Shots: text}
}

// TestAShotIsItsShotFilesWordsAndItsImagesSize. Neither is typed in a page:
// the text is the Alt the shot file gives the image, and the size is half
// the image's own, since it was captured at two pixels to the point.
func TestAShotIsItsShotFilesWordsAndItsImagesSize(t *testing.T) {
	m := shotRoot(t, map[string][2]int{"hero.go": {1448, 936}, "hero.gruv": {1448, 936}})
	if m.Shots["hero-2"] != "the second image" {
		t.Errorf("an Also line's text is %q", m.Shots["hero-2"])
	}
	html, err := m.figure(".", Page{Path: "guide/start.html"}, "hero", "in a project")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`alt="the startup screen"`, `width="724" height="468"`,
		`class="v-go" src="../img/shots/hero.go.webp?v=`, `class="v-gruv" src="../img/shots/hero.gruv.webp?v=`,
		`in a project`, `loading="lazy"`,
	} {
		if !strings.Contains(string(html), want) {
			t.Errorf("the figure has no %s:\n%s", want, html)
		}
	}
}

// TestAShotNotTakenFailsTheBuild — in either palette, or with the two at
// different sizes, since the page swaps one for the other in place.
func TestAShotNotTakenFailsTheBuild(t *testing.T) {
	m := shotRoot(t, map[string][2]int{"hero.go": {1448, 936}})
	if _, err := m.figure(".", Page{Path: "index.html"}, "hero"); err == nil || !strings.Contains(err.Error(), "gruv") {
		t.Errorf("a missing palette built: %v", err)
	}
	m = shotRoot(t, map[string][2]int{"hero.go": {1448, 936}, "hero.gruv": {1448, 990}})
	if _, err := m.figure(".", Page{Path: "index.html"}, "hero"); err == nil {
		t.Error("two sizes built")
	}
	if _, err := m.figure(".", Page{Path: "index.html"}, "nobody"); err == nil {
		t.Error("a shot no file takes built")
	}
}
