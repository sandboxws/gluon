package rpc

import (
	"fmt"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/argline"
	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/redact"
)

// GRPC is the plugin for google.golang.org/grpc.
//
// It answers the question a service's own developer has and currently writes a
// program for: what does this method return for this input. The ceremony that
// program is made of — dial, construct a client, build a request, call, print,
// close — is longer than the equivalent for REST, and the generated client's
// API is not memorable, which is why `curl`'s absence is felt here more than it
// is for HTTP.
//
// Server reflection is what makes it possible without a generated client: the
// method set is discovered rather than known, so the command can be told a
// target and nothing else. See the package comment for what that does not
// extend to.
type GRPC struct{}

func (GRPC) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "grpc",
		Module:  "google.golang.org/grpc",
		Summary: ":grpc lists what a server exposes, and calls a unary method on it",
	}
}

// Imports is what a line naming grpc. resolves through, so the qualifier costs
// no goimports pass. The generated programs name far more than this and declare
// it per call — see call.imports.
func (GRPC) Imports() []plugin.Import {
	return []plugin.Import{{Name: "grpc", Path: "google.golang.org/grpc"}}
}

func (GRPC) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "grpc", Module: "google.golang.org/grpc"}}
}

const (
	// defaultTimeout bounds the call inside the child.
	//
	// The evaluator's own timeout would eventually fire, but it is measured for
	// compiling and running a program, not for waiting on a network. A server
	// that never answers should say so in seconds rather than wedge the prompt
	// for the full session timeout with no explanation.
	defaultTimeout = "30s"

	usage = "usage: :grpc <addr> [m]   e.g. :grpc localhost:50051, " +
		":grpc localhost:50051 pkg.Svc/Get -d '{\"id\":1}'"
)

func (GRPC) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":grpc",
		Arg:  "<addr> [m]",
		Usage: cmdspec.Spec{
			Kind:    cmdspec.Words,
			FlagsAt: cmdspec.After,
			Params: []cmdspec.Param{
				{Name: "addr", Help: "host:port; a name-resolver scheme passes through as written"},
				{Name: "method", Optional: true, Help: "pkg.Service/Method, as the listing prints it"},
			},
			Flags: []cmdspec.Flag{
				{Name: "-H", Value: "header", Repeat: true, Help: "call metadata, written 'name: value'; repeat it for more"},
				{Name: "-d", Value: "json", Help: "the request, checked against the descriptor ({} by default)"},
				{Name: "-t", Value: "duration", Help: "how long to wait: 5s, 500ms, 1m (" + defaultTimeout + " by default)"},
				{Name: "-tls", Help: "dial with TLS instead of plaintext"},
			},
			Examples: []cmdspec.Example{
				{Line: ":grpc localhost:50051", Says: "what the server exposes, discovered through reflection"},
				{Line: `:grpc localhost:50051 shop.v1.Orders/Get -d '{"id": 42}' -t 5s`},
				{Line: ":grpc api.internal:443 shop.v1.Orders/Get -tls -H 'authorization: Bearer $TOKEN'",
					Says: "the token is named, so its value reaches no history file"},
			},
			See: []string{":http", ":pb"},
		},
		Summary: "what a gRPC server exposes, and one unary call against it",
		Detail: "With an address alone, lists the services and methods the server exposes,\n" +
			"discovered through server reflection. A method that needs a stream is listed\n" +
			"and marked, because gluon compiles each line into a program that exits and a\n" +
			"stream has nowhere to live — reading one message and hanging up would return\n" +
			"a number that looks like an answer and is not.\n\n" +
			"With a `pkg.Service/Method` as well, calls it. -d '<json>' is the request and\n" +
			"defaults to {}; it is checked against the reflected descriptor before the call\n" +
			"is made, so a field you got wrong is named here rather than by the server.\n\n" +
			"Quote anything with a space in it: no shell has been near the line.\n\n" +
			"A metadata value may name a variable rather than carry one.\n" +
			"-H 'authorization: Bearer $TOKEN' resolves $TOKEN in the child, so the value\n" +
			"reaches neither :src, nor :save, nor ~/.local/state/gluon/history. A value\n" +
			"whose *shape* says it is a credential is refused instead of sent, because by\n" +
			"then the line as typed is already in the history file and masking the display\n" +
			"would not take it back.\n\n" +
			"Every call dials and hangs up. gRPC assumes a channel that outlives many\n" +
			"calls and this process outlives one, so latency measured here is the cost of\n" +
			"connecting plus the call and is not a measurement of gRPC. Nothing survives\n" +
			"between calls: no channel, no resolved address, no state a server kept.\n\n" +
			"The response is never cached. The cache is keyed on program text alone, so\n" +
			"the same call would otherwise answer with what it first saw.\n\n" +
			"There is no :proto. Reading a .proto off disk needs a compiler that\n" +
			"google.golang.org/protobuf does not contain — internal/plugins/rpc's package\n" +
			"comment records what was examined and what it would take.",
		Live: &plugin.LiveCall{Plan: plan},
	}}
}

