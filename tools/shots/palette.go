package main

// A Palette is one of the site's two, as a terminal draws it: the site's code
// well for the background, so a shot sits flush on the page, the theme's
// ident colour for text gluon does not paint, its prompt colour for the
// cursor, and the gluon theme whose roles the site's code colours are. A
// promo image is the site's page around the window: its background, accent,
// ink, and the quieter ink it sets small print in.
type Palette struct {
	Name, Theme, BG, FG, Cursor string
	Page, Accent, Ink, Ink3     string
}

var palettes = []Palette{
	{Name: "go", Theme: "go", BG: "#0C0C0D", FG: "#E4E4E7", Cursor: "#4FC9EE",
		Page: "#0A0A0A", Accent: "#00ADD8", Ink: "#EDEDED", Ink3: "#7C7C84"},
	{Name: "gruv", Theme: "gruvppuccin-mocha", BG: "#141617", FG: "#D4BE98", Cursor: "#7DAEA3",
		Page: "#101213", Accent: "#7DAEA3", Ink: "#D4BE98", Ink3: "#A69A8A"},
}

func paletteNamed(name string) (Palette, bool) {
	for _, p := range palettes {
		if p.Name == name {
			return p, true
		}
	}
	return Palette{}, false
}
