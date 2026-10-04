// Package redact decides, from a value's bytes alone, whether it carries a
// secret — and produces the form that is safe to put on a screen.
//
// It exists so there is one definition of "secret" rather than two. The
// database layer needs it to print a connection string; :env and :conf need it
// to print a value out of the environment or a config file. A second copy would
// drift, and the copy nobody exercises against real connection strings would be
// the one on the screen.
//
// The rule the package is built around is invariant 23: a value is a secret by
// shape, never by the name of the key holding it. Nothing here is ever given
// that name — it cannot be influenced by it, rather than merely choosing not to
// be. The one function that takes a name, Param, is given a *connection
// string's own query parameter*, which is grammar and not a guess: libpq
// documents `password=` and sqlcipher documents `_pragma_key=`, so the name
// there is part of the value's structure.
//
// It has no dependencies, which is what lets internal/dsn import it. The
// combined test — "is this a connection string, and if not is it a token" —
// lives in internal/dsn as RedactValue, because answering the first half means
// parsing, and a package dsn imports cannot import dsn back.
package redact

import (
	"encoding/base64"
	"strings"
)

// Mask is what a hidden value shows as. Everything that redacts uses this one
// spelling so a reader learns it once.
const Mask = "***"

// MinSecret is the shortest tail worth hiding behind a preserved prefix.
//
// The number comes from internal/eval/live.go, where it bounds the last-resort
// scrubber for the same reason it bounds this one: below it there is not enough
// left to hide. `Bearer abc` redacted to `Bearer ***` claims a protection it
// does not provide, since three characters is not a secret. So below the floor
// the whole value goes, which is what the spec means by never showing a secret
// partially.
const MinSecret = 6

// Param reports whether a connection-string query parameter is itself a secret.
//
// See the package comment: this is a closed set of names out of two documented
// grammars, not a heuristic over arbitrary keys.
func Param(k string) bool {
	switch strings.ToLower(k) {
	case "password", "passwd", "pwd", "_pragma_key", "_key", "key", "sslpassword":
		return true
	}
	return false
}

// prefixes are the credential formats that announce themselves.
//
// Each entry is a vendor's own documented prefix, so matching one is reading a
// format rather than guessing at entropy. Entropy analysis is deliberately not
// here: it calls every base64 payload and every content hash a secret, and a
// redactor with that false-positive rate is one people learn to ignore.
//
// The prefix itself is not the secret — it says which service to go and rotate
// — so it survives redaction whenever what follows it is long enough to hide.
//
// Public counterparts are left out on purpose. Stripe's pk_live_ is
// publishable, a Twilio account SID is printed in its own dashboard, and a
// certificate is the half of a key pair meant to be handed out. Redacting one
// hides nothing and teaches the reader that the marker means little.
var prefixes = []string{
	"sk-",         // OpenAI, and several that copied it
	"sk_live_",    // Stripe secret
	"sk_test_",    //
	"rk_live_",    // Stripe restricted
	"ghp_",        // GitHub personal access token
	"gho_",        // GitHub OAuth
	"ghu_",        // GitHub user-to-server
	"ghs_",        // GitHub server-to-server
	"ghr_",        // GitHub refresh
	"github_pat_", //
	"glpat-",      // GitLab
	"xoxb-",       // Slack bot
	"xoxp-",       // Slack user
	"xoxa-",       //
	"xoxr-",       //
	"xapp-",       // Slack app-level
	"AKIA",        // AWS access key id
	"ASIA",        // AWS temporary access key id
	"AIza",        // Google API key
	"SG.",         // SendGrid
	"npm_",        // npm
	"dop_v1_",     // DigitalOcean
	"hf_",         // Hugging Face
	"shpat_",      // Shopify
	"sq0atp-",     // Square
}

// authSchemes are the HTTP Authorization schemes whose credential follows the
// scheme name. The scheme is not the secret and is worth keeping: "Basic" and
// "Bearer" failing look nothing alike from the server's side.
var authSchemes = []string{"bearer", "basic"}

// Secret reports whether v carries a credential, judged on its bytes alone.
//
// It answers only for values with no connection-string structure. Ask
// dsn.RedactValue for the general question; this is the half of it that needs
// no parser.
func Secret(v string) bool {
	_, redacted := Value(v)
	return redacted
}

// Value returns the form of v that is safe to display, and whether anything was
// hidden.
//
// A value that is not a secret comes back unchanged, which matters as much as
// the other direction: a redactor that hides the harmless is one people work
// around, and `PASSWORD_HINT=ask the team` is not a password.
//
// Redaction preserves every part that is not the secret, and never shows part
// of the secret itself. There is no middle setting where a few characters leak.
func Value(v string) (string, bool) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return v, false
	}

	// A PEM private key, which arrives in an environment variable as one line
	// with escaped newlines. Its own header names the key type and the rest is
	// the key, so there is nothing here worth preserving.
	if i := strings.Index(trimmed, "-----BEGIN "); i >= 0 &&
		strings.Contains(trimmed[i:], "PRIVATE KEY") {
		return Mask, true
	}

	// An Authorization header value. The scheme is a fact about the protocol,
	// so it stays; the credential after it must actually look like one, or
	// "Basic understanding of the parser" would redact as a token.
	if scheme, cred, ok := strings.Cut(trimmed, " "); ok && isAuthScheme(scheme) {
		if cred = strings.TrimSpace(cred); isCredential(cred) {
			if len(cred) < MinSecret {
				return Mask, true
			}
			return scheme + " " + Mask, true
		}
	}

	// A JWT. Three base64url segments whose first decodes to a JOSE header is
	// the format's own definition, so this recognises a JWT rather than
	// anything that happens to contain two dots.
	if isJWT(trimmed) {
		return Mask, true
	}

	// A vendor-prefixed token.
	for _, p := range prefixes {
		if !strings.HasPrefix(trimmed, p) {
			continue
		}
		rest := trimmed[len(p):]
		if len(rest) < MinSecret || !isCredential(rest) {
			// Not enough behind the prefix to be a credential, or not the
			// shape of one — "sk-1" is somebody's note, and "hf_ the model"
			// is prose. Keep looking rather than redacting a value the prefix
			// only coincidentally matched.
			continue
		}
		return p + Mask, true
	}

	return v, false
}

func isAuthScheme(s string) bool {
	s = strings.ToLower(s)
	for _, a := range authSchemes {
		if s == a {
			return true
		}
	}
	return false
}

// isCredential reports whether s has the shape of an opaque token: one run of
// the characters base64, base64url and hex all draw from. It is what separates
// a credential from a sentence.
func isCredential(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '+', r == '/', r == '=', r == '~':
		default:
			return false
		}
	}
	return true
}

// isJWT reports whether s is a JSON Web Token, by decoding its header.
//
// The three-segment shape alone is not enough — a version string and a file
// path both have dots — so the header must actually decode to JSON naming an
// algorithm, which is what RFC 7519 requires of every JWT.
func isJWT(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return false
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	h := string(header)
	return strings.HasPrefix(strings.TrimSpace(h), "{") && strings.Contains(h, "\"alg\"")
}
