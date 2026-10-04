package gluonrt

import _ "embed"

// Source is print.go's own text, injected into every generated program as a
// sibling of main.go with its package clause rewritten. Embedding the file the
// compiler already checks means the printer can never drift from a stale copy.
//
//go:embed print.go
var Source string

// The fd gate travels the same way, but it is per-platform: dup2 does not
// exist on linux/arm64 and dup3 does not exist on darwin, so a single file
// naming either call would fail to compile somewhere. Both variants are
// embedded whole — build tags gate compilation, not embedding — and FDSource
// picks by GOOS. The child always compiles on the machine gluon is running
// on, so the host's GOOS is the only one that can ever be right.
//
//go:embed fd_dup2.go
var fdDup2 string

//go:embed fd_dup3.go
var fdDup3 string

// FDSource returns the fd-gate source for goos.
func FDSource(goos string) string {
	if goos == "linux" {
		return fdDup3
	}
	return fdDup2
}
