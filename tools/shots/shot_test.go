package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestAShotReadsEveryStep(t *testing.T) {
	s, err := parseShot(strings.Join([]string{
		"# comment",
		"Shot values",
		"Alt the truth attached to a value",
		"Also values-2 the second",
		"Env SHOP_CONFIG=$HOST/config.yaml",
		"Config value.form line",
		"Promo 4:5 og",
		"Host shop",
		"Grid 72x20",
		`Type "\"héllo\"[1]"`,
		"Enter",
		"Wait Prompt",
		"Paste <<",
		"x := 1",
		"x + 1",
		">>",
		"Ctrl r",
		"Sleep 300ms",
		"Screenshot",
		"Screenshot values-2",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "values" || s.Cols != 72 || s.Rows != 20 || s.Host != "shop" || !reflect.DeepEqual(s.Promo, []string{"4:5", "og"}) {
		t.Errorf("shot = %+v", s)
	}
	var ops []string
	for _, st := range s.Steps {
		ops = append(ops, st.Op)
	}
	if want := []string{"Type", "Enter", "Wait", "Paste", "Ctrl", "Sleep", "Screenshot", "Screenshot"}; !reflect.DeepEqual(ops, want) {
		t.Errorf("ops = %v", ops)
	}
	if s.Steps[0].Arg != `"héllo"[1]` || s.Steps[3].Arg != "x := 1\nx + 1\n" {
		t.Errorf("args = %q, %q", s.Steps[0].Arg, s.Steps[3].Arg)
	}
	if s.Steps[6].Arg != "values" || s.Steps[7].Arg != "values-2" || !reflect.DeepEqual(s.Images(), []string{"values", "values-2"}) {
		t.Errorf("images = %v, screenshots %q %q", s.Images(), s.Steps[6].Arg, s.Steps[7].Arg)
	}
}

func TestABadShotSaysWhere(t *testing.T) {
	for _, bad := range []string{
		"Alt x\nScreenshot",
		"Shot a\nScreenshot",
		"Shot a\nAlt x\nType hi",
		"Shot a\nAlt x\nFly\nScreenshot",
		"Shot a\nAlt x\nWait forever\nScreenshot",
		"Shot a\nAlt x\nPromo 3:2\nScreenshot",
		"Shot a\nAlt x",
		"Shot a\nAlt x\nScreenshot b",
		"Shot a\nAlt x\nAlso b the other\nScreenshot",
		"Shot A\nAlt x\nScreenshot",
		"Shot a\nAlt x\nEnv 1X=2\nScreenshot",
	} {
		if _, err := parseShot(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
