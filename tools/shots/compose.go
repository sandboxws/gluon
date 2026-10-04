package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// What a composed image is made of, at the capture's scale: a Retina capture
// is two pixels to the point, so the terminal's 20pt padding is 40 pixels.
const (
	inset     = 40 // the padding kept around what the terminal drew
	barHeight = 56 // a drawn window's titlebar
	radius    = 20 // its corners
)

// Fonts for the words a promo image carries. Helvetica Neue ships with macOS;
// Geist is the site's, and is used instead when it is installed. They are
// looked up once, when the first image is composed.
var headFont, monoFont, barFont string

func fonts() {
	if headFont != "" {
		return
	}
	headFont = firstFont("Geist-Bold", "Helvetica-Neue-Bold")
	monoFont = firstFont("Geist-Mono-Medium", "Menlo-Regular")
	barFont = firstFont("Geist-Medium", "Helvetica-Neue-Medium")
}

// compose makes every image the shots named have captured: the docs' WebPs,
// the promo PNGs, and the og card.
func compose(root string, ids []string) error {
	fonts()
	shots, err := pick(root, ids)
	if err != nil {
		return err
	}
	raw := filepath.Join(root, "dist", "shots", "raw")
	docs := filepath.Join(root, "docs", "img", "shots")
	promo := filepath.Join(root, "dist", "promo")
	for _, d := range []string{docs, promo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, s := range shots {
		for _, id := range s.Images() {
			pair := map[string]*image.NRGBA{}
			for _, p := range palettes {
				c, err := content(filepath.Join(raw, id+"."+p.Name+".png"), p)
				if err != nil {
					return fmt.Errorf("%s.%s: %w", id, p.Name, err)
				}
				pair[p.Name] = c
			}
			evenUp(pair)
			for _, p := range palettes {
				if err := webp(pair[p.Name], filepath.Join(docs, id+"."+p.Name+".webp")); err != nil {
					return fmt.Errorf("%s.%s: %w", id, p.Name, err)
				}
			}
			if id != s.ID {
				continue
			}
			for _, p := range palettes {
				win, err := window(ink(pair[p.Name], p), p)
				if err != nil {
					return fmt.Errorf("%s.%s: %w", id, p.Name, err)
				}
				for _, size := range s.Promo {
					wh := promoSizes[size]
					out := filepath.Join(promo, p.Name, fmt.Sprintf("%dx%d", wh[0], wh[1]), id+".png")
					if err := poster(win, s.Headline, p, wh[0], wh[1], out); err != nil {
						return fmt.Errorf("%s.%s %s: %w", id, p.Name, size, err)
					}
					if size == "og" && p.Name == "go" {
						if err := copyFile(out, filepath.Join(root, "docs", "img", "og.png")); err != nil {
							return err
						}
					}
				}
			}
		}
		fmt.Printf("ok   %s\n", s.ID)
	}
	all, err := loadShots(root)
	if err != nil {
		return err
	}
	return contactSheet(promo, all)
}

// content is what the terminal drew in one capture: the window below its
// titlebar, cut to the rows that hold anything and padded as evenly as the
// terminal pads, on the palette's background — the window's rounded corners
// and all. It keeps the terminal's whole width, so every shot on a page is
// as wide as the window it came from.
func content(path string, p Palette) (*image.NRGBA, error) {
	img, err := load(path)
	if err != nil {
		return nil, err
	}
	bg := hexColor(p.BG)
	area, err := terminalArea(img, bg)
	if err != nil {
		return nil, err
	}
	first, last := inkRows(img, area, bg)
	if first < 0 {
		return nil, fmt.Errorf("the capture is empty")
	}
	r := image.Rect(area.Min.X, max(area.Min.Y, first-inset), area.Max.X, min(area.Max.Y, last+1+inset))
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Over)
	// The window's bottom corners are rounded, and what the rounding leaves
	// is the desktop's colour blended in: square them off.
	squareCorners(out, bg)
	return out, nil
}

// squareCorners paints the window's rounded bottom corners the background.
func squareCorners(img *image.NRGBA, bg color.NRGBA) {
	r := img.Bounds()
	const k = 2 * radius
	for y := r.Max.Y - k; y < r.Max.Y; y++ {
		for _, x0 := range []int{r.Min.X, r.Max.X - k} {
			for x := x0; x < x0+k; x++ {
				if !near(img.At(x, y), bg, 1) && luma(img.At(x, y)) < luma(bg)+0.02 {
					img.SetNRGBA(x, y, bg)
				}
			}
		}
	}
}

