// Package external is the SDK for an external Collection: a Collection
// built outside this repository, compiled into a program of its own, and
// run by The Pleiades as a child process, once per task.
//
// A built-in Collection is a Go package compiled into the pleiades binary,
// which means adding one means rebuilding The Pleiades. An external one is a
// separate program that imports only pkg/ (this package, pkg/collection,
// pkg/sdk and whatever else under pkg/ it needs) and hands its methods to
// Main:
//
//	func main() {
//		external.Main(collection.Descriptor{
//			Name:     "acme.motd.set",
//			Manifest: collection.Manifest{ /* ... */ },
//			Invoke:   Set,
//			Check:    CheckSet,
//		})
//	}
//
// The methods are ordinary collection.Descriptor values, written exactly
// as a built-in method is, and validated by the same collection.Register
// a built-in goes through. What differs is only where the code runs.
//
// # The contract
//
// The Pleiades runs the program with one argument, in one of two ways:
//
//   - "describe": the program prints a Description (every method and its
//     full manifest) as JSON on stdout and exits 0. The loader reads it
//     once, when it loads the program.
//   - "invoke": the program reads one wire.ChildRequest from stdin, runs
//     the named method in the requested mode, and writes one
//     wire.ChildResponse to file descriptor 3 (ResponseFD). This is
//     exactly the exchange the Runner already uses with its own per-task
//     child, run by the same ServeChild.
//
// The program never runs on a managed device. It runs beside The Pleiades, on
// the machine running `pleiades run` or on the Runner, and reaches the
// device the way a built-in method does, through pkg/sdk.Connect and the
// credential in the request. Copying a program onto a target and running
// it there was considered and rejected: it needs the program built for
// every target's platform, leaves code on the device, and escapes the
// capability checks the engine applies before a method is called.
//
// # What crosses the boundary
//
// In: the method name, the mode, the task's params, the target device's
// identity, address and capabilities, and the credential The Pleiades resolved
// for the task (on stdin, never in argv or the environment). That
// credential is exactly what a built-in method receives through
// sdk.RunbookContext.InjectSecrets: the machine credential bound to the
// template, or the device's own stored credential when none is bound. The
// program starts with an environment the loader reduced to a short
// allowlist, so nothing else the parent holds is handed to it, and on
// Linux it runs confined by Landlock to its own directory, the system
// files it needs, the known_hosts file and a private TMPDIR, so it cannot
// read the credential store or the user's keys either. Out: whether
// anything changed, the stats the method recorded, and an error string.
// Nothing else is read back, and output on stdout and stderr is captured,
// capped, masked and logged, never parsed.
package external

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ProtocolVersion is the version of the contract between The Pleiades and an
// external Collection: the describe document and the invoke exchange.
//
// It is one number for the whole contract, bumped only by a change an
// existing external Collection could not survive (a renamed field, a new
// required one, a different descriptor for the response). An additive,
// optional field such as wire.ChildRequest.Mode is not a bump, because
// JSON decoding on both sides already ignores what it does not know. A
// loader refuses any other version rather than guessing at compatibility.
const ProtocolVersion = 1

// The two commands an external Collection answers, each as its single
// command-line argument.
const (
	// CommandDescribe asks the program for its Description.
	CommandDescribe = "describe"

	// CommandInvoke asks the program to run one method once.
	CommandInvoke = "invoke"
)

// Description is what an external Collection prints for CommandDescribe:
// which protocol it speaks and every method it provides.
//
// Each method's manifest is collection.Manifest itself, serialized once,
// not a second document describing it. Two documents that must agree
// eventually will not, and here the failure would be a program describing
// one method and running another. So everything a built-in method
// declares, capabilities and check support included, arrives from a third
// party in exactly the shape the registry already validates.
type Description struct {
	// Protocol is the ProtocolVersion the program was built against.
	Protocol int `json:"protocol"`

	// Methods lists every method the program provides.
	Methods []DescribedMethod `json:"methods"`
}

// DescribedMethod is one method an external Collection provides.
type DescribedMethod struct {
	// Name is the method's fully-qualified name, namespaced exactly like a
	// built-in one.
	Name string `json:"name"`

	// Manifest is the method's full declared contract.
	Manifest collection.Manifest `json:"manifest"`
}

// Streams are the streams Serve reads and writes. Main passes the
// process's own; a test passes buffers.
type Streams struct {
	// In carries the request for CommandInvoke.
	In io.Reader

	// Out carries the Description for CommandDescribe.
	Out io.Writer

	// Err carries diagnostics meant for a person.
	Err io.Writer

	// Response carries the one response frame for CommandInvoke. It is
	// file descriptor 3 in a real child, never stdout.
	Response io.Writer
}

// Main is the whole main function of an external Collection. It serves
// descs for the command the program was run with and exits with Serve's
// code; it never returns.
func Main(descs ...collection.Descriptor) {
	os.Exit(Serve(context.Background(), os.Args[1:], descs, Streams{
		In:       os.Stdin,
		Out:      os.Stdout,
		Err:      os.Stderr,
		Response: os.NewFile(uintptr(ResponseFD), "collection-response"),
	}))
}

// Serve answers one command for descs and returns the process exit code:
// 0 for an answered command, 1 for an invoke exchange that broke, and 2
// for a program that cannot serve at all (a bad argument, or a method
// collection.Register refuses).
//
// Every method is registered first, through collection.Register, so an
// external method is held to exactly the rules a built-in one is: a
// namespaced name, known capabilities, a reversibility answer, and a
// check declaration that agrees with itself. A method that is declared but
// not implemented is refused too, since a program exists to run code and
// a stub has none. Failing here, on the author's own machine, beats
// failing in the loader on somebody else's.
func Serve(ctx context.Context, args []string, descs []collection.Descriptor, s Streams) int {
	if len(args) != 1 || (args[0] != CommandDescribe && args[0] != CommandInvoke) {
		fmt.Fprintf(s.Err, "usage: %s %s|%s\n\nThis program is an external Collection for The Pleiades. The Pleiades runs it; it is not meant to be run by hand.\n",
			programName(), CommandDescribe, CommandInvoke)
		return 2
	}

	byName := make(map[string]collection.Descriptor, len(descs))
	for _, d := range descs {
		if d.Manifest.Status != collection.StatusImplemented {
			fmt.Fprintf(s.Err, "external collection: %q is not implemented: every method a program provides must be\n", d.Name)
			return 2
		}
		if err := collection.Register(d); err != nil {
			fmt.Fprintln(s.Err, "external collection:", err)
			return 2
		}
		byName[d.Name] = d
	}

	if args[0] == CommandDescribe {
		desc := Description{Protocol: ProtocolVersion, Methods: make([]DescribedMethod, 0, len(descs))}
		for _, d := range descs {
			desc.Methods = append(desc.Methods, DescribedMethod{Name: d.Name, Manifest: d.Manifest})
		}
		if err := json.NewEncoder(s.Out).Encode(&desc); err != nil {
			fmt.Fprintln(s.Err, "external collection: failed to write the description:", err)
			return 1
		}
		return 0
	}

	// The lookup is over this program's own methods only, never the
	// process-wide registry, so a library that happens to register a
	// method of its own cannot make it reachable through this program.
	lookup := func(name string) (collection.Descriptor, bool) {
		d, ok := byName[name]
		return d, ok
	}
	return ServeChild(ctx, lookup, s.In, s.Response, s.Err)
}

// programName is this program's own name for its usage line, or a generic
// word when the platform does not provide one.
func programName() string {
	if len(os.Args) > 0 && os.Args[0] != "" {
		return os.Args[0]
	}
	return "collection"
}
