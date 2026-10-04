// Package gover reports the installed toolchain's release.
//
// It has no dependency but os/exec, so anything may import it — which is the
// point: whichever package next needs to synthesize a go directive gets this
// answer rather than a second copy of a twelve-line function, and cannot be
// drawn into an import cycle for it.
package gover

import (
	"os/exec"
	"regexp"
	"strings"
)

var minorRe = regexp.MustCompile(`go(\d+\.\d+)`)

// Fallback is what Minor answers when the toolchain will not say.
//
// A number rather than an error, because every caller is synthesizing a go
// directive and a directive is not optional. It is deliberately behind the
// current release: a directive below the toolchain builds, and one above it
// does not.
const Fallback = "1.25"

// Minor is the installed toolchain's major.minor, for a synthesized go
// directive.
//
// It comes from `go env GOVERSION`, never runtime.Version() — invariant 4. The
// binary that built gluon is not necessarily the one that will build what
// gluon writes, and a directive taken from the wrong one names a release the
// user does not have.
func Minor() string {
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return Fallback
	}
	if m := minorRe.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
		return m[1]
	}
	return Fallback
}
