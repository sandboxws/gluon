package rpc

import (
	"strconv"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/argline"
	"github.com/sandboxws/gluon/internal/plugin"
)

// The program each form becomes.
//
// It is an ordinary gRPC client and nothing else, which is what keeps the
// plugin rule — a command can do nothing a line you could have typed could not
// — literally true, and what makes :src worth reading afterwards. The only
// thing it does that typing does not is read a credential by name rather than
// carry one, which is the same reason :http generates os.Getenv.

// Import paths the generated programs use. They are supplied rather than left
// to goimports, which costs ~135ms, and exact because an import nothing names
// does not compile.
var (
	baseImports = []plugin.Import{
		{Name: "context", Path: "context"},
		{Name: "fmt", Path: "fmt"},
		{Name: "strings", Path: "strings"},
		{Name: "time", Path: "time"},
		{Name: "grpc", Path: "google.golang.org/grpc"},
		{Name: "codes", Path: "google.golang.org/grpc/codes"},
		{Name: "status", Path: "google.golang.org/grpc/status"},
		{Name: "grpc_reflection_v1", Path: "google.golang.org/grpc/reflection/grpc_reflection_v1"},
		{Name: "proto", Path: "google.golang.org/protobuf/proto"},
		{Name: "protodesc", Path: "google.golang.org/protobuf/reflect/protodesc"},
		{Name: "protoreflect", Path: "google.golang.org/protobuf/reflect/protoreflect"},
		{Name: "descriptorpb", Path: "google.golang.org/protobuf/types/descriptorpb"},
	}
	plaintextImports = []plugin.Import{
		{Name: "insecure", Path: "google.golang.org/grpc/credentials/insecure"},
	}
	tlsImports = []plugin.Import{
		{Name: "tls", Path: "crypto/tls"},
		{Name: "credentials", Path: "google.golang.org/grpc/credentials"},
	}
	callImports = []plugin.Import{
		{Name: "strconv", Path: "strconv"},
		{Name: "protojson", Path: "google.golang.org/protobuf/encoding/protojson"},
		{Name: "dynamicpb", Path: "google.golang.org/protobuf/types/dynamicpb"},
	}
	metadataImports = []plugin.Import{
		{Name: "metadata", Path: "google.golang.org/grpc/metadata"},
	}
	envImports = []plugin.Import{{Name: "os", Path: "os"}}
)

// imports is what one call's program names.
func (c call) imports() []plugin.Import {
	out := append([]plugin.Import(nil), baseImports...)
	if c.TLS {
		out = append(out, tlsImports...)
	} else {
		out = append(out, plaintextImports...)
	}
	if c.Method != "" {
		out = append(out, callImports...)
	}
	if len(c.Headers) > 0 {
		out = append(out, metadataImports...)
	}
	if len(c.Refs) > 0 {
		out = append(out, envImports...)
	}
	return out
}

// creds is the transport the dial uses.
//
// Plaintext is the default because a gRPC target carries no scheme to read and
// the service somebody is debugging from a REPL is overwhelmingly a local one.
// What is not defaulted is the security decision that follows from it: a
// credential over a plaintext connection to anything but loopback is refused in
// parseCall rather than sent, so the default cannot quietly put a token on the
// wire in the clear.
func (c call) creds() string {
	if c.TLS {
		return "grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{}))"
	}
	return "grpc.WithTransportCredentials(insecure.NewCredentials())"
}

// goDuration renders a duration as Go source.
//
// Whole seconds become 30*time.Second rather than a nanosecond count, because
// :src shows this program to the person who typed the line and nobody reads
// 30000000000. It is :http's httpTimeout reasoning rather than its code, for
// the reason that one is not internal/db's either.
func goDuration(spec string) string {
	d, err := time.ParseDuration(spec)
	if err != nil || d <= 0 {
		d, _ = time.ParseDuration(defaultTimeout)
	}
	switch {
	case d%time.Second == 0:
		return strconv.FormatInt(int64(d/time.Second), 10) + "*time.Second"
	case d%time.Millisecond == 0:
		return strconv.FormatInt(int64(d/time.Millisecond), 10) + "*time.Millisecond"
	}
	return "time.Duration(" + strconv.FormatInt(int64(d), 10) + ")"
}

