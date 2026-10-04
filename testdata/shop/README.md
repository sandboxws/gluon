# shop

A small shop service that exists to be looked at. gluon's guides record their
sessions against a copy of it, and the screenshots are taken standing in it:
an HTTP API on chi, a gRPC service with reflection, a SQLite database through
sqlx with goose migrations, viper configuration, a cobra CLI and a samber/do
container — one of each thing a gluon plugin describes.

`go run ./cmd/seed` writes `data/shop.db`; `go run ./cmd/shopd` serves HTTP on
127.0.0.1:8765 and gRPC on 127.0.0.1:50051. Every value it serves is fixed, so
a session recorded against it reads the same every time.

It is test data. Nothing in gluon imports it, and `go` ignores a directory named
testdata.
