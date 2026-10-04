-- +goose Up
CREATE TABLE users (
    id      INTEGER PRIMARY KEY,
    email   TEXT NOT NULL UNIQUE,
    plan    TEXT NOT NULL DEFAULT 'free',
    created TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE users;
