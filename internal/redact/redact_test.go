package redact

import "testing"

// The corpus that matters lives in internal/dsn, where the whole test — DSN
// shapes and token shapes together — can be exercised as one. These cover the
// pieces this package owns alone.

// TestParamIsTheClosedSet. It is grammar out of two documented formats, not a
// heuristic over key names, so the set must not grow by accident.
func TestParamIsTheClosedSet(t *testing.T) {
	for _, k := range []string{"password", "PASSWORD", "Passwd", "pwd", "sslpassword",
		"_pragma_key", "_key", "key"} {
		if !Param(k) {
			t.Errorf("Param(%q) = false; a documented secret parameter would print", k)
		}
	}
	for _, k := range []string{"sslmode", "host", "dbname", "user", "keepalives",
		"application_name", "cache", "parseTime"} {
		if Param(k) {
			t.Errorf("Param(%q) = true; an ordinary parameter would be hidden", k)
		}
	}
}

// TestValuePreservesWhatIsNotTheSecret. The prefix says which service to go and
// rotate, which is the whole value of showing anything at all.
func TestValuePreservesWhatIsNotTheSecret(t *testing.T) {
	cases := map[string]string{
		"Bearer sq7Kd0aMzX9vLpQr2TfY":              "Bearer ***",
		"basic YWRhOmh1bnRlcjI=":                   "basic ***",
		"ghp_16C7e42F292c6912E7710c838347Ae178B4a": "ghp_***",
		"AKIAIOSFODNN7EXAMPLE":                     "AKIA***",
		"glpat-abcdefghijklmnop":                   "glpat-***",
	}
	for in, want := range cases {
		got, redacted := Value(in)
		if !redacted {
			t.Errorf("Value(%q) was not redacted", in)
		}
		if got != want {
			t.Errorf("Value(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestValueBelowTheFloorGoesWhole. There is nothing left to hide behind a
// preserved prefix, and a form that showed most of a short secret would claim a
// protection it does not provide.
func TestValueBelowTheFloorGoesWhole(t *testing.T) {
	got, redacted := Value("Bearer ab1")
	if !redacted || got != Mask {
		t.Errorf("Value(\"Bearer ab1\") = %q, %v; want %q redacted whole", got, redacted, Mask)
	}
	// The same shortness on a prefix means the prefix matched something that is
	// not a token at all, so nothing is hidden.
	if got, redacted := Value("sk-1"); redacted || got != "sk-1" {
		t.Errorf("Value(\"sk-1\") = %q, %v; want it passed through", got, redacted)
	}
}

// TestValueLeavesProseAlone. A redactor that hides ordinary configuration is one
// people route around — and the connection strings go with it.
func TestValueLeavesProseAlone(t *testing.T) {
	for _, in := range []string{
		"ask the team",
		"Basic auth is off in dev",
		"Bearer of bad news",
		"debug",
		"https://api.example.com/v1",
		"/var/log/app.log",
		"1.2.3",
		"",
		"   ",
	} {
		if got, redacted := Value(in); redacted {
			t.Errorf("Value(%q) = %q, redacted; it is not a secret", in, got)
		}
	}
}

// TestSecretAgreesWithValue. Two answers to one question is how a second
// definition of "secret" starts.
func TestSecretAgreesWithValue(t *testing.T) {
	for _, in := range []string{
		"Bearer sq7Kd0aMzX9vLpQr2TfY", "ask the team", "sk-1",
		"ghp_16C7e42F292c6912E7710c838347Ae178B4a", "debug",
	} {
		_, redacted := Value(in)
		if Secret(in) != redacted {
			t.Errorf("Secret(%q) = %v but Value redacted = %v", in, Secret(in), redacted)
		}
	}
}

// TestPEMPrivateKeyGoesWhole, and a certificate does not: one is the half of a
// key pair meant to be handed out.
func TestPEMPrivateKeyGoesWhole(t *testing.T) {
	key := "-----BEGIN RSA PRIVATE KEY-----\\nMIIEpAIBAAKCAQEA\\n-----END RSA PRIVATE KEY-----"
	if got, redacted := Value(key); !redacted || got != Mask {
		t.Errorf("a private key was not hidden whole: %q", got)
	}
	cert := "-----BEGIN CERTIFICATE-----\\nMIIDdTCCAl2gAwIBAgI\\n-----END CERTIFICATE-----"
	if got, redacted := Value(cert); redacted {
		t.Errorf("a certificate was redacted: %q", got)
	}
}
