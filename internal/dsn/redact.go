package dsn

import "github.com/sandboxws/gluon/internal/redact"

// RedactValue is the whole shape-based secret test, for a value out of an
// environment variable or a configuration file.
//
// It is the one definition of "secret" gluon has, and it is the same one :db
// depends on: a connection string is redacted by the parser that opens it, so
// every DSN test in this package is also a test of what :env and :conf show. A
// value with no connection-string structure falls through to the token shapes
// in internal/redact.
//
// It lives here rather than in internal/redact because the first half of the
// question is "does this parse", and internal/redact is what internal/dsn
// imports — a package cannot import the one that imports it. The split is
// therefore mechanical: redact holds everything that needs no parser, and this
// is the seam where the two halves meet.
//
// The name of the key holding v is deliberately not a parameter, for the same
// reason Parse does not take one (invariant 23). dir is only what Parse needs:
// the directory v was found in, so a relative SQLite path resolves against the
// file that named it.
//
// The second return says whether anything was hidden. Callers need it because a
// value that *is* the mask and a value that was replaced by it are different
// facts, and a display that showed both as *** could not say which.
func RedactValue(v, dir string) (string, bool) {
	if d, err := Parse(v, dir); err == nil {
		// Only a password or a secret parameter is hidden, and a value with
		// neither is shown as it was written. Comparing the display form with
		// the input would call a rewrite a redaction: a relative SQLite path
		// comes back resolved against dir, and nothing about it was secret.
		if !d.hides() {
			return v, false
		}
		return d.Redacted(), true
	}
	return redact.Value(v)
}
