package db

import (
	"go/parser"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/plugin"
)

func TestEntSchemaRewriteParses(t *testing.T) {
	src, err := entRewrite("migrate.Tables")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("produced source that does not parse: %v\n%s", err, src)
	}
	// Every field the listing reads is exported, which is the whole reason
	// schema introspection is possible where a query preview is not.
	for _, want := range []string{
		"__t.Name", "__t.Columns", "__t.PrimaryKey", "__t.ForeignKeys",
		"__c.Type.String()", "__c.Nullable", "__k.RefTable",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the listing does not read %s:\n%s", want, src)
		}
	}
}

// TestEntOffersNoQueryPreview. ent's builders expose no supported way to
// produce a statement without running it, so a command that looked like gorm's
// :sql could only be one that queried.
func TestEntOffersNoQueryPreview(t *testing.T) {
	for _, c := range (Ent{}).Commands() {
		if c.Name == ":sql" {
			t.Fatal("the ent plugin offers :sql, which for ent could only execute")
		}
		src, err := c.Rewrite("x")
		if err != nil {
			t.Fatalf("%s rejected a plain argument: %v", c.Name, err)
		}
		// A preview that ran would need a terminal method or a debug driver.
		for _, bad := range []string{".All(", ".First(", ".Only(", ".Count(", "dialect.Debug", ".Debug()"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s reaches the database through %s:\n%s", c.Name, bad, src)
			}
		}
	}
}

// TestEntPackageCommentNamesWhatWasLookedFor. "no dry-run exists" is a claim,
// and a claim with no method names in it cannot be rechecked against the next
// ent release.
func TestEntGuideNamesTheQuestionItCannotAnswer(t *testing.T) {
	g, ok := any(Ent{}).(plugin.Guider)
	if !ok {
		t.Fatal("the ent plugin has no guide, so :guide ent is a dead end")
	}
	guide := g.Guide()
	for _, want := range []string{
		":sql",      // the command it does not have
		"querySpec", // the unexported method that would have been it
		"sqlQuery",  // and the other one
		"Debug",     // the near miss
		"DryRun",    // what gorm has and ent does not
		":schema",   // what it offers instead
		"v0.14.6",   // the version the finding was made against
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("the guide does not mention %q", want)
		}
	}
}

// TestEntColumnFormatIsOneDefinition — gluon's copy and the generated source's
// must format through the same constant, or a width drifts and the listing
// stops lining up.
func TestEntColumnFormatIsOneDefinition(t *testing.T) {
	src, err := entRewrite("migrate.Tables")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, `"  %-24s %-12s %s"`) {
		t.Errorf("the generated source does not use entColumnFormat:\n%s", src)
	}
	got := entColumn("id", "int", "pk  not null")
	if !strings.HasPrefix(got, "  id") || !strings.Contains(got, "int") {
		t.Errorf("entColumn(%q) = %q", "id", got)
	}
}
