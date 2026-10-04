package db

import (
	"go/parser"
	"strings"
	"testing"
)

// TestInjectDryRunGoesToTheBase is the whole reason :sql is Go and not a
// template: DryRun is a property of the session the statement is built on, so
// appending it to the end of the chain would be too late.
func TestInjectDryRunGoesToTheBase(t *testing.T) {
	const session = ".Session(&gorm.Session{DryRun: true})"
	cases := map[string]string{
		"db.Where(\"a = ?\", 1).Find(&users)": "db" + session + ".Where(\"a = ?\", 1).Find(&users)",
		"db.Find(&users)":                     "db" + session + ".Find(&users)",
		"db":                                  "db" + session,
		"app.DB.Model(&U{}).Count(&n)":        "app.DB" + session + ".Model(&U{}).Count(&n)",
		"(db).Find(&u)":                       "(db)" + session + ".Find(&u)",
		// The base is the receiver of the innermost call, not the deepest
		// identifier: wrapping `app` here would compile and call the wrong
		// thing.
		"app.DB.Session(nil).Find(&u)": "app.DB" + session + ".Session(nil).Find(&u)",
		// A function call can be the receiver too.
		"getDB().Find(&u)": "getDB()" + session + ".Find(&u)",
	}
	for in, want := range cases {
		got, err := injectDryRun(in)
		if err != nil {
			t.Errorf("injectDryRun(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("injectDryRun(%q)\n got %s\nwant %s", in, got, want)
		}
	}
}

// TestInjectDryRunRejectsNonGo, with a message naming the command rather than
// letting the failure surface later as a build error in generated source.
func TestInjectDryRunRejectsNonGo(t *testing.T) {
	if _, err := injectDryRun("db.Where(("); err == nil {
		t.Fatal("unbalanced parens were accepted")
	} else if !strings.Contains(err.Error(), ":sql") {
		t.Errorf("error should name the command, got %q", err)
	}
}

// TestGormRewriteParses guards against a command that only fails when someone
// runs it.
func TestGormRewriteParses(t *testing.T) {
	src, err := gormRewrite("db.Where(\"a = ?\", 1).Find(&users)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("produced source that does not parse: %v\n%s", err, src)
	}
	if !strings.Contains(src, "DryRun") {
		t.Error("DryRun was not injected")
	}
	// Arguments must not be interpolated into the SQL: a query with its
	// parameters already substituted is not the query that runs.
	if !strings.Contains(src, "Statement.Vars") {
		t.Error("the bound arguments are not reported")
	}
}

func TestGormRewriteNeedsAnArgument(t *testing.T) {
	if _, err := gormRewrite("   "); err == nil {
		t.Fatal("empty argument accepted")
	}
}
