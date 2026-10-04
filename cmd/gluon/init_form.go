package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/dsn"
	"github.com/sandboxws/gluon/internal/ui"
)

// promptForDatabase asks only what detection could not work out.
//
// It follows promptForScratch, which is the repo's only other form and encodes
// three rules worth inheriting: it runs only on a terminal, there is always a
// flag that skips it, and a cancelled form is a decision rather than a failure.
//
// There is deliberately no field for typing a password. The description says
// why, in the form rather than in documentation nobody reads: gluon stores
// where a secret lives, not the secret.
func promptForDatabase(module string, det db.Detection, th ui.Theme) (config.Database, error) {
	// When detection found candidates but could not choose, the first question
	// is which one — never a guess made on the user's behalf.
	if len(det.Candidates) > 0 {
		chosen, err := chooseCandidate(det)
		if err != nil {
			return config.Database{}, err
		}
		if chosen != nil {
			return finishEntry(databaseFor(module, *chosen), det)
		}
	}
	return askFromScratch(module, det)
}

func chooseCandidate(det db.Detection) (*db.Candidate, error) {
	opts := make([]huh.Option[int], 0, len(det.Candidates)+1)
	for i, c := range det.Candidates {
		label := c.DSN.Driver + "  " + c.DSN.Redacted()
		if len(c.From) > 0 {
			label += "   (" + c.From[0].String() + ")"
		}
		opts = append(opts, huh.NewOption(label, i))
	}
	opts = append(opts, huh.NewOption("None of these — let me type it", -1))

	pick := 0
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("Which database?").
			Description("gluon found these by reading the project. It will not choose for you.").
			Options(opts...).
			Value(&pick),
	)).Run()
	if err != nil {
		return nil, err
	}
	if pick < 0 {
		return nil, nil
	}
	return &det.Candidates[pick], nil
}

func askFromScratch(module string, det db.Detection) (config.Database, error) {
	d := config.Database{Module: module, Name: "primary"}

	driver := dsn.Postgres
	if len(det.Drivers) > 0 {
		driver = det.Drivers[0].Family
	}
	var host, port, user, dbname, file string
	port = "5432"

	driverOpts := []huh.Option[string]{
		huh.NewOption("postgres", dsn.Postgres),
		huh.NewOption("mysql", dsn.MySQL),
		huh.NewOption("sqlite", dsn.SQLite),
		huh.NewOption("sqlserver", dsn.SQLServer),
	}
	desc := "gluon opens this with the driver your project already requires."
	if len(det.Drivers) > 0 {
		desc = "your go.mod has " + det.Drivers[0].Module
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Driver").Description(desc).
				Options(driverOpts...).Value(&driver),
		),
		huh.NewGroup(
			huh.NewInput().Title("Database file").
				Description("The path to the SQLite file, relative to this project.").
				Placeholder("./data/app.db").Value(&file).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("a path is required")
					}
					p := s
					if !filepath.IsAbs(p) {
						p = filepath.Join(".", s)
					}
					// The header check, in the one place somebody can still fix
					// it. "that file exists but is not a database" is a far
					// better answer now than a driver error later.
					if _, err := dsn.Parse(p, "."); err != nil {
						return errors.New("that is not a SQLite database (checked its header)")
					}
					return nil
				}),
		).WithHideFunc(func() bool { return driver != dsn.SQLite }),
		huh.NewGroup(
			huh.NewInput().Title("Host").Placeholder("127.0.0.1").Value(&host),
			huh.NewInput().Title("Port").Value(&port).
				Validate(func(s string) error {
					if _, err := strconv.Atoi(strings.TrimSpace(s)); err != nil {
						return errors.New("a port is a number")
					}
					return nil
				}),
			huh.NewInput().Title("User").Value(&user),
			huh.NewInput().Title("Database").Placeholder("acme_dev").Value(&dbname).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("a database name is required")
					}
					return nil
				}),
		).WithHideFunc(func() bool { return driver == dsn.SQLite }),
	)
	if err := form.Run(); err != nil {
		return config.Database{}, err
	}

	d.Driver = driver
	if driver == dsn.SQLite {
		abs, err := filepath.Abs(strings.TrimSpace(file))
		if err != nil {
			abs = strings.TrimSpace(file)
		}
		d.File = abs
		return d, nil
	}
	d.Host = strings.TrimSpace(host)
	if d.Host == "" {
		d.Host = "127.0.0.1"
	}
	d.Port, _ = strconv.Atoi(strings.TrimSpace(port))
	d.User = strings.TrimSpace(user)
	d.DBName = strings.TrimSpace(dbname)
	return finishEntry(d, det)
}

