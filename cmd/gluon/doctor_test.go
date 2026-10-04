package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/ui"
)

// captureStdout runs f with stdout redirected, and returns what it printed.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	f()
	w.Close()
	os.Stdout = saved
	return <-done
}

// TestDoctorPlainThemeEmitsNoEscapes is the pipe contract for the report: with
// colour off, nothing may reach stdout that a script would have to strip.
func TestDoctorPlainThemeEmitsNoEscapes(t *testing.T) {
	rep := collectDoctor(".")
	out := captureStdout(t, func() { renderDoctor(rep, ui.Plain()) })
	if strings.Contains(out, "\x1b") {
		t.Errorf("plain doctor report contains escape codes:\n%q", out)
	}
	if !strings.Contains(out, "gluon") {
		t.Errorf("report looks empty:\n%s", out)
	}
}

// TestDoctorRendersPartialReports covers the three paths that only appear when
// something is wrong, since those are exactly the ones nobody runs by hand.
func TestDoctorRendersPartialReports(t *testing.T) {
	cases := map[string]*doctorJSON{
		"no config, no host": {
			Version: version,
			Config:  &doctorConfigJSON{},
			Host:    &doctorHostJSON{Error: "not in a module"},
		},
		"broken config": {
			Version: version,
			Config:  &doctorConfigJSON{Path: "/x/config.toml", Error: "unknown setting nope"},
			Host:    &doctorHostJSON{Error: "not in a module"},
		},
		"host index failed": {
			Version: version,
			Config:  &doctorConfigJSON{},
			Host:    &doctorHostJSON{Module: "example.com/m", IndexError: "permission denied"},
		},
		"gatekeeper could not be measured": {
			Version:    version,
			Gatekeeper: &doctorGatekeeperJSON{Status: "unknown", Error: "no go toolchain"},
			Config:     &doctorConfigJSON{},
			Host:       &doctorHostJSON{Error: "not in a module"},
		},
	}
	for name, rep := range cases {
		t.Run(name, func(t *testing.T) {
			out := captureStdout(t, func() { renderDoctor(rep, ui.Plain()) })
			if out == "" {
				t.Fatal("rendered nothing")
			}
			if strings.Contains(out, "%!") {
				t.Errorf("format verb leaked into output:\n%s", out)
			}
		})
	}
}

// TestDoctorJSONCarriesTheSameAnswers keeps -json from drifting behind the text
// form: both are rendered from one struct, and this is what says so.
func TestDoctorJSONCarriesTheSameAnswers(t *testing.T) {
	// The report reads the user's config; a test of its shape must not.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	rep := collectDoctor(".")

	blob, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ok", "version", "go", "goToolchain", "goFlags", "editor", "config", "host"} {
		if _, ok := back[key]; !ok {
			t.Errorf("doctor -json is missing %q", key)
		}
	}
	// gluon's own repo is a module, so the host half must be populated.
	host, _ := back["host"].(map[string]any)
	if host["module"] != "github.com/sandboxws/gluon" {
		t.Errorf("host.module = %v (host.error = %v), want the gluon module path", host["module"], host["error"])
	}
	if host["importable"] == nil {
		t.Error("host.importable is absent")
	}
}

// TestDoctorDatabaseEnvelopeCannotCarryASecret.
//
// The -json envelope is what a script pipes to jq, and it is built from the
// same struct the text form renders — so a value that reached one would reach
// both. dsnRef names a location and dsnResolves is a boolean: "does it work" is
// the answer worth having, and "here it is" would be a password in a pipeline.
func TestDoctorDatabaseEnvelopeCannotCarryASecret(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go.mod": "module example.com/api\n\ngo 1.25.0\n\nrequire github.com/lib/pq v1.10.9\n",
		".env":   "DATABASE_URL=postgres://app:hunter2@127.0.0.1:5432/acme\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rep := collectDoctor(root)
	if rep.Database == nil {
		t.Fatal("no database section")
	}
	blob, err := marshalIndent(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, "hunter2") {
		t.Fatalf("the doctor envelope carries the password:\n%s", blob)
	}
	if !strings.Contains(blob, "***") {
		t.Errorf("the detected DSN is not shown redacted:\n%s", blob)
	}
}

