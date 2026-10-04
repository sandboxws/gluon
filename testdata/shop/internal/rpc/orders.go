// Package rpc is the shop's gRPC service.
package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shopv1 "example.com/shop/proto/shop/v1"
)

// Orders serves the order book. Its orders are fixed, so a call reads the
// same every time it is made.
type Orders struct {
	shopv1.UnimplementedOrdersServer
	book map[int64]*shopv1.Order
}

// NewOrders is the service with its fixed orders.
func NewOrders() *Orders {
	return &Orders{book: map[int64]*shopv1.Order{
		42: {
			Id:    42,
			Email: "ada@example.com",
			Items: []*shopv1.LineItem{
				{Sku: "MUG-01", Qty: 2, Price: "12.50"},
				{Sku: "TEE-M", Qty: 1, Price: "24.00"},
			},
			Total:  "53.04",
			Status: shopv1.Status_STATUS_PAID,
		},
	}}
}

// Get returns one order.
func (o *Orders) Get(_ context.Context, req *shopv1.GetRequest) (*shopv1.Order, error) {
	if ord, ok := o.book[req.GetId()]; ok {
		return ord, nil
	}
	return nil, status.Errorf(codes.NotFound, "no order %d", req.GetId())
}

// Watch streams an order through the statuses it has left to pass.
func (o *Orders) Watch(req *shopv1.GetRequest, stream shopv1.Orders_WatchServer) error {
	ord, ok := o.book[req.GetId()]
	if !ok {
		return status.Errorf(codes.NotFound, "no order %d", req.GetId())
	}
	for s := ord.GetStatus(); s <= shopv1.Status_STATUS_SHIPPED; s++ {
		if err := stream.Send(&shopv1.Order{Id: ord.GetId(), Status: s}); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}