// finishEntry asks where the password comes from.
//
// The options are a variable, a file, or nothing — and there is no fourth
// option that takes a value. A password typed here would end up in a config
// file, which is precisely what this feature exists not to do, and the
// description says so where somebody will actually read it.
func finishEntry(d config.Database, det db.Detection) (config.Database, error) {
	if d.File != "" || d.DSNEnv != "" || (d.DSNFile != "" && d.DSNKey != "") {
		// A SQLite path carries no secret, and a reference already names one.
		return d, nil
	}

	const (
		fromEnv  = "env"
		fromFile = "file"
		none     = "none"
	)
	choice := none
	envName := "PGPASSWORD"
	fileName := ""
	if det.Chosen != nil && det.Chosen.Secret.Env != "" {
		choice, envName = fromEnv, det.Chosen.Secret.Env
	}

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Where does the password come from?").
				Description("gluon does not store passwords. It stores where to find one,\n"+
					"and reads it at the moment a query runs.").
				Options(
					huh.NewOption("An environment variable", fromEnv),
					huh.NewOption("A file", fromFile),
					huh.NewOption("There is no password", none),
				).Value(&choice),
		),
		huh.NewGroup(
			huh.NewInput().Title("Variable name").Placeholder("PGPASSWORD").Value(&envName),
		).WithHideFunc(func() bool { return choice != fromEnv }),
		huh.NewGroup(
			huh.NewInput().Title("File").Placeholder("~/.pgpass").Value(&fileName),
		).WithHideFunc(func() bool { return choice != fromFile }),
	).Run()
	if err != nil {
		return config.Database{}, err
	}

	switch choice {
	case fromEnv:
		d.PassEnv = strings.TrimSpace(envName)
	case fromFile:
		d.PassFile = strings.TrimSpace(fileName)
	}
	return d, nil
}

// confirmWrite asks where the entry goes, and shows the exact bytes first.
//
// The block on screen is config.Stanza's output, which is also what
// AppendDatabase writes — one string, so a confirmation cannot promise one
// thing and do another.
func confirmWrite(entry config.Database, root string, chosen bool, th ui.Theme) (config.Database, string, error) {
	target := config.File()
	if chosen {
		target = config.File()
	}

	const (
		toGlobal = "global"
		toLocal  = "local"
	)
	where := toGlobal
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Where should this go?").
			Options(
				huh.NewOption("~/.config/gluon/config.toml — private, nothing is added to the repository", toGlobal),
				huh.NewOption("./gluon.toml — travels with the project, and holds no secret", toLocal),
			).Value(&where),
	)).Run()
	if err != nil {
		return entry, "", err
	}
	if where == toLocal {
		target = filepath.Join(root, config.ProjectFile)
		entry = forTarget(entry, target)
	}

	ok := true
	block := config.Stanza(entry)
	err = huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Write this?").
			Description("append to " + shortenHome(target) + "\n\n" + block).
			Affirmative("Write it").Negative("Cancel").
			Value(&ok),
	)).Run()
	if err != nil {
		return entry, "", err
	}
	if !ok {
		return entry, "", fmt.Errorf("cancelled")
	}
	return entry, target, nil
}
