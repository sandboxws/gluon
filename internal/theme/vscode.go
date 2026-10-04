package theme

import (
	"encoding/json"
	"fmt"
)

// A VS Code theme is the same TextMate scope model in JSON, which is the form
// almost every published theme takes now.
type vsTheme struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Include string `json:"include"`
	// Colors are UI colours a .tmTheme cannot express — a panel border, a line
	// number — and they are where gluon's non-syntax roles come from.
	//
	// Decoded loosely, because a published theme can get one of them wrong and
	// map[string]string would refuse the whole file over it: GitHub Dark
	// Default writes symbolIcon.constantForeground as an array, and the theme is
	// otherwise perfectly readable. Dropping the one entry costs a colour gluon
	// does not use; refusing the file costs the theme.
	Colors      map[string]json.RawMessage `json:"colors"`
	TokenColors []vsToken                  `json:"tokenColors"`
}

// strings keeps the entries that are actually strings.
func (v vsTheme) strings() map[string]string {
	out := make(map[string]string, len(v.Colors))
	for k, raw := range v.Colors {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			out[k] = s
		}
	}
	return out
}

type vsToken struct {
	// Scope is a string or a list of them, so it is decoded by hand.
	Scope    json.RawMessage   `json:"scope"`
	Settings map[string]string `json:"settings"`
}

func readVSCode(data []byte) (tmTheme, error) {
	var v vsTheme
	if err := json.Unmarshal(stripJSONC(data), &v); err != nil {
		return tmTheme{}, fmt.Errorf("not a VS Code theme: %w", err)
	}
	if v.Include != "" {
		// Following it would mean resolving a relative path out of a file the
		// user handed us, which is a question a colour converter should not be
		// asking. Naming the file is more useful than guessing at it.
		return tmTheme{}, fmt.Errorf(
			"this theme includes %q — import that file instead, or merge them by hand", v.Include)
	}
	if len(v.TokenColors) == 0 && len(v.Colors) == 0 {
		return tmTheme{}, fmt.Errorf("no tokenColors and no colors: nothing to convert")
	}
	colors := v.strings()
	// A VS Code theme says which ground it was drawn for, in a field gluon
	// parsed and never read until there were light themes to ship. Carried raw:
	// toFile weighs it against the background, and prefers the background.
	tm := tmTheme{Name: v.Name, Format: "VS Code", Appearance: v.Type,
		Global: map[string]string{}, Extra: colors}
	if fg := normaliseHex(colors["editor.foreground"]); fg != "" {
		tm.Global["foreground"] = fg
	}
	if bg := normaliseHex(colors["editor.background"]); bg != "" {
		tm.Global["background"] = bg
	}
	for _, t := range v.TokenColors {
		scopes, err := decodeScopes(t.Scope)
		if err != nil {
			return tmTheme{}, err
		}
		if len(scopes) == 0 {
			for k, val := range t.Settings {
				if _, ok := tm.Global[k]; !ok {
					tm.Global[k] = val
				}
			}
			continue
		}
		tm.Rules = append(tm.Rules, tmRule{Scopes: scopes, Settings: t.Settings})
	}
	return tm, nil
}

func decodeScopes(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return splitScopes(one), nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("a tokenColors scope is neither a string nor a list of them")
	}
	var out []string
	for _, s := range many {
		out = append(out, splitScopes(s)...)
	}
	return out, nil
}

// stripJSONC removes // and /* */ comments and trailing commas.
//
// Not optional. VS Code reads its theme files as JSONC and the published ones
// use it freely — the stock Dark+ theme opens with a comment — so
// encoding/json alone would refuse a large fraction of what people actually
// have on disk. Telling them to strip the comments themselves is not a
// convenience command.
//
// Two passes rather than one, and that is the whole point of the split: a
// trailing comma is one with nothing but a closing brace after it, and whether
// that is true cannot be decided while the comments are still in the way.
// Tokyo Night Light writes `"foreground": "#0f4b6e" //"#33635c"` and then a
// brace, and a single pass looking ahead through the original bytes sees the
// slash, keeps the comma, and hands encoding/json a trailing comma it had
// itself created.
//
// Byte-preserving is not a goal here, unlike everywhere else in this change:
// the output is fed to a JSON parser and thrown away. What matters is that a
// comment marker inside a string literal is left alone, which is the one thing
// a naive stripper gets wrong.
func stripJSONC(data []byte) []byte {
	return stripTrailingCommas(stripComments(data))
}

func stripComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}

func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == ',':
			// A trailing comma is one followed by a closing brace or bracket.
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue // drop it
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
