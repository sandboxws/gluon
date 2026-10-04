package scratch

import "github.com/sandboxws/gluon/internal/gover"

// GoMinor is the installed toolchain's major.minor, for the synthesized go
// directive. It comes from `go env GOVERSION`, never runtime.Version(): the
// binary that built gluon is not necessarily the one that will build this.
//
// The implementation lives in internal/gover, which imports nothing of gluon's
// and so can serve any package without a cycle. This stays as the name every
// caller and invariant 4's own text already use.
func GoMinor() string { return gover.Minor() }
