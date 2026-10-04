# Contributing to gluon

gluon is a single-maintainer project moving quickly. Contributions are
welcome, review is best-effort, and the design constraints below are real —
reading them first will save you a closed PR.

## Before proposing anything

- **ROADMAP.md is normative.** Its numbered invariants — deliberately not
  counted here, so this file cannot go stale — are the constraint list this
  codebase is built on; a change that moves one needs the invariant changed
  first, deliberately, in the same PR.
- **Check the "Permanently rejected" section of ROADMAP.md.** It is,
  explicitly, the will-not-merge list. An interpreter fast path, output
  diffing, `-buildmode=plugin`, automatic `go get` — each entry records why,
  usually with measurements. A proposal on that list needs new evidence, not
  a new vote.
- **No new dependencies without an issue first.** gluon deliberately links
  none of the libraries its plugins describe, and the generated child program
  stays strictly standard library.

## Working on it

Go 1.27 or newer must be on `PATH`: the module declares it, and gluon type-checks
with the `go/types` of the toolchain that built it.

```sh
just check            # fmt + vet + unit tests + integration tests
just test             # the fast tier: no toolchain needed
just test-integration # really invokes `go build` and runs the children
just install          # build and install the binary
```

The two test tiers exist because whole classes of bug — a write path that
silently discarded `CREATE TABLE`, an fd syscall that does not exist on
linux/arm64 — are only caught by really running the toolchain. If your change
touches `internal/eval`, `internal/render`, `internal/gluonrt` or
`internal/check`, run the integration tier before pushing; CI runs it on
Linux (amd64 and arm64) and macOS either way.

Two surfaces are frozen: the Plain rendering and the `-json` envelopes.
Scripts and pipes read them. New fields are add-only; new envelopes are fine;
changed bytes are not.

## Style

Match the code around you. Comments in this repo record *why* — constraints,
measurements, rejected alternatives — and the ROADMAP records anything a
future contributor would otherwise rediscover the hard way.
