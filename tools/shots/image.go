package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
)

// load decodes a PNG.
func load(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// save encodes a PNG.
func save(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// litCapture is the capture of two whose cursor is lit. They were taken half
// a blink apart with nothing else running, so they differ only in the
// cursor's cell, and the lit one is the brighter there. Two that differ
// widely mean the window changed between them: the later is kept, and said.
func litCapture(a, b string) (string, string, error) {
	ia, err := load(a)
	if err != nil {
		return "", "", err
	}
	ib, err := load(b)
	if err != nil {
		return "", "", err
	}
	if ia.Bounds() != ib.Bounds() {
		return b, "the window changed size between captures; kept the later", nil
	}
	r := ia.Bounds()
	diff := 0
	var la, lb float64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ca, cb := ia.At(x, y), ib.At(x, y)
			if ca == cb {
				continue
			}
			if sameColor(ca, cb) {
				continue
			}
			diff++
			la += luma(ca)
			lb += luma(cb)
		}
	}
	switch {
	case diff == 0:
		return a, "", nil
	case diff > r.Dx()*r.Dy()/50:
		return b, fmt.Sprintf("%d pixels changed between captures — more than a cursor; kept the later", diff), nil
	case la >= lb:
		return a, "", nil
	}
	return b, "", nil
}

func sameColor(a, b color.Color) bool {
	r1, g1, b1, a1 := a.RGBA()
	r2, g2, b2, a2 := b.RGBA()
	return r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2
}

// luma is a colour's brightness, 0 to 1, weighted as the eye weighs it, and
// scaled by its opacity.
func luma(c color.Color) float64 {
	r, g, b, a := c.RGBA()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 65535 * float64(a) / 65535
}

// hexColor reads #RRGGBB.
func hexColor(s string) color.NRGBA {
	v, _ := strconv.ParseUint(s[1:], 16, 32)
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

// is says whether a pixel is exactly an opaque colour.
func is(c color.Color, want color.NRGBA) bool {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	return n == want
}

// near says whether a pixel is within a few steps of a colour in each
// channel: what the window server's colour matching leaves of an exact one.
func near(c color.Color, want color.NRGBA, tol int) bool {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	d := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	return n.A == 255 && d(n.R, want.R) <= tol && d(n.G, want.G) <= tol && d(n.B, want.B) <= tol
}

// terminalArea is the part of a window capture below its titlebar: the first
// row, from the top, that is the terminal's background right across.
func terminalArea(img image.Image, bg color.NRGBA) (image.Rectangle, error) {
	r := img.Bounds()
	w := r.Dx()
	probes := []int{r.Min.X + w/4, r.Min.X + w/2, r.Min.X + 3*w/4}
	for y := r.Min.Y; y < r.Min.Y+r.Dy()/3; y++ {
		all := true
		for _, x := range probes {
			if !near(img.At(x, y), bg, 2) {
				all = false
				break
			}
		}
		if all {
			return image.Rect(r.Min.X, y, r.Max.X, r.Max.Y), nil
		}
	}
	return image.Rectangle{}, fmt.Errorf("no row of the capture is the background %v: is the palette's colour what the window drew?", bg)
}

// inkRows is the first and last row, within area, holding anything that is
// not the background.
func inkRows(img image.Image, area image.Rectangle, bg color.NRGBA) (int, int) {
	first, last := -1, -1
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			c := img.At(x, y)
			if _, _, _, a := c.RGBA(); a < 0xff00 {
				continue // the window's rounded corner
			}
			if !near(c, bg, 6) {
				if first < 0 {
					first = y
				}
				last = y
				break
			}
		}
	}
	return first, last
}
