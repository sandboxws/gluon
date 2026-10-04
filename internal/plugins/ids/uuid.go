// Package ids holds the plugins for identifier and number types — the ones
// whose default rendering is least useful because their meaning is an encoding,
// not a shape.
package ids

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
)

// UUID is the plugin for github.com/google/uuid.
//
// uuid.UUID is [16]byte, so the value printer shows sixteen small integers.
// Everything a UUID means — its canonical form, its version, its variant — is
// an encoding of those bytes, which makes this the case a renderer is exactly
// right for: the child already sent every byte, and nothing needs to run.
type UUID struct{}

func (UUID) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "uuid",
		Module:  "github.com/google/uuid",
		Summary: "UUIDs as UUIDs, with the version and variant decoded",
	}
}

func (UUID) Imports() []plugin.Import {
	return []plugin.Import{{Name: "uuid", Path: "github.com/google/uuid"}}
}

func (UUID) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "uuid", Module: "github.com/google/uuid"}}
}

func (UUID) Renders() []plugin.Render {
	return []plugin.Render{{
		Type: "uuid.UUID",
		Rich: func(v pretty.Value, st pretty.Styles) (string, bool) {
			b, ok := byteList(v, 16)
			if !ok {
				return "", false
			}
			out := st.Type.Render("(uuid.UUID)") + " " + st.Str.Render(canonical(b))
			if note := decode(b); note != "" {
				out += "  " + st.Annot.Render(note)
			}
			return out, true
		},
		Inline: func(v pretty.Value, st pretty.Styles) (string, bool) {
			b, ok := byteList(v, 16)
			if !ok {
				return "", false
			}
			// The version is dropped here and the canonical form kept: a table
			// of UUIDs is scanned for which one, not for what kind.
			return st.Str.Render(canonical(b)), true
		},
	}}
}

// canonical is the 8-4-4-4-12 form.
func canonical(b []byte) string {
	hex := func(from, to int) string {
		var sb strings.Builder
		for _, c := range b[from:to] {
			sb.WriteString(fmt.Sprintf("%02x", c))
		}
		return sb.String()
	}
	return hex(0, 4) + "-" + hex(4, 6) + "-" + hex(6, 8) + "-" + hex(8, 10) + "-" + hex(10, 16)
}

// decode reads the version and variant out of the bits RFC 4122 puts them in.
//
// This is worth showing because the two are the difference between a UUID that
// sorts by time and one that does not, and neither is visible in the hex.
func decode(b []byte) string {
	version := b[6] >> 4

	var variant string
	switch {
	case b[8]&0x80 == 0x00:
		variant = "reserved (NCS)"
	case b[8]&0xc0 == 0x80:
		variant = "RFC 4122"
	case b[8]&0xe0 == 0xc0:
		variant = "reserved (Microsoft)"
	default:
		variant = "reserved (future)"
	}

	kind := map[byte]string{
		1: "time-based",
		2: "DCE security",
		3: "MD5 name-based",
		4: "random",
		5: "SHA-1 name-based",
		6: "time-ordered",
		7: "unix-time-ordered",
	}[version]

	if isNil(b) {
		return "the nil UUID"
	}
	out := "v" + strconv.Itoa(int(version))
	if kind != "" {
		out += " " + kind
	}
	return out + ", " + variant
}

func isNil(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// byteList reassembles a fixed-size byte array the encoder sent as a list. Each
// element arrives in the byte renderer's form — "244 (0xf4)" — so the leading
// decimal is what to read.
func byteList(v pretty.Value, n int) ([]byte, bool) {
	if v.Kind != "list" || len(v.Items) != n {
		return nil, false
	}
	out := make([]byte, 0, n)
	for _, it := range v.Items {
		field, _, _ := strings.Cut(it.Repr, " ")
		b, err := strconv.ParseUint(field, 10, 8)
		if err != nil {
			return nil, false
		}
		out = append(out, byte(b))
	}
	return out, true
}
