// Package rpc describes the gRPC libraries a session already has.
//
// Neither web nor db fits: this is not a router and not a database, and the
// two decisions that shaped it are recorded here rather than in a commit
// message, because both are boundaries a later change will otherwise walk into.
//
// # Streaming is listed and refused, never partially served
//
// A stream needs a process that outlives the evaluation, and gluon's child
// exits after every line (constraint A). That is the same ground `:ws` and an
// in-REPL `binding.pry` are permanently rejected on. The alternative that keeps
// coming up — call a streaming method, read one message, hang up — produces a
// number that looks like an answer and is not, and leaves a half-open stream at
// the far end. So `:grpc` lists every method with whether it can be called from
// a single evaluation, and refuses the ones that cannot, at the listing rather
// than at the call.
//
// # There is no :proto, and this is why
//
// The proposal made a `:proto <file>` conditional on its being buildable
// without a dependency (constraint K). It is not, and the finding is recorded
// here because an absent command is otherwise indistinguishable from an
// oversight.
//
// What was examined is the descriptor half of google.golang.org/protobuf —
// which is already in the build list of any session that has gRPC, so using it
// would have cost nothing:
//
//   - protodesc.NewFile(*descriptorpb.FileDescriptorProto, Resolver) and
//     protodesc.NewFiles(*descriptorpb.FileDescriptorSet) both take a
//     descriptor that has already been compiled. They consume protoc's output;
//     they are not a front end for it.
//   - protoregistry.GlobalFiles holds only files whose generated Go is linked
//     into the running binary, keyed by the path recorded at generation time.
//     It cannot be pointed at a path on disk, and a .proto the project has not
//     generated code for is not in it at all.
//   - descriptorpb is the descriptor schema itself, so it can decode a
//     descriptor set — a *.protoset or protoc --descriptor_set_out — and
//     nothing else.
//
// There is no .proto parser anywhere in google.golang.org/protobuf. Reading
// one would need github.com/bufbuild/protocompile (or the older
// github.com/jhump/protoreflect/desc/protoparse), which is a new dependency and
// therefore an issue first, not a commit. Shelling out to protoc was the other
// way and is worse: it is a dependency on the user's machine that fails
// confusingly when it is absent, and it is a code generator, which
// ROADMAP.md's non-goals already exclude.
//
// The gap this leaves is narrower than it sounds. Every question `:proto` would
// have answered about a *running* server, `:grpc <target>` answers from server
// reflection, which is the same descriptors over the wire instead of off the
// disk.
package rpc
