package gluonrt

// This file is NOT part of Source, so it never reaches the generated program.
// It exists only so gluon's own process can reuse the encoder that the child
// would have used.

// Payload encodes values exactly as the injected printer writes them.
//
// gluon answers a constant expression from go/types without building or
// running anything. Formatting that answer separately would be a second
// implementation free to drift from the child's — a byte and a rune print
// differently from a plain integer, and only one of those rules would get
// updated. Calling the same function makes divergence impossible.
func Payload(vs ...any) string { return string(__gluonPayload(vs...)) }
