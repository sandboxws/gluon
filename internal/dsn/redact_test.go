package dsn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRedactValueCorpus is the one place the whole secret test is exercised.
//
// It covers the four parseable DSN shapes, the token formats that have no
// structure to preserve, and the values that must survive untouched — because a
// redactor that hides ordinary configuration is one people route around, and
// then the connection strings go with it.
func TestRedactValueCorpus(t *testing.T) {
	// A real SQLite file, so the file shape is matched on its magic bytes
	// rather than on its name — which is the whole point of that branch.
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "app.db")
	if err := os.WriteFile(dbFile, []byte("SQLite format 3\x00 and then some"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		in   string
		// want is the exact display form when it is worth pinning, or "" to
		// assert only that the value was redacted.
		want     string
		redacted bool
		// keep is what must survive redaction: the parts that are not secret.
		keep []string
		// gone is what must not appear. The password itself, always.
		gone []string
	}{
		// The four shapes.
		{
			name: "url", in: "postgres://ada:hunter2@db.internal:5432/app?sslmode=require",
			redacted: true,
			keep:     []string{"postgres", "ada", "db.internal", "5432", "app", "sslmode=require"},
			gone:     []string{"hunter2"},
		}, {
			name: "mysql-go", in: "root:s3cr3tpw@tcp(127.0.0.1:3306)/shop?parseTime=true",
			redacted: true,
			keep:     []string{"root", "127.0.0.1", "3306", "shop", "parseTime=true"},
			gone:     []string{"s3cr3tpw"},
		}, {
			name: "keyvalue", in: "host=db.internal port=5432 user=ada password=hunter2 dbname=app",
			redacted: true,
			keep:     []string{"db.internal", "5432", "ada", "app"},
			gone:     []string{"hunter2"},
		}, {
			// The file shape carries no password, so it passes through whole.
			// A sqlcipher key rides in a query string, which is the URL form
			// below — the one case where a sqlite value has anything to hide.
			name: "file", in: dbFile,
			want: dbFile, redacted: false,
		}, {
			// Relative, it resolves against dir to be recognised — and is
			// still shown as written, because resolving it hid nothing.
			name: "relative file", in: "app.db",
			want: "app.db", redacted: false,
		}, {
			name: "sqlite url with a cipher key", in: "sqlite:///data/app.db?_pragma_key=opensesame",
			redacted: true,
			keep:     []string{"/data/app.db", "_pragma_key"},
			gone:     []string{"opensesame"},
		},

		// A DSN with no password is not a secret, and saying so is the point:
		// "no password" and "password hidden" are different answers.
		{
			name: "url without a password", in: "postgres://db.internal:5432/app",
			want: "postgres://db.internal:5432/app", redacted: false,
		},

		// Tokens, which have no structure to preserve beyond the part that
		// says which service to go and rotate.
		{
			name: "bearer", in: "Bearer sq7Kd0aMzX9vLpQr2TfY",
			want: "Bearer ***", redacted: true, gone: []string{"sq7Kd0aMzX9vLpQr2TfY"},
		}, {
			name: "basic", in: "Basic YWRhOmh1bnRlcjI=",
			want: "Basic ***", redacted: true, gone: []string{"YWRhOmh1bnRlcjI="},
		}, {
			name: "openai key", in: "sk-proj-9fKq2mVb7Xt0RzLdEwNj",
			want: "sk-***", redacted: true, gone: []string{"proj-9fKq2mVb7Xt0RzLdEwNj"},
		}, {
			name: "github pat", in: "ghp_16C7e42F292c6912E7710c838347Ae178B4a",
			want: "ghp_***", redacted: true,
		}, {
			name: "aws access key", in: "AKIAIOSFODNN7EXAMPLE",
			want: "AKIA***", redacted: true,
		}, {
			name: "slack bot token", in: "xoxb-2334-4567-abcdefghijklmnop",
			want: "xoxb-***", redacted: true,
		}, {
			name: "jwt", in: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
				"eyJzdWIiOiIxMjM0In0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
			want: "***", redacted: true,
		}, {
			name: "pem private key",
			in:   "-----BEGIN RSA PRIVATE KEY-----\\nMIIEpAIBAAKCAQEA\\n-----END RSA PRIVATE KEY-----",
			want: "***", redacted: true, gone: []string{"MIIEpAIBAAKCAQEA"},
		},

		// The minSecret floor. There is nothing behind the prefix worth
		// hiding, so the whole value goes rather than a form that shows most
		// of a short secret while claiming to protect it.
		{
			name: "short bearer", in: "Bearer ab1",
			want: "***", redacted: true, gone: []string{"ab1"},
		}, {
			name: "short prefixed value is not a token", in: "sk-1",
			want: "sk-1", redacted: false,
		},

		// Not secrets. Each of these is a value somebody actually keeps in an
		// environment, and hiding it would cost more than it saves.
		{name: "a hint", in: "ask the team", want: "ask the team"},
		{name: "prose beginning Basic", in: "Basic auth is off in dev", want: "Basic auth is off in dev"},
		{name: "a log level", in: "debug", want: "debug"},
		{name: "an http url", in: "https://api.example.com/v1", want: "https://api.example.com/v1"},
		{name: "a path", in: "/var/log/app.log", want: "/var/log/app.log"},
		{name: "a version", in: "1.2.3", want: "1.2.3"},
		{name: "a port", in: "8080", want: "8080"},
		{name: "empty", in: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, redacted := RedactValue(tc.in, dir)
			if redacted != tc.redacted {
				t.Fatalf("RedactValue(%q) redacted=%v, want %v (got %q)",
					tc.in, redacted, tc.redacted, got)
			}
			if tc.want != "" && got != tc.want {
				t.Errorf("RedactValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for _, k := range tc.keep {
				if !strings.Contains(got, k) {
					t.Errorf("RedactValue(%q) = %q, dropped %q — a redacted value must stay diagnosable",
						tc.in, got, k)
				}
			}
			for _, g := range tc.gone {
				if strings.Contains(got, g) {
					t.Errorf("RedactValue(%q) = %q, leaked %q", tc.in, got, g)
				}
			}
		})
	}
}

