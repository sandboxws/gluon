# Go, Backwards Compatibility, and GODEBUG

## Introduction {#intro}

This is prose about the mechanism and belongs to no release.

## Default GODEBUG Values {#default}

Also not a release.

## GODEBUG History {#history}

### Go 1.21

Go 1.21 introduced a new `panicnil` setting that controls whether `panic(nil)`
is allowed.

### Go 1.20

Go 1.20 changed the default to always enable `http2client`.
