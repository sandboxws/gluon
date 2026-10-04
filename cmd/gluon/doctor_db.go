package main

import (
	"os/exec"
	"path/filepath"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/ui"
)

// doctorDatabaseJSON is the database section of the report.
//
// There is deliberately no field here that can hold a connection string's
// value. DSNRef names a location and Resolves is a boolean — "does it work" is
// the answer worth having, and "here it is" would be a password in whatever a
// script pipes this to.
type doctorDatabaseJSON struct {
	ConfigPath string           `json:"configPath,omitempty"`
	Tracked    bool             `json:"configTrackedByGit,omitempty"`
	Databases  []doctorDBJSON   `json:"databases,omitempty"`
	Detected   []detectedJSON   `json:"detected,omitempty"`
	Ambiguous  bool             `json:"ambiguous,omitempty"`
	Note       string           `json:"note,omitempty"`
	Drivers    []initDriverJSON `json:"drivers,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type doctorDBJSON struct {
	Name              string `json:"name"`
	Driver            string `json:"driver"`
	DSNRef            string `json:"dsnRef"`
	DSNResolves       bool   `json:"dsnResolves"`
	ResolveError      string `json:"resolveError,omitempty"`
	DriverModule      string `json:"driverModule,omitempty"`
	ModuleInBuildList bool   `json:"moduleInBuildList"`
	ReadOnly          bool   `json:"readOnly"`
}

// collectDatabase answers the same three questions :db does: what is
// configured, what could be detected, and which drivers exist to open it with.
//
// Not finding a database is exit 0. It is a fact about the project rather than
// something gluon depends on being broken — the same stance doctor already
// takes about not being inside a module.
func collectDatabase(dir, module string, requires []string, cfg *config.Config) *doctorDatabaseJSON {
	out := &doctorDatabaseJSON{}

	configured, from := configuredDatabases(dir, module, cfg)
	out.ConfigPath = from
	if from != "" && filepath.Base(from) == config.ProjectFile {
		out.Tracked = trackedByGit(from)
	}

	drivers := db.DriversIn(requires)
	for _, d := range drivers {
		out.Drivers = append(out.Drivers, initDriverJSON{Module: d.Module, Family: d.Family, Name: d.Name})
	}

	for _, d := range configured {
		j := doctorDBJSON{
			Name:         d.Label(),
			Driver:       d.Driver,
			DSNRef:       d.SecretRef(),
			DriverModule: d.DriverModule,
			ReadOnly:     d.IsReadOnly(),
		}
		conn, err := db.Resolve(d)
		if err != nil {
			j.ResolveError = err.Error()
		} else {
			j.DSNResolves = true
			family := conn.Driver
			if family == "" {
				family = d.Driver
			}
			if drv, ok := db.DriverFor(family, drivers, d.DriverModule); ok {
				j.ModuleInBuildList = true
				j.DriverModule = drv.Module
			}
		}
		out.Databases = append(out.Databases, j)
	}

	if len(configured) > 0 {
		return out
	}

	det := db.Detect(dir, requires)
	out.Ambiguous, out.Note = det.Ambiguous, det.Note
	for _, c := range det.Candidates {
		out.Detected = append(out.Detected, candidateJSON(c))
	}
	return out
}

// configuredDatabases resolves the same order :db uses: a project file wins,
// then the global config, and they are never merged.
func configuredDatabases(dir, module string, cfg *config.Config) ([]config.Database, string) {
	if dir != "" {
		if p, ok := config.FindProject(dir); ok {
			if proj, err := config.LoadProject(p); err == nil && proj != nil && len(proj.Databases) > 0 {
				return proj.Databases, p
			}
		}
	}
	if cfg == nil {
		return nil, ""
	}
	got := cfg.DatabasesFor(module)
	if len(got) == 0 {
		return nil, ""
	}
	return got, cfg.Path
}

// trackedByGit reports a gluon.toml that git already follows.
//
// The permanently-rejected _gluon/ entry names this failure exactly: a file in
// a product repo "would eventually be caught by a stray `git add -A`". The fix
// for a named failure mode is to report it, not to hope.
func trackedByGit(path string) bool {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", filepath.Base(path))
	cmd.Dir = filepath.Dir(path)
	return cmd.Run() == nil
}

// renderDatabase is the terminal form of the same struct the -json envelope is
// built from, so the two cannot report different things.
func renderDatabase(d *doctorDatabaseJSON, th ui.Theme, row func(k, v string)) {
	if d == nil {
		return
	}
	line := row

	switch {
	case len(d.Databases) > 0:
		for _, x := range d.Databases {
			status := th.OK.Render("resolves")
			if !x.DSNResolves {
				status = th.Fail.Render("does not resolve") + th.Dim.Render("  "+x.ResolveError)
			}
			line(x.Name, x.Driver+"  "+th.Path.Render(x.DSNRef)+"  "+status)
			switch {
			case x.ModuleInBuildList:
				line("", th.Dim.Render("driver "+x.DriverModule+" is in the build list"))
			case x.DSNResolves:
				line("", th.Fail.Render("no "+x.Driver+" driver in the build list")+
					th.Dim.Render(" — gluon never links one"))
			}
			if !x.ReadOnly {
				line("", th.Dim.Render("writable — :query still needs -w per statement"))
			}
		}
		line("config", th.Path.Render(shortenHome(d.ConfigPath)))
		if d.Tracked {
			line("", th.Dim.Render("tracked by git — it holds no secret, only the name of one, "+
				"but it travels with the repository"))
		}

	case d.Ambiguous:
		line("detected", th.Fail.Render("several, and gluon will not guess"))
		for _, c := range d.Detected {
			line("", c.Driver+"  "+th.Path.Render(c.Redacted))
			for _, f := range c.From {
				line("", th.Dim.Render("  "+f))
			}
		}
		line("config", th.Dim.Render("none — gluon init records one"))

	case len(d.Detected) > 0:
		c := d.Detected[0]
		line("detected", c.Driver+"  "+th.Path.Render(c.Redacted))
		for _, f := range c.From {
			line("", th.Dim.Render(f))
		}
		if d.Note != "" {
			line("", th.Dim.Render(d.Note))
		}
		if c.DSNRef != "" {
			line("secret", th.Path.Render(c.DSNRef))
		}
		line("config", th.Dim.Render("none — gluon init would write one"))

	default:
		line("detected", th.Dim.Render("none"))
	}

	for _, drv := range d.Drivers {
		line("driver", drv.Module+th.Dim.Render("  a "+drv.Family+" driver"))
	}
	if len(d.Drivers) == 0 && (len(d.Detected) > 0 || len(d.Databases) > 0) {
		line("driver", th.Dim.Render("none in the build list — :query cannot open anything yet"))
	}
}
