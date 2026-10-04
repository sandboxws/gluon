// Package events is what the shop tells the rest of the company, encoded
// with msgpack for the queue.
package events

import (
	"time"

	"github.com/google/uuid"
	"github.com/vmihailenco/msgpack/v5"
)

// OrderPlaced is sent when an order is paid for.
type OrderPlaced struct {
	OrderID  int64     `msgpack:"id" json:"id"`
	Customer uuid.UUID `msgpack:"customer" json:"customer"`
	Total    string    `msgpack:"total" json:"total"`
	At       time.Time `msgpack:"at" json:"at"`
}

// Encode is the bytes that go on the queue.
func Encode(e OrderPlaced) ([]byte, error) { return msgpack.Marshal(e) }

// Sample is one event, the same every time.
func Sample() OrderPlaced {
	return OrderPlaced{
		OrderID:  42,
		Customer: uuid.MustParse("5f0c2c1e-8a7b-4e0f-9c6d-3b2a1f0e9d8c"),
		Total:    "53.04",
		At:       time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC),
	}
}
