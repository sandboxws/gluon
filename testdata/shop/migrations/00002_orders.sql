-- +goose Up
CREATE TABLE orders (
    id        INTEGER PRIMARY KEY,
    user_id   INTEGER NOT NULL REFERENCES users (id),
    total     TEXT NOT NULL,
    status    TEXT NOT NULL,
    placed_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE orders;
