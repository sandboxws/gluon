package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/ui"
)

// initJSON is the -json envelope.
//
// There is deliberately no field here that can hold a connection string's
// value. DSNRef is a reference and Resolves is a boolean — "does it work" is
// the useful answer, and "here it is" is a password in a file somebody pipes to
// jq.
type initJSON struct {
	OK        bool             `json:"ok"`
	Module    string           `json:"module,omitempty"`
	Root      string           `json:"root"`
	Detected  []detectedJSON   `json:"detected,omitempty"`
	Chosen    *detectedJSON    `json:"chosen,omitempty"`
	Ambiguous bool             `json:"ambiguous,omitempty"`
	Note      string           `json:"note,omitempty"`
	Drivers   []initDriverJSON `json:"drivers,omitempty"`
	Searched  []string         `json:"searched,omitempty"`
	Roots     []string         `json:"roots,omitempty"`
	Ceiling   string           `json:"ceiling,omitempty"`
	Stanza    string           `json:"stanza,omitempty"`
}

type detectedJSON struct {
	Driver   string   `json:"driver"`
	Target   string   `json:"target"`
	Redacted string   `json:"redacted"`
	From     []string `json:"from"`
	// DSNRef names where a connection string lives. Never its value.
	DSNRef   string   `json:"dsnRef,omitempty"`
	Rank     string   `json:"rank"`
	Concerns []string `json:"concerns,omitempty"`
}

type initDriverJSON struct {
	Module string `json:"module"`
	Family string `json:"family"`
	Name   string `json:"name"`
}

func buildInitJSON(module, root string, det db.Detection) *initJSON {
	rep := &initJSON{
		OK: true, Module: module, Root: root,
		Ambiguous: det.Ambiguous, Note: det.Note,
		Searched: det.Searched, Roots: det.Roots, Ceiling: det.Ceiling,
	}
	for _, c := range det.Candidates {
		rep.Detected = append(rep.Detected, candidateJSON(c))
	}
	if det.Chosen != nil {
		j := candidateJSON(*det.Chosen)
		rep.Chosen = &j
		rep.Stanza = config.Stanza(databaseFor(module, *det.Chosen))
	}
	for _, d := range det.Drivers {
		rep.Drivers = append(rep.Drivers, initDriverJSON{Module: d.Module, Family: d.Family, Name: d.Name})
	}
	return rep
}

func candidateJSON(c db.Candidate) detectedJSON {
	j := detectedJSON{
		Driver:   c.DSN.Driver,
		Target:   c.DSN.Target(),
		Redacted: c.DSN.Redacted(),
		Rank:     c.Rank.String(),
		Concerns: c.Concerns,
	}
	for _, p := range c.From {
		j.From = append(j.From, p.String())
	}
	switch {
	case c.Secret.Env != "":
		j.DSNRef = "env:" + c.Secret.Env
	case c.Secret.File != "" && c.Secret.Key != "":
		j.DSNRef = "file:" + c.Secret.File + "#" + c.Secret.Key
	}
	return j
}

// renderDetection prints the evidence before anything is asked.
//
// The form that follows only makes sense once somebody can see what gluon
// found; asking first and explaining afterwards would be asking them to
// confirm something they cannot check.
func renderDetection(module, root string, det db.Detection, th ui.Theme) string {
	var b strings.Builder
	name := module
	if name == "" {
		name = shortenHome(root)
	}
	b.WriteString(th.Heading.Render(name) + "\n\n")

	switch {
	case det.Ambiguous:
		b.WriteString("several databases, and gluon will not guess between them:\n\n")
		for i, c := range det.Candidates {
			b.WriteString(fmt.Sprintf("  %d  %-9s %s\n", i+1, c.DSN.Driver, th.Path.Render(c.DSN.Redacted())))
			for _, p := range c.From {
				b.WriteString(th.Dim.Render("       "+relTo(root, p.String())) + "\n")
			}
		}
		b.WriteString("\n")

	case det.Chosen != nil:
		b.WriteString("found one database:\n\n")
		b.WriteString("  " + th.OK.Render(det.Chosen.DSN.Driver) + "  " +
			th.Path.Render(det.Chosen.DSN.Redacted()) + "\n")
		for _, p := range det.Chosen.From {
			b.WriteString(th.Dim.Render("    "+relTo(root, p.String())) + "\n")
		}
		for _, c := range det.Chosen.Concerns {
			b.WriteString(th.Dim.Render("    note: "+c) + "\n")
		}
		if det.Note != "" {
			b.WriteString(th.Dim.Render("    note: "+det.Note) + "\n")
		}
		b.WriteString("\n")

	default:
		b.WriteString("no database found.\n")
		if len(det.Roots) > 0 {
			b.WriteString(th.Dim.Render("  looked in "+strings.Join(relAll(root, det.Roots), "  ")) + "\n")
		}
		if det.Ceiling != "" {
			b.WriteString(th.Dim.Render("  stopped at the repository root") + "\n")
		}
		b.WriteString("\n")
	}

	if len(det.Drivers) > 0 {
		b.WriteString(th.Dim.Render("  drivers in the build list") + "\n")
		for _, d := range det.Drivers {
			b.WriteString(th.Dim.Render("    "+d.Module+"  ("+d.Family+")") + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func relTo(root, p string) string {
	file, rest, found := strings.Cut(p, "  ")
	if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
		file = rel
	} else {
		file = shortenHome(file)
	}
	if found {
		return file + "  " + rest
	}
	return file
}

func relAll(root string, in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		out = append(out, relTo(root, p))
	}
	return out
}

// marshalIndent is what -json emits, exposed so a test can assert on the exact
// bytes a script would receive.
func marshalIndent(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}
