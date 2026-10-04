package theme

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// A .tmTheme is an Apple property list, and this reads the small subset one
// uses: dict, array, string, and scalars it skips.
//
// Hand-written over encoding/xml rather than a plist library, because
// CONTRIBUTING says no new dependencies without an issue first and this is
// eighty lines against a format that has not changed since TextMate 1. It is a
// converter, not a detector: what it cannot read it refuses by name and line,
// rather than returning a theme with a colour quietly missing.
//
// Worth knowing before someone asks: a .tmTheme opens with
// <!DOCTYPE plist PUBLIC "..." "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
// and encoding/xml never fetches an external DTD — it surfaces the line as an
// xml.Directive token and moves on. There is no network access here and no
// external-entity surface.
type plistKind int

const (
	pOther plistKind = iota
	pDict
	pArray
	pString
)

type plistValue struct {
	kind  plistKind
	str   string
	dict  map[string]*plistValue
	array []*plistValue
}

func parsePlist(data []byte) (*plistValue, error) {
	d := xml.NewDecoder(strings.NewReader(string(data)))
	d.Strict = true
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("not a property list: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue // CharData, Comment, ProcInst, and the DOCTYPE Directive
		}
		if start.Name.Local != "plist" {
			return parseValue(d, start)
		}
		// The plist element wraps exactly one value.
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, fmt.Errorf("empty property list: %w", err)
			}
			if s, ok := tok.(xml.StartElement); ok {
				return parseValue(d, s)
			}
			if _, ok := tok.(xml.EndElement); ok {
				return nil, fmt.Errorf("empty property list")
			}
		}
	}
}

func parseValue(d *xml.Decoder, start xml.StartElement) (*plistValue, error) {
	switch start.Name.Local {
	case "string", "key":
		var s string
		if err := d.DecodeElement(&s, &start); err != nil {
			return nil, err
		}
		return &plistValue{kind: pString, str: s}, nil

	case "dict":
		v := &plistValue{kind: pDict, dict: map[string]*plistValue{}}
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.EndElement:
				return v, nil
			case xml.StartElement:
				if t.Name.Local != "key" {
					return nil, fmt.Errorf("line %d: expected <key> inside <dict>, got <%s>",
						lineOf(d), t.Name.Local)
				}
				var key string
				if err := d.DecodeElement(&key, &t); err != nil {
					return nil, err
				}
				val, err := nextValue(d)
				if err != nil {
					return nil, err
				}
				// Last wins. Real themes do carry duplicate keys, and refusing
				// one over that would be refusing a theme every editor reads.
				v.dict[key] = val
			}
		}

	case "array":
		v := &plistValue{kind: pArray}
		for {
			tok, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.EndElement:
				return v, nil
			case xml.StartElement:
				item, err := parseValue(d, t)
				if err != nil {
					return nil, err
				}
				v.array = append(v.array, item)
			}
		}

	default:
		// integer, real, data, date, true, false — present in some themes,
		// never a colour, and skipping is what keeps this total.
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return &plistValue{kind: pOther}, nil
	}
}

func nextValue(d *xml.Decoder) (*plistValue, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return parseValue(d, t)
		case xml.EndElement:
			return nil, fmt.Errorf("line %d: <key> with no value", lineOf(d))
		}
	}
}

func lineOf(d *xml.Decoder) int64 { return d.InputOffset() }

// readPlist turns a parsed plist into the shape both formats share.
func readPlist(data []byte) (tmTheme, error) {
	root, err := parsePlist(data)
	if err != nil {
		return tmTheme{}, err
	}
	if root.kind != pDict {
		return tmTheme{}, fmt.Errorf("a .tmTheme is a dictionary at its top level")
	}
	tm := tmTheme{Format: "tmTheme", Global: map[string]string{}}
	if n := root.dict["name"]; n != nil && n.kind == pString {
		tm.Name = n.str
	}
	settings := root.dict["settings"]
	if settings == nil || settings.kind != pArray {
		return tmTheme{}, fmt.Errorf("a .tmTheme has a `settings` array; this one does not")
	}
	for _, item := range settings.array {
		if item.kind != pDict {
			continue
		}
		inner := item.dict["settings"]
		if inner == nil || inner.kind != pDict {
			continue
		}
		vals := map[string]string{}
		for k, v := range inner.dict {
			if v.kind == pString {
				vals[k] = v.str
			}
		}
		scope := item.dict["scope"]
		if scope == nil || scope.kind != pString {
			// No scope: this is the rule that sets the editor's own colours.
			for k, v := range vals {
				tm.Global[k] = v
			}
			continue
		}
		tm.Rules = append(tm.Rules, tmRule{Scopes: splitScopes(scope.str), Settings: vals})
	}
	return tm, nil
}

func splitScopes(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