// header is one -H, already split into what was typed and what was named.
type header struct {
	Name string
	// Value is the value as typed, references intact. It is what a message
	// quotes: telling somebody about a value they did not write sends them
	// looking for the wrong thing.
	Value string
	Parts []argline.Part
}

// call is a parsed :grpc line. An empty Method is the listing form.
type call struct {
	Target string
	// Method is the `pkg.Service/Method` as given, and Service and MethodName
	// are its halves. All three are kept because the halves are what the
	// generated program needs and the whole is what a message should quote.
	Method     string
	Service    string
	MethodName string
	Body       string
	Headers    []header
	Timeout    string
	TLS        bool
	// Refs is every variable the metadata names, first-seen order. The child's
	// environment and the redaction list are built from this one walk, so the
	// two cannot disagree about which values are secret.
	Refs []string
}

// plan turns the line into the program to run.
//
// Everything it can refuse, it refuses here: before a socket is opened, and —
// for a credential typed as a literal — before the value is anywhere but the
// history file it is already in.
func plan(arg string) (plugin.Call, error) {
	c, err := parseCall(arg)
	if err != nil {
		return plugin.Call{}, err
	}
	out := plugin.Call{Imports: c.imports(), Refs: c.Refs}
	if c.Method == "" {
		out.Source, out.Report = c.listSource(), reportList(c.Target)
	} else {
		out.Source, out.Report = c.callSource(), reportCall(c.Target, c.Method)
	}
	return out, nil
}

// parseCall reads `<addr> [method]` and the flags that follow them.
//
// The operands are positional and lead, so the common case — :grpc <addr> —
// needs no flag at all. Flags come after, which is :http's shape and for the
// same reason: the tail here is a flag list.
func parseCall(arg string) (call, error) {
	c := call{Timeout: defaultTimeout, Body: "{}"}
	words, err := argline.Fields(arg)
	if err != nil {
		return c, err
	}
	if len(words) == 0 {
		return c, fmt.Errorf("%s", usage)
	}

	c.Target = words[0]
	if err := checkTarget(c.Target); err != nil {
		return c, err
	}

	i := 1
	if len(words) > 1 && !strings.HasPrefix(words[1], "-") {
		if err := c.setMethod(words[1]); err != nil {
			return c, err
		}
		i = 2
	}
	for ; i < len(words); i++ {
		flag := words[i]
		switch flag {
		case "-tls":
			c.TLS = true
			continue
		case "-H", "-d", "-t":
		default:
			return c, fmt.Errorf(
				"unknown flag %s — :grpc takes -H <name: value>, -d <json>, -t <duration> "+
					"and -tls", flag)
		}
		if i+1 >= len(words) {
			return c, fmt.Errorf("%s needs a value", flag)
		}
		value := words[i+1]
		i++
		switch flag {
		case "-H":
			h, err := parseHeader(value)
			if err != nil {
				return c, err
			}
			c.Headers = append(c.Headers, h)
		case "-d":
			c.Body = value
		case "-t":
			if d, err := time.ParseDuration(value); err != nil || d <= 0 {
				return c, fmt.Errorf("-t %s is not a duration — 5s, 500ms, 1m", value)
			}
			c.Timeout = value
		}
	}

	if c.Method == "" {
		for _, flag := range []struct {
			set  bool
			name string
		}{{len(c.Headers) > 0, "-H"}, {c.Body != "{}", "-d"}} {
			if flag.set {
				return c, fmt.Errorf(
					"%s belongs to a call, and no method was named — :grpc %s pkg.Svc/Method %s ...",
					flag.name, c.Target, flag.name)
			}
		}
	}
	for _, h := range c.Headers {
		c.Refs = append(c.Refs, argline.Refs(h.Parts)...)
	}
	return c, c.checkCredentialTransport()
}