// evenUp pads the shorter of a pair at the bottom, so the page can swap one
// palette's image for the other's without anything below it moving. The two
// can differ: the gruv startup screen has a theme row the default does not.
func evenUp(pair map[string]*image.NRGBA) {
	h := 0
	for _, img := range pair {
		h = max(h, img.Bounds().Dy())
	}
	for name, img := range pair {
		if img.Bounds().Dy() == h {
			continue
		}
		p, _ := paletteNamed(name)
		out := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), h))
		draw.Draw(out, out.Bounds(), &image.Uniform{hexColor(p.BG)}, image.Point{}, draw.Src)
		draw.Draw(out, img.Bounds(), img, image.Point{}, draw.Src)
		pair[name] = out
	}
}

// ink cuts content to the columns that hold anything, padded as the rows
// are: a promo image is shown small, and the unused half of an eighty-column
// terminal would shrink everything in it. It keeps at least half the width,
// so a window never looks like a sliver.
func ink(img *image.NRGBA, p Palette) *image.NRGBA {
	bg := hexColor(p.BG)
	r := img.Bounds()
	right := r.Min.X
	for x := r.Max.X - 1; x >= r.Min.X && right == r.Min.X; x-- {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			if !near(img.At(x, y), bg, 6) {
				right = x
				break
			}
		}
	}
	w := min(r.Dx(), max(r.Dx()/2, right+1+inset))
	return img.SubImage(image.Rect(0, 0, w, r.Dy())).(*image.NRGBA)
}

// webp writes an image as a lossless WebP: text is edges, and a lossy codec
// rings around every one of them.
func webp(img image.Image, out string) error {
	tmp := out + ".png"
	if err := save(tmp, img); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if b, err := exec.Command("cwebp", "-quiet", "-lossless", "-exact", "-z", "9", "-metadata", "none", tmp, "-o", out).CombinedOutput(); err != nil {
		return fmt.Errorf("cwebp: %v: %s", err, b)
	}
	return nil
}

// window draws a window around content: a titlebar in the palette, the three
// lights, a title, rounded corners, a hairline edge and a shadow. It is drawn
// rather than captured because the captured titlebar is the person's — their
// focus, their Ghostty's update notice — and a promo image should show gluon.
func window(content *image.NRGBA, p Palette) (image.Image, error) {
	dir, err := os.MkdirTemp("", "shots-window")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "content.png")
	if err := save(in, content); err != nil {
		return nil, err
	}
	w, h := content.Bounds().Dx(), content.Bounds().Dy()+barHeight
	bar := mix(hexColor(p.BG), hexColor(p.FG), 0.07)
	sep := mix(hexColor(p.BG), color.NRGBA{A: 255}, 0.4)
	cy := barHeight / 2
	light := func(i int, fill string) []string {
		cx := 38 + 40*i
		return []string{"-fill", fill, "-stroke", "#00000026", "-strokewidth", "1",
			"-draw", fmt.Sprintf("circle %d,%d %d,%d", cx, cy, cx+12, cy)}
	}
	args := []string{
		"-size", fmt.Sprintf("%dx%d", w, h), "xc:" + hexOf(bar),
		in, "-geometry", fmt.Sprintf("+0+%d", barHeight), "-composite",
		"-fill", hexOf(sep), "-draw", fmt.Sprintf("rectangle 0,%d %d,%d", barHeight-2, w, barHeight-1),
	}
	args = append(args, light(0, "#FF5F57")...)
	args = append(args, light(1, "#FEBC2E")...)
	args = append(args, light(2, "#28C840")...)
	args = append(args,
		"-stroke", "none", "-font", barFont, "-pointsize", "25", "-fill", p.Ink3,
		"-gravity", "north", "-annotate", fmt.Sprintf("+0+%d", cy-16), "gluon", "-gravity", "northwest",
		"(", "-size", fmt.Sprintf("%dx%d", w, h), "xc:black", "-fill", "white",
		"-draw", fmt.Sprintf("roundrectangle 0,0 %d,%d %d,%d", w-1, h-1, radius, radius), ")",
		"-alpha", "off", "-compose", "CopyOpacity", "-composite", "-compose", "over",
		"-fill", "none", "-stroke", "#FFFFFF1F", "-strokewidth", "2",
		"-draw", fmt.Sprintf("roundrectangle 1,1 %d,%d %d,%d", w-2, h-2, radius-1, radius-1),
		"(", "+clone", "-background", "black", "-shadow", "60x36+0+22", ")",
		"+swap", "-background", "none", "-layers", "merge", "+repage", "png:-",
	)
	return magick(args...)
}