// metaExpr is the Go expression one metadata value becomes.
//
// A referenced value is os.Getenv("NAME") and never the value itself, so
// neither <tmp>/main.go, :src, :save nor a build error — which quotes source —
// can carry it. That leaves the child's own output, which EvalLive's mask
// covers.
func metaExpr(parts []argline.Part) string {
	if len(parts) == 0 {
		return `""`
	}
	terms := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Ref != "" {
			terms = append(terms, "os.Getenv("+strconv.Quote(p.Ref)+")")
			continue
		}
		terms = append(terms, strconv.Quote(p.Text))
	}
	return strings.Join(terms, "+")
}

// prologue dials and walks server reflection.
//
// Both forms need exactly this: a connection, the service list, and every
// descriptor those services live in. The walk accumulates across the whole
// stream because grpc-go sends each file once per stream — a second request for
// a service sharing an import gets the import elided, so a set built per
// response would be missing files the first response already delivered.
//
// The failure paths are three and are kept apart. A connection that never
// opened is a different answer from a server that answered without a reflection
// service, which is different again from a server that reflected and exposes
// nothing — and reporting the second as the third sends somebody looking for a
// bug in a server that is working.
func (c call) prologue() string {
	var b strings.Builder
	b.WriteString(`
	var __out []string
	__emit := func(__s string) { __out = append(__out, __s) }
	defer func() { fmt.Print(strings.Join(__out, "\n")) }()
	__fail := func(__e error) {
		if __s, __ok := status.FromError(__e); __ok && __s.Code() == codes.Unimplemented {
			__emit(` + litNoReflect + ` + __s.Message())
			return
		}
		__emit(` + litErr + ` + __e.Error())
	}
	__ctx, __cancel := context.WithTimeout(context.Background(), ` + goDuration(c.Timeout) + `)
	defer __cancel()
	__conn, __err := grpc.NewClient(` + strconv.Quote(c.Target) + `, ` + c.creds() + `)
	if __err != nil {
		__emit(` + litErr + ` + __err.Error())
		return nil
	}
	// gRPC assumes a channel that outlives many calls; this process does not
	// outlive one. Closing is not an optimisation here, it is the whole
	// lifetime — see the command's Detail.
	defer __conn.Close()

	__stream, __err := grpc_reflection_v1.NewServerReflectionClient(__conn).ServerReflectionInfo(__ctx)
	if __err != nil {
		__fail(__err)
		return nil
	}
	__ask := func(__req *grpc_reflection_v1.ServerReflectionRequest) (*grpc_reflection_v1.ServerReflectionResponse, error) {
		if __e := __stream.Send(__req); __e != nil {
			return nil, __e
		}
		return __stream.Recv()
	}
	__resp, __err := __ask(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{},
	})
	if __err != nil {
		__fail(__err)
		return nil
	}
	if __er := __resp.GetErrorResponse(); __er != nil {
		__fail(status.Error(codes.Code(__er.GetErrorCode()), __er.GetErrorMessage()))
		return nil
	}

	__files := map[string]*descriptorpb.FileDescriptorProto{}
	var __names []string
	for _, __s := range __resp.GetListServicesResponse().GetService() {
		__names = append(__names, __s.GetName())
		__r, __e := __ask(&grpc_reflection_v1.ServerReflectionRequest{
			MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_FileContainingSymbol{
				FileContainingSymbol: __s.GetName(),
			},
		})
		if __e != nil {
			__fail(__e)
			return nil
		}
		if __r.GetErrorResponse() != nil {
			// One service the server would not describe is not a reason to
			// describe none of them.
			continue
		}
		for _, __raw := range __r.GetFileDescriptorResponse().GetFileDescriptorProto() {
			__fd := &descriptorpb.FileDescriptorProto{}
			if __e := proto.Unmarshal(__raw, __fd); __e != nil {
				__emit(` + litErr + ` + __e.Error())
				return nil
			}
			__files[__fd.GetName()] = __fd
		}
	}
	__set := &descriptorpb.FileDescriptorSet{}
	for _, __fd := range __files {
		__set.File = append(__set.File, __fd)
	}
	__reg, __err := protodesc.NewFiles(__set)
	if __err != nil {
		__emit(` + litErr + ` + __err.Error())
		return nil
	}
	__kind := func(__m protoreflect.MethodDescriptor) string {
		switch {
		case __m.IsStreamingClient() && __m.IsStreamingServer():
			return ` + strconv.Quote(kindBidi) + `
		case __m.IsStreamingClient():
			return ` + strconv.Quote(kindClient) + `
		case __m.IsStreamingServer():
			return ` + strconv.Quote(kindServer) + `
		}
		return ` + strconv.Quote(kindUnary) + `
	}
	__service := func(__name string) protoreflect.ServiceDescriptor {
		__d, __e := __reg.FindDescriptorByName(protoreflect.FullName(__name))
		if __e != nil {
			return nil
		}
		__sd, _ := __d.(protoreflect.ServiceDescriptor)
		return __sd
	}
	__report := func(__sd protoreflect.ServiceDescriptor) {
		for __i := 0; __i < __sd.Methods().Len(); __i++ {
			__m := __sd.Methods().Get(__i)
			__emit(` + litMethod + ` + string(__sd.FullName()) +
		` + litSep + ` + string(__m.Name()) + ` + litSep + ` + __kind(__m) +
		` + litSep + ` + string(__m.Input().FullName()) + ` + litSep + ` + string(__m.Output().FullName()))
		}
	}
`)
	return b.String()
}

