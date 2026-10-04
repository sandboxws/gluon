// Command grpcserver is the server :grpc's integration tests dial.
//
// It is its own module, under testdata, for one reason: gluon links neither
// gRPC nor a protobuf runtime, and a test binary that imported them would put
// both in gluon's own go.mod — which is the thing the change's spec forbids and
// its task list verifies with `git diff go.mod go.sum`. The go tool ignores
// directories named testdata, so nothing here reaches gluon's module graph,
// `go build ./...` or `go vet ./...`.
//
// It prints the address it is listening on, as one line, and then serves until
// it is killed. The caller reads that line rather than picking a port, because
// a port chosen in advance is a port something else may already hold.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	pb "gluon.test/grpcserver/testpb"
)

// probe answers every method the tests need.
type probe struct {
	pb.UnimplementedProbeServer
	// calls counts every unary call this process has served. It is what makes
	// "the same call twice returned the same answer" observable: a cached
	// answer repeats a number that cannot repeat.
	calls atomic.Int64
}

func (p *probe) Echo(ctx context.Context, req *pb.EchoRequest) (*pb.EchoReply, error) {
	reply := &pb.EchoReply{
		Text:  req.GetText(),
		Count: p.calls.Add(1),
		Took:  durationpb.New(90 * time.Minute),
	}
	if key := req.GetMetaKey(); key != "" {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vs := md.Get(key); len(vs) > 0 {
				reply.Meta = vs[0]
			}
		}
	}
	if pr, ok := peer.FromContext(ctx); ok {
		reply.Conn = pr.Addr.String()
	}
	return reply, nil
}

func (p *probe) Fail(context.Context, *pb.Empty) (*pb.Empty, error) {
	return nil, status.Error(codes.FailedPrecondition, "the ledger is not open")
}

func (p *probe) Watch(*pb.Empty, grpc.ServerStreamingServer[pb.EchoReply]) error {
	return status.Error(codes.Unimplemented, "nothing calls this; it exists to be listed")
}

func (p *probe) Upload(grpc.ClientStreamingServer[pb.EchoRequest, pb.EchoReply]) error {
	return status.Error(codes.Unimplemented, "nothing calls this; it exists to be listed")
}

func (p *probe) Chat(grpc.BidiStreamingServer[pb.EchoRequest, pb.EchoReply]) error {
	return status.Error(codes.Unimplemented, "nothing calls this; it exists to be listed")
}

func main() {
	reflect := flag.Bool("reflect", true, "register the server reflection service")
	flag.Parse()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	s := grpc.NewServer()
	pb.RegisterProbeServer(s, &probe{})
	if *reflect {
		reflection.Register(s)
	}
	// One line, flushed before Serve blocks: the caller is waiting on it to
	// know the port, and a buffered write would deadlock the test rather than
	// fail it.
	fmt.Println(lis.Addr().String())
	if err := s.Serve(lis); err != nil {
		panic(err)
	}
}