// TestDoctorReportsADatabaseWithoutAGoModule.
//
// collectDoctor used to return the moment host.Detect failed. A directory with
// a compose file and a .env has a database whether or not it has a go.mod, and
// stopping there answered "no database" to a question nobody asked.
func TestDoctorReportsADatabaseWithoutAGoModule(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "services:\n  db:\n    image: postgres:16\n    ports: [\"5432:5432\"]\n" +
		"    environment:\n      POSTGRES_DB: acme\n"
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := collectDoctor(root)
	if rep.Host == nil || rep.Host.Error == "" {
		t.Fatal("expected the host section to report no module")
	}
	if rep.Database == nil || len(rep.Database.Detected) == 0 {
		t.Fatalf("no database was reported outside a module: %+v", rep.Database)
	}
	// Not finding a module, and not having a database configured, are facts
	// about the project rather than something gluon depends on being broken.
	if !rep.OK {
		t.Error("OK went false for a project that is merely not configured")
	}
}

// TestDoctorReportsTheValueLimitsOnlyWhenSet.
//
// GLUON_MAX_ITEMS and GLUON_MAX_DEPTH are how value.items and value.depth reach
// the child, which means one exported in the user's own shell is in force for
// every session — over the setting that names the same bound. That is the
// confusion doctor exists to end, so it reports them.
//
// The -json half is add-only: the fields are omitempty, so a caller that has
// never seen them gets the object it always got.
func TestDoctorReportsTheValueLimitsOnlyWhenSet(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		os.Unsetenv("GLUON_MAX_ITEMS")
		os.Unsetenv("GLUON_MAX_DEPTH")

		rep := collectDoctor(".")
		if rep.MaxItems != "" || rep.MaxDepth != "" {
			t.Errorf("reported (%q, %q) with neither variable set", rep.MaxItems, rep.MaxDepth)
		}

		blob, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		var back map[string]any
		if err := json.Unmarshal(blob, &back); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"maxItems", "maxDepth"} {
			if _, ok := back[key]; ok {
				t.Errorf("doctor -json carries %q when it is not set; the envelope must be add-only", key)
			}
		}

		out := captureStdout(t, func() { renderDoctor(rep, ui.Plain()) })
		if strings.Contains(out, "GLUON_MAX_ITEMS") {
			t.Errorf("the report names a variable nothing set:\n%s", out)
		}
	})

	t.Run("set", func(t *testing.T) {
		t.Setenv("GLUON_MAX_ITEMS", "500")
		t.Setenv("GLUON_MAX_DEPTH", "12")

		rep := collectDoctor(".")
		if rep.MaxItems != "500" || rep.MaxDepth != "12" {
			t.Errorf("reported (%q, %q), want (\"500\", \"12\")", rep.MaxItems, rep.MaxDepth)
		}

		blob, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		var back map[string]any
		if err := json.Unmarshal(blob, &back); err != nil {
			t.Fatal(err)
		}
		if back["maxItems"] != "500" || back["maxDepth"] != "12" {
			t.Errorf("doctor -json carries (%v, %v), want (500, 12)", back["maxItems"], back["maxDepth"])
		}

		// The text form and the envelope are rendered from one struct, so a
		// value in one is a value in both.
		out := captureStdout(t, func() { renderDoctor(rep, ui.Plain()) })
		for _, want := range []string{"GLUON_MAX_ITEMS", "500", "GLUON_MAX_DEPTH", "12", "value.items", "value.depth"} {
			if !strings.Contains(out, want) {
				t.Errorf("the report does not mention %q:\n%s", want, out)
			}
		}
	})
}