// listSource is the program `:grpc <target>` becomes.
//
// It returns the service names so the expression has a value at all; the answer
// the user sees is built by reportList from the marker lines, because a table
// laid out in generated Go is a table nobody can read in :src.
func (c call) listSource() string {
	return "func() []string {" + c.prologue() + `
	for _, __name := range __names {
		if __sd := __service(__name); __sd != nil {
			__report(__sd)
		}
	}
	return __names
}()`
}

// callSource is the program `:grpc <target> <Service/Method>` becomes.
//
// The request is checked against the reflected descriptor before Invoke is
// reached, so a request the user got wrong locally fails naming the user's own
// field rather than costing a round trip and coming back in the server's field
// paths. It is the ordering :query already chose, where db.CheckStatement runs
// before anything is resolved.
//
// The response comes back as a map of real Go values rather than as protojson,
// so that every renderer that applies to a value anywhere applies here too — a
// google.protobuf.Duration field reaches the terminal as a time.Duration and is
// drawn by the renderer that draws every other duration.
func (c call) callSource() string {
	meta := ""
	if len(c.Headers) > 0 {
		var pairs []string
		for _, h := range c.Headers {
			pairs = append(pairs, strconv.Quote(strings.ToLower(h.Name)), metaExpr(h.Parts))
		}
		meta = "\n\t__ctx = metadata.AppendToOutgoingContext(__ctx, " +
			strings.Join(pairs, ", ") + ")\n"
	}
	return "func() map[string]any {" + c.prologue() + `
	__sd := __service(` + strconv.Quote(c.Service) + `)
	if __sd == nil {
		__emit(` + litUnknown + ` + "service" + ` + litSep + ` + ` + strconv.Quote(c.Service) + `)
		return nil
	}
	__md := __sd.Methods().ByName(protoreflect.Name(` + strconv.Quote(c.MethodName) + `))
	if __md == nil {
		__emit(` + litUnknown + ` + "method" + ` + litSep + ` + ` + strconv.Quote(c.Service) + `)
		__report(__sd)
		return nil
	}
	if __k := __kind(__md); __k != ` + strconv.Quote(kindUnary) + ` {
		__emit(` + litStream + ` + map[string]string{
			` + strconv.Quote(kindClient) + `: "client stream",
			` + strconv.Quote(kindServer) + `: "server stream",
			` + strconv.Quote(kindBidi) + `:   "bidirectional stream",
		}[__k])
		return nil
	}

	__in := dynamicpb.NewMessage(__md.Input())
	if __e := protojson.Unmarshal([]byte(` + strconv.Quote(c.Body) + `), __in); __e != nil {
		__emit(` + litBadReq + ` + __e.Error())
		return nil
	}

	var __fields func(protoreflect.Message) map[string]any
	var __value func(protoreflect.FieldDescriptor, protoreflect.Value) any
	var __single func(protoreflect.FieldDescriptor, protoreflect.Value) any
	__message := func(__m protoreflect.Message) any {
		// The two well-known types with a Go counterpart gluon already renders.
		// Everything else is its fields: a wrapper of its own would be a second
		// spelling of a value the reader already knows how to read.
		__fs := __m.Descriptor().Fields()
		switch __m.Descriptor().FullName() {
		case "google.protobuf.Duration":
			return time.Duration(__m.Get(__fs.ByName("seconds")).Int())*time.Second +
				time.Duration(__m.Get(__fs.ByName("nanos")).Int())
		case "google.protobuf.Timestamp":
			return time.Unix(__m.Get(__fs.ByName("seconds")).Int(),
				__m.Get(__fs.ByName("nanos")).Int()).UTC()
		}
		return __fields(__m)
	}
	__single = func(__fd protoreflect.FieldDescriptor, __v protoreflect.Value) any {
		switch __fd.Kind() {
		case protoreflect.MessageKind, protoreflect.GroupKind:
			return __message(__v.Message())
		case protoreflect.EnumKind:
			if __e := __fd.Enum().Values().ByNumber(__v.Enum()); __e != nil {
				return string(__e.Name())
			}
			return int32(__v.Enum())
		}
		return __v.Interface()
	}
	__value = func(__fd protoreflect.FieldDescriptor, __v protoreflect.Value) any {
		switch {
		case __fd.IsMap():
			__out := map[string]any{}
			__v.Map().Range(func(__k protoreflect.MapKey, __mv protoreflect.Value) bool {
				__out[__k.String()] = __single(__fd.MapValue(), __mv)
				return true
			})
			return __out
		case __fd.IsList():
			__l := __v.List()
			__out := make([]any, 0, __l.Len())
			for __i := 0; __i < __l.Len(); __i++ {
				__out = append(__out, __single(__fd, __l.Get(__i)))
			}
			return __out
		}
		return __single(__fd, __v)
	}
	__fields = func(__m protoreflect.Message) map[string]any {
		// Range visits populated fields only, which is what proto3 means by a
		// field being set — inventing zero values for the rest would report
		// data the server did not send.
		__out := map[string]any{}
		__m.Range(func(__fd protoreflect.FieldDescriptor, __v protoreflect.Value) bool {
			__out[string(__fd.Name())] = __value(__fd, __v)
			return true
		})
		return __out
	}
` + meta + `
	__answer := dynamicpb.NewMessage(__md.Output())
	if __e := __conn.Invoke(__ctx, "/"+` + strconv.Quote(c.Service) + `+"/"+` +
		strconv.Quote(c.MethodName) + `, __in, __answer); __e != nil {
		__s, _ := status.FromError(__e)
		__emit(` + litStatus + ` + strconv.Itoa(int(__s.Code())) + ` + litSep + ` +
			__s.Code().String() + ` + litSep + ` + __s.Message())
		return nil
	}
	return __fields(__answer)
}()`
}
