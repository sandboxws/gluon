# Security

## Reporting

Report vulnerabilities through GitHub's private security advisories
(Security → Report a vulnerability on the repository). Best-effort response
from a single maintainer; please allow a reasonable window before public
disclosure.

## Trust model, in one paragraph each

**gluon executes what you type.** Every line is compiled with your own Go
toolchain and run with your permissions, like `go run`. There is no sandbox,
and evaluation replays the whole session, so side effects repeat. The
`gluon mcp` server exposes evaluation only behind an explicit `--eval` flag;
without it the server only answers type-level questions.

**Database detection reads, and only reads.** `:db` scans `.env` files,
compose files, config files and Go source *by shape* to say what the project
can reach — nothing is transmitted anywhere, nothing is opened until `:query`
runs, and detection never searches above the repository root.

**Passwords are unrepresentable, not just unprinted.** The DSN type keeps the
password in an unexported field whose `String()` is the redacted form, so
`%v`, logs and `-json` cannot leak it by accident. A `password` key in config
is refused with instructions to use `password_env`/`password_file` instead.
At query time the connection string travels to the child process only in an
environment variable — never in argv, never in the generated source, so it
cannot reach `:src`, `:save` output, or the build cache.

**The only things that go online** are `:get` (which is the asking), `:share`
(which shows you the program and asks before posting it to the Go Playground at
go.dev), `:http` and `:grpc` (which send the request you typed), and your own
code when it makes network calls.
