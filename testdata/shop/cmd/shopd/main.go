// Command shopd serves the shop: HTTP on 127.0.0.1:8765 and gRPC, with
// reflection, on 127.0.0.1:50051.
package main

import (
	"log"
	"net"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"example.com/shop/internal/api"
	"example.com/shop/internal/rpc"
	"example.com/shop/internal/store"
	shopv1 "example.com/shop/proto/shop/v1"
)

func main() {
	s, err := store.Open("data/shop.db")
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		lis, err := net.Listen("tcp", "127.0.0.1:50051")
		if err != nil {
			log.Fatal(err)
		}
		srv := grpc.NewServer()
		shopv1.RegisterOrdersServer(srv, rpc.NewOrders())
		healthpb.RegisterHealthServer(srv, health.NewServer())
		reflection.Register(srv)
		log.Fatal(srv.Serve(lis))
	}()
	log.Fatal(http.ListenAndServe("127.0.0.1:8765", api.Routes(s)))
}
