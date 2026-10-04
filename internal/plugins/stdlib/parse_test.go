package stdlib

import (
	"go/parser"
)

// parsesAsExpr reports whether src is a Go expression. A plugin whose rewrite
// does not parse fails at the worst moment — when someone runs it — and the
// error names gluon rather than the plugin.
func parsesAsExpr(src string) error {
	_, err := parser.ParseExpr(src)
	return err
}
