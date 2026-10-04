package repl

import (
	"os"
	"strings"
	"testing"
)

func TestEditRoundTrip(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	t.Cleanup(func() { c.Close() })

	for _, src := range []string{"x := []int{3,1,2}", "len(x)"} {
		if res := c.Submit(src); res.Err {
			t.Fatalf("%s: %s", src, res.Out)
		}
	}

	res := c.Submit(":edit")
	if res.Edit == "" {
		t.Fatalf("no file to edit: %q", res.Out)
	}
	data, err := os.ReadFile(res.Edit)
	if err != nil {
		t.Fatal(err)
	}
	// The buffer is what the user typed, not what it rendered into.
	if strings.Contains(string(data), "__gluon") {
		t.Errorf("the edit buffer leaks gluon's machinery:\n%s", data)
	}

	// Edit it: change the slice.
	edited := strings.Replace(string(data), "[]int{3,1,2}", "[]int{1,2,3,4}", 1)
	if edited == string(data) {
		t.Fatalf("test did not edit anything:\n%s", data)
	}
	if err := os.WriteFile(res.Edit, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	if r := c.Reload(res.Edit); r.Err {
		t.Fatalf("reload: %s", r.Out)
	}
	if got := c.Submit("len(x)"); !strings.Contains(got.Out, "4") {
		t.Errorf("after reload got %q, want 4", got.Out)
	}
}
