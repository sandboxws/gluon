package db

import (
	"os"
	"strings"
	"testing"
)

// TestGluonRequiresTheMaintainedYAMLModule.
//
// gopkg.in/yaml.v3 and go.yaml.in/yaml/v3 are the same source under two import
// paths, so a line that went back to the archived one would compile, pass every
// fixture, and read as correct — there is no behaviour to catch it. The module
// graph is the only place the difference shows, which is why it is asserted
// here rather than left to a reviewer.
//
// The old path may still appear in `go list -m all`: a test dependency of a
// dependency requires it. What matters is gluon's own requirement and what the
// binary links, and this is the first.
func TestGluonRequiresTheMaintainedYAMLModule(t *testing.T) {
	// Tests run in their package directory.
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	mod := string(data)
	if strings.Contains(mod, "gopkg.in/yaml.v3") {
		t.Error("go.mod requires gopkg.in/yaml.v3, whose upstream repository is archived")
	}
	if !strings.Contains(mod, "go.yaml.in/yaml/v3") {
		t.Error("go.mod does not require go.yaml.in/yaml/v3")
	}
}
