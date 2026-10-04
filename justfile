# gluon — a REPL and scratch runner for Go

default: check

# Install to $GOBIN (~/go/bin)
install:
    go install ./cmd/gluon

build:
    go build -o dist/gluon ./cmd/gluon

# Fast tests: no toolchain needed, they assert on generated source
test:
    go test ./...

# Tests that actually invoke `go build` and run the result
test-integration:
    go test -tags=integration ./...

fmt:
    gofmt -w .

vet:
    go vet ./...

check: fmt vet test test-integration

# Start a REPL from source, without installing
repl:
    go run ./cmd/gluon

# Regenerate docs/ — the site GitHub Pages serves — from site/ and the registries
docs:
    go test ./cmd/gluon -run '^TestDocs$' -update -count=1

# Record the guides' transcripts: each site/sessions/*.gl run through a real session
docs-sessions *names:
    SESSIONS="{{names}}" go test -tags sessions ./internal/docgen -run '^TestRecordSessions$' -count=1 -timeout 30m

# Serve docs/ at http://localhost:8000/gluon/, the path GitHub Pages uses
docs-serve:
    mkdir -p "${TMPDIR:-/tmp}/gluon-pages"
    ln -sfn "{{justfile_directory()}}/docs" "${TMPDIR:-/tmp}/gluon-pages/gluon"
    python3 -m http.server -d "${TMPDIR:-/tmp}/gluon-pages" 8000

# Every local link and #fragment in the site and the README, offline
docs-links:
    lychee --offline --include-fragments --index-files index.html --no-progress docs README.md

# Screenshots: one fresh Ghostty window takes every shot in tapes/, or those named, in both palettes
shots *ids:
    cd tools/shots && go run . run {{ids}}

# The docs' WebPs, the promo PNGs and the og card, composed from the last shots
promo *ids:
    cd tools/shots && go run . compose {{ids}}

# Each shot's lines through gluon with no window: what they print, and a warm build cache
shots-check *ids:
    cd tools/shots && go run . check {{ids}}

# The permissions and tools the screenshots need, checked without opening a window
shots-preflight:
    cd tools/shots && go run . preflight

# One throwaway window with every primitive checked in it, closed again
shots-spike:
    cd tools/shots && go run . spike

# The screenshot tool's own tests
shots-test:
    cd tools/shots && go vet ./... && go test ./...
