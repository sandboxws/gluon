package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/render"
)

// writeSumHost is a host directory carrying a go.sum, which is the case the
// session module has to reproduce: the replace covers the host itself, and
// nothing covers what the host requires.
func writeSumHost(t *testing.T, sum string) *host.Host {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/proj\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sum != "" {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(sum), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &host.Host{Dir: dir, Path: "example.com/proj", Go: "1.24"}
}

func TestWriteModCopiesTheHostsGoSum(t *testing.T) {
	const sum = "example.com/dep v1.2.3 h1:abc=\nexample.com/dep v1.2.3/go.mod h1:def=\n"
	// Attached, so writeMod never needs the toolchain to pick a directive.
	e := &Evaluator{dir: t.TempDir(), attached: writeSumHost(t, sum)}
	if err := e.writeMod(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(e.dir, "go.sum"))
	if err != nil {
		t.Fatalf("no go.sum beside the session's go.mod: %v", err)
	}
	if string(got) != sum {
		t.Errorf("go.sum = %q, want %q", got, sum)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "go.mod")); err != nil {
		t.Errorf("go.mod: %v", err)
	}
}

func TestWriteModLeavesNoSumForAStdlibOnlyHost(t *testing.T) {
	e := &Evaluator{dir: t.TempDir(), attached: writeSumHost(t, "")}
	if err := e.writeMod(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "go.sum")); !os.IsNotExist(err) {
		t.Errorf("go.sum exists for a host that has none (err = %v)", err)
	}
}

func TestWriteSumRemovesThePreviousHosts(t *testing.T) {
	// :use rewrites go.mod from scratch, so a sum left over from the module
	// before it would be the only piece of that module still in the temp dir.
	e := &Evaluator{dir: t.TempDir()}
	p := filepath.Join(e.dir, "go.sum")
	if err := os.WriteFile(p, []byte("example.com/old v1.0.0 h1:x=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.writeSum(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("stale go.sum survived (err = %v)", err)
	}
	// Removing one that is already gone is the ordinary case, not an error.
	if err := e.writeSum(nil); err != nil {
		t.Errorf("writeSum on a missing go.sum: %v", err)
	}
}

// TestAGottenModuleNamesItsPackages: after :get, a bare qualifier is the
// package the session required, not whichever package of that name the
// module cache holds — and only when it is the one such package, and never
// in place of the standard library's.
func TestAGottenModuleNamesItsPackages(t *testing.T) {
	got, std := readPackages(strings.Join([]string{
		"true errors errors",
		"true rand math/rand",
		"false postgres gorm.io/driver/postgres",
		"false errors github.com/pkg/errors",
		"false schema gorm.io/gorm/schema",
		"false schema entgo.io/ent/schema",
		"false main gorm.io/gorm/cmd/tool",
		"false pgconn github.com/jackc/pgx/v5/internal/pgconn",
		"false yaml go.yaml.in/yaml/v3",
		"",
	}, "\n"))
	e := &Evaluator{got: got, std: std}
	for q, want := range map[string]render.ImportSpec{
		"postgres": {Path: "gorm.io/driver/postgres"},
		"yaml":     {Name: "yaml", Path: "go.yaml.in/yaml/v3"},
		"errors":   {}, // the standard library's name stays the standard library's
		"schema":   {}, // two modules have one: goimports decides, as before
		"main":     {},
		"pgconn":   {}, // internal: nothing outside pgx may import it
		"nothing":  {},
	} {
		spec, ok := e.required(q)
		if ok != (want.Path != "") || spec != want {
			t.Errorf("required(%q) = %+v, %v; want %+v", q, spec, ok, want)
		}
	}
}
