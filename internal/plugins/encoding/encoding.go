// Package encoding holds the plugins for serialization libraries gluon does
// not link.
//
// It is separate from plugins/stdlib for the reason the whole plugin model
// exists: the generated child program is strictly standard library, and gluon
// links none of the libraries its plugins describe. So :xml and :csv are stdlib
// plugins, always active because the standard library is always there, and
// everything here activates only when the session's own build list already has
// the module — which is also the only way these commands could work, since the
// encoder runs in the child against the user's own dependency.
//
// The answer each command gives is the same shape: what the value becomes under
// that encoder, tags and all. That is a question the value printer cannot
// answer, because it reports Go field names and the meaning lives in the tag.
package encoding

import (
	"fmt"
	"strings"
)

// needsArg is the usage line a bare invocation gets. Every command here takes a
// required <exp>, so this is what stands between the user and a build error
// naming gluon rather than their line.
func needsArg(name, example string) error {
	return fmt.Errorf("usage: %s <expression>   e.g. %s %s", name, name, example)
}

// blank reports an argument that is only whitespace.
func blank(arg string) bool { return strings.TrimSpace(arg) == "" }