// setMethod splits `pkg.Service/Method` into its halves.
//
// The dotted spelling `pkg.Service.Method` is taken too, because it is what
// people write when they are reading a .proto rather than a URL, and refusing
// it would be a rule with no purpose. The last dot is the split: a package has
// dots in it and a method name does not.
func (c *call) setMethod(spec string) error {
	c.Method = spec
	if svc, name, ok := strings.Cut(spec, "/"); ok {
		if svc == "" || name == "" || strings.Contains(name, "/") {
			return fmt.Errorf("%s is not a method — write it pkg.Service/Method", spec)
		}
		c.Service, c.MethodName = svc, name
		return nil
	}
	i := strings.LastIndex(spec, ".")
	if i <= 0 || i == len(spec)-1 {
		return fmt.Errorf(
			"%s names no service — write it pkg.Service/Method, which is what the listing "+
				"prints", spec)
	}
	c.Service, c.MethodName = spec[:i], spec[i+1:]
	return nil
}

// checkTarget refuses an address gluon would have to complete.
//
// A gRPC target is host:port and carries no scheme to read, so the port is the
// one piece that cannot be guessed: 443 and 50051 are both ordinary, and
// picking one silently would make the difference invisible in exactly the
// session where somebody is debugging it. A target with a gRPC name-resolver
// scheme is passed through as written — that is grpc-go's own grammar, and
// re-checking it here would be a second, worse copy of it.
func checkTarget(target string) error {
	switch {
	case target == "":
		return fmt.Errorf("%s", usage)
	case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
		return fmt.Errorf(
			"%s is a URL — a gRPC target is host:port, so write %s", target,
			strings.TrimSuffix(strings.SplitN(strings.TrimPrefix(
				strings.TrimPrefix(target, "http://"), "https://"), "/", 2)[0], "/"))
	case strings.Contains(target, "://"):
		return nil // a resolver scheme: dns:///, unix:///, passthrough:///
	case strings.HasPrefix(target, "unix:"):
		return nil
	case !strings.Contains(target, ":"):
		return fmt.Errorf(
			"%s names no port — a gRPC target is host:port. gluon will not pick one: "+
				"443 and 50051 are both ordinary", target)
	}
	return nil
}

// parseHeader splits one -H into a name and a value, and the value into the
// literal text and the variables it names.
func parseHeader(s string) (header, error) {
	// The first colon is the separator: a metadata value routinely contains
	// more of them, starting with every URL.
	name, value, ok := strings.Cut(s, ":")
	if !ok {
		return header{}, fmt.Errorf("-H %q has no colon — metadata is written `name: value`", s)
	}
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if name == "" {
		return header{}, fmt.Errorf("-H %q has no name", s)
	}
	if strings.HasSuffix(name, "-bin") {
		return header{}, fmt.Errorf(
			"%s is a binary metadata key, whose value is base64 of arbitrary bytes — "+
				"there is no way to write one on a line", name)
	}
	parts := argline.SplitRefs(value)

	// The shape test runs on what was typed, with the references still
	// standing as $NAME — which is exactly why a referenced credential passes
	// and a typed one does not, without the test having to know what a
	// reference means. Invariant 23 holds either way: the key's *name* is never
	// consulted, so an x-anything carrying a bearer token is refused and an
	// authorization carrying "Negotiate" is not.
	if redact.Secret(argline.Probe(parts)) {
		return header{}, fmt.Errorf(
			"%s looks like a credential typed on the line, and the line is already in "+
				"~/.local/state/gluon/history.\n"+
				"      Masking the display would not take it back. Name a variable instead:  "+
				"-H '%s: $TOKEN'  — it is read in the child and written nowhere",
			name, name)
	}
	return header{Name: name, Value: value, Parts: parts}, nil
}

// checkCredentialTransport refuses to put a credential on the wire in the
// clear.
//
// Plaintext is the default because a REPL is usually pointed at a local
// service, and defaulting the other way would make the common case need a flag
// for nothing. What must not follow from that default is a token sent
// unencrypted to a host that is not this machine, so that one combination is
// refused and names the flag that fixes it. Loopback is exempt because there is
// no wire to listen on.
func (c call) checkCredentialTransport() error {
	if c.TLS || len(c.Headers) == 0 || loopback(c.Target) {
		return nil
	}
	return fmt.Errorf(
		"%s is not this machine and the connection would be plaintext, so metadata — "+
			"including %s — would go\n      out unencrypted. Add -tls, or point at "+
			"localhost", c.Target, c.Headers[0].Name)
}

// loopback reports whether the target is this machine.
func loopback(target string) bool {
	host := target
	if _, rest, ok := strings.Cut(target, "://"); ok {
		host = rest
	}
	if strings.HasPrefix(host, "unix:") || strings.HasPrefix(target, "unix:") {
		// A unix socket is a path on this machine by construction.
		return true
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	switch host {
	case "localhost", "127.0.0.1", "::1", "":
		return true
	}
	return strings.HasPrefix(host, "127.")
}
