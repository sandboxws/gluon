-- +goose Up
CREATE INDEX orders_by_status ON orders (status, placed_at);

-- +goose Down
DROP INDEX orders_by_status;