// TestRedactValueNeverPartiallyShowsASecret is the property behind the
// minSecret floor, stated once rather than per case: whatever the shape, no
// prefix of the secret itself reaches the output.
func TestRedactValueNeverPartiallyShowsASecret(t *testing.T) {
	secrets := map[string]string{
		"postgres://ada:hunter2@h:5432/app":  "hunter2",
		"root:s3cr3tpw@tcp(h:3306)/shop":     "s3cr3tpw",
		"host=h user=ada password=hunter2":   "hunter2",
		"Bearer sq7Kd0aMzX9vLpQr2TfY":        "sq7Kd0aMzX9vLpQr2TfY",
		"ghp_16C7e42F292c6912E7710c838347Ae": "16C7e42F292c6912E7710c838347Ae",
	}
	for in, secret := range secrets {
		got, _ := RedactValue(in, "")
		// Every prefix of the secret from minSecret up must be absent too, not
		// only the whole of it.
		for n := 6; n <= len(secret); n++ {
			if strings.Contains(got, secret[:n]) {
				t.Errorf("RedactValue(%q) = %q shows %q, a prefix of the secret",
					in, got, secret[:n])
				break
			}
		}
	}
}

// TestRedactValueIsNotGivenTheKeyName is invariant 23 as a compile-time fact
// rather than a promise: there is no parameter to pass a name in.
//
// The behavioural half is the pair below — the same two values a `NOTE` and a
// `PASSWORD_HINT` would hold in :env — decided identically here, where no name
// exists at all.
func TestRedactValueIsNotGivenTheKeyName(t *testing.T) {
	if got, redacted := RedactValue("postgres://ada:hunter2@h:5432/app", ""); !redacted ||
		strings.Contains(got, "hunter2") {
		t.Errorf("a connection string was not redacted: %q", got)
	}
	if got, redacted := RedactValue("ask the team", ""); redacted || got != "ask the team" {
		t.Errorf("plain text was redacted: %q", got)
	}
}