// poster puts a window on the site's page — its background, its glow, its dot
// grid — with the headline and the address, in a canvas of one promo size.
// The window is set above or beside the words, whichever lets it be larger,
// and never larger than it was captured.
func poster(win image.Image, headline string, p Palette, cw, ch int, out string) error {
	canvas := field(cw, ch, p)
	m := int(math.Round(0.06 * float64(min(cw, ch))))
	small := max(20, int(math.Round(float64(min(cw, ch))*0.024)))
	brand, err := text("gluon", monoFont, small+4, p.Accent, 0)
	if err != nil {
		return err
	}
	addr, err := text("github.com/sandboxws/gluon", monoFont, small, p.Ink3, 0)
	if err != nil {
		return err
	}
	headSize := int(math.Round(float64(min(cw, ch)) * 0.058))

	ww, wh := float64(win.Bounds().Dx()), float64(win.Bounds().Dy())
	fit := func(w, h int) float64 { return math.Min(1, math.Min(float64(w)/ww, float64(h)/wh)) }

	// Stacked: the brand line, the headline under it, the window below.
	var head image.Image
	if headline != "" {
		if head, err = balanced(headline, headFont, headSize, p.Ink, cw-2*m, "center"); err != nil {
			return err
		}
	}
	top := m + brand.Bounds().Dy()
	if head != nil {
		top += m/2 + head.Bounds().Dy()
	}
	top += int(0.7 * float64(m))
	stacked := fit(cw-2*m, ch-top-m)

	// Beside: the words in a left column, the window to their right.
	col := int(0.36 * float64(cw))
	var sideHead image.Image
	if headline != "" {
		if sideHead, err = balanced(headline, headFont, headSize, p.Ink, col-m, "west"); err != nil {
			return err
		}
	}
	beside := fit(cw-col-2*m, ch-2*m)

	if beside > stacked*1.08 {
		scaled, err := resize(win, beside)
		if err != nil {
			return err
		}
		x := col + m + (cw-col-2*m-scaled.Bounds().Dx())/2
		y := (ch - scaled.Bounds().Dy()) / 2
		paste(canvas, scaled, x, y)
		paste(canvas, brand, m, m)
		if sideHead != nil {
			paste(canvas, sideHead, m, m+brand.Bounds().Dy()+m/2)
		}
		paste(canvas, addr, m, ch-m-addr.Bounds().Dy())
	} else {
		scaled, err := resize(win, stacked)
		if err != nil {
			return err
		}
		paste(canvas, brand, m, m)
		paste(canvas, addr, cw-m-addr.Bounds().Dx(), m+brand.Bounds().Dy()-addr.Bounds().Dy())
		if head != nil {
			paste(canvas, head, (cw-head.Bounds().Dx())/2, m+brand.Bounds().Dy()+m/2)
		}
		x := (cw - scaled.Bounds().Dx()) / 2
		y := top + (ch-top-m-scaled.Bounds().Dy())/2
		paste(canvas, scaled, x, y)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return save(out, canvas)
}

// field is the site's page background, drawn as its stylesheet draws .field:
// the page colour, a glow of the accent from the top centre, and a dot grid
// fading out from the top.
func field(w, h int, p Palette) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	bg, acc, ink := hexColor(p.Page), hexColor(p.Accent), hexColor(p.Ink)
	cx := float64(w) / 2
	step := 26.0 * math.Max(1, float64(w)/1440)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			// radial-gradient(58% 34% at 50% 0%, accent 7%, transparent 70%)
			d := math.Hypot((fx-cx)/(0.58*float64(w)), fy/(0.34*float64(h)))
			c := mix(bg, acc, 0.07*math.Max(0, 1-d/0.7))
			// radial-gradient(ink 5%, 1px, transparent 1px) every 26px, masked
			// by radial-gradient(70% 90% at 50% 0%, #000, transparent 78%)
			// over the top 52% of the page.
			if fy < 0.52*float64(h) {
				gx, gy := math.Mod(fx, step)-step/2, math.Mod(fy, step)-step/2
				dot := math.Max(0, math.Min(1, 1.6-math.Hypot(gx, gy)))
				mask := math.Max(0, 1-math.Hypot((fx-cx)/(0.7*float64(w)), fy/(0.9*0.52*float64(h)))/0.78)
				c = mix(c, ink, 0.06*dot*mask)
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// text renders words in a font, wrapped at width when width is not 0, and
// cut to their ink.
func text(s, font string, size int, fill string, width int) (image.Image, error) {
	return render(s, font, size, fill, width, "west", true)
}

// render draws words with ImageMagick: on one line with label:, or wrapped at
// width with caption:, aligned by gravity, and cut to their ink when trim.
func render(s, font string, size int, fill string, width int, gravity string, trim bool) (image.Image, error) {
	// magick reads a leading @ as a file and % as an escape.
	s = strings.NewReplacer("%", "%%", `\`, `\\`).Replace(strings.TrimPrefix(s, "@"))
	args := []string{"-background", "none", "-fill", fill, "-font", font, "-pointsize", strconv.Itoa(size), "-kerning", "-0.5", "-gravity", gravity}
	if width > 0 {
		args = append(args, "-size", fmt.Sprintf("%dx", width), "caption:"+s)
	} else {
		args = append(args, "label:"+s)
	}
	if trim {
		args = append(args, "-trim", "+repage")
	}
	return magick(append(args, "png:-")...)
}

// balanced wraps a headline in as few lines as width allows, and then as
// evenly as those lines allow: the narrowest width that still takes no more
// lines. A greedy wrap leaves one word on a line of its own, which in a
// headline reads as the word the line forgot.
func balanced(s, font string, size int, fill string, width int, gravity string) (image.Image, error) {
	lines := func(w int) (int, error) {
		img, err := render(s, font, size, fill, w, gravity, false)
		if err != nil {
			return 0, err
		}
		return img.Bounds().Dy(), nil
	}
	h, err := lines(width)
	if err != nil {
		return nil, err
	}
	lo, hi := width/3, width
	for hi-lo > 8 {
		mid := (lo + hi) / 2
		mh, err := lines(mid)
		if err != nil {
			return nil, err
		}
		if mh > h {
			lo = mid
		} else {
			hi = mid
		}
	}
	return render(s, font, size, fill, hi, gravity, true)
}

// resize scales an image with Lanczos, the filter that keeps text sharpest
// when it shrinks.
func resize(img image.Image, scale float64) (image.Image, error) {
	if scale >= 0.999 {
		return img, nil
	}
	var in bytes.Buffer
	if err := png.Encode(&in, img); err != nil {
		return nil, err
	}
	cmd := exec.Command("magick", "png:-", "-filter", "Lanczos", "-resize", fmt.Sprintf("%.3f%%", scale*100), "png:-")
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("magick resize: %v", err)
	}
	return png.Decode(bytes.NewReader(out))
}

// magick runs ImageMagick and decodes the PNG it writes to stdout.
func magick(args ...string) (image.Image, error) {
	var errb bytes.Buffer
	cmd := exec.Command("magick", args...)
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("magick: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return png.Decode(bytes.NewReader(out))
}

// paste draws src over dst with its top-left at x, y.
func paste(dst *image.NRGBA, src image.Image, x, y int) {
	b := src.Bounds()
	draw.Draw(dst, image.Rect(x, y, x+b.Dx(), y+b.Dy()), src, b.Min, draw.Over)
}

// mix is a blended a fraction t of the way to b.
func mix(a, b color.NRGBA, t float64) color.NRGBA {
	f := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.NRGBA{R: f(a.R, b.R), G: f(a.G, b.G), B: f(a.B, b.B), A: 255}
}

func hexOf(c color.NRGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

// firstFont is the first of the fonts ImageMagick knows.
func firstFont(names ...string) string {
	out, _ := exec.Command("magick", "-list", "font").Output()
	for _, n := range names {
		if bytes.Contains(out, []byte("Font: "+n+"\n")) {
			return n
		}
	}
	return names[len(names)-1]
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o644)
}
