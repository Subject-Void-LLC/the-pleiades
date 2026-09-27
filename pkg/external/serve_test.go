// Tests for Serve, the whole of an external Collection's main function
// short of os.Exit: argument handling, registration of the program's own
// methods through collection.Register, the describe document, and the
// invoke exchange over the program's own methods only.
//
// Serve registers into the process-wide collection registry, exactly as
// it does in a real program, so every test that reaches registration takes
// a snapshot first and puts the registry back when it ends.
// program_test.go covers the same function as a real child process, with
// Main's real wiring.
package external_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// serveBuffers holds one buffer per stream Serve reads or writes, so a
// test can assert what went where as well as what was written.
type serveBuffers struct {
	in       *bytes.Buffer
	out      bytes.Buffer
	err      bytes.Buffer
	response bytes.Buffer
}

// streams returns the Streams view of b.
func (b *serveBuffers) streams() external.Streams {
	return external.Streams{In: b.in, Out: &b.out, Err: &b.err, Response: &b.response}
}

// runServe runs Serve over fresh buffers with in as stdin and returns the
// exit code with everything written. It takes the registry snapshot
// itself, since every path past the argument check registers.
func runServe(t *testing.T, args []string, descs []collection.Descriptor, in []byte) (int, *serveBuffers) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	b := &serveBuffers{in: bytes.NewBuffer(in)}
	code := external.Serve(context.Background(), args, descs, b.streams())
	return code, b
}

// requestFrame marshals req the way a parent writes it to a child's stdin.
func requestFrame(t *testing.T, req wire.ChildRequest) []byte {
	t.Helper()
	frame, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}
	return frame
}

// richDescriptors returns two registrable methods whose manifests set
// every field describe has to carry, one with check support and one
// without, so a round trip that dropped or defaulted any field shows up.
func richDescriptors(invoked *callCounter) []collection.Descriptor {
	withCheck := countingDescriptor("acme.motd.set", true, invoked)
	withCheck.Manifest = collection.Manifest{
		SupportedTransports:  []string{"ssh"},
		RequiredCapabilities: []capability.Name{capability.NameSSHTransport, capability.NameSystemd},
		ExecutionContext:     collection.ExecutionContext{RequiresElevation: true},
		PlatformTargets:      []collection.PlatformTarget{{Vendor: "acme", Model: "m1", VersionRange: ">=1.2", DeploymentContext: "lab"}},
		EngineVersion:        ">=0.9",
		Status:               collection.StatusImplemented,
		Reversibility:        collection.Reversibility{Reversible: true, Notes: "restores the previous banner text"},
		SupportsCheck:        true,
		Doc: collection.Doc{
			Summary: "Set the message of the day.",
			Params:  []collection.Param{{Name: "text", Type: "string", Required: true, Description: "The banner text."}},
		},
	}

	withoutCheck := countingDescriptor("acme.motd.show", false, invoked)
	withoutCheck.Manifest.SupportedTransports = []string{"ssh"}
	withoutCheck.Manifest.Doc = collection.Doc{Summary: "Show the message of the day."}

	return []collection.Descriptor{withCheck, withoutCheck}
}

// TestServe_DescribeWritesEveryMethodAndManifest proves describe prints
// one Description naming the protocol and every method in the order the
// program passed them, each with its whole manifest intact after a JSON
// round trip. The manifest is the one document a loader ever reads about a
// third-party method, so a field lost here is a field The Pleiades never
// learns.
func TestServe_DescribeWritesEveryMethodAndManifest(t *testing.T) {
	var calls callCounter
	descs := richDescriptors(&calls)

	code, b := runServe(t, []string{external.CommandDescribe}, descs, nil)
	if code != 0 {
		t.Fatalf("Serve(describe) = %d, want 0 (stderr: %s)", code, b.err.String())
	}
	raw := b.out.Bytes()

	dec := json.NewDecoder(bytes.NewReader(raw))
	var got external.Description
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decoding the description: %v\n%s", err, raw)
	}
	if dec.More() {
		t.Errorf("describe wrote more than one JSON value: %s", raw)
	}
	if got.Protocol != external.ProtocolVersion {
		t.Errorf("Protocol = %d, want %d", got.Protocol, external.ProtocolVersion)
	}
	if len(got.Methods) != len(descs) {
		t.Fatalf("described %d methods, want %d: %s", len(got.Methods), len(descs), raw)
	}
	for i, d := range descs {
		if got.Methods[i].Name != d.Name {
			t.Errorf("Methods[%d].Name = %q, want %q", i, got.Methods[i].Name, d.Name)
		}
		if !reflect.DeepEqual(got.Methods[i].Manifest, d.Manifest) {
			t.Errorf("Methods[%d].Manifest after a round trip =\n  %+v\nwant\n  %+v", i, got.Methods[i].Manifest, d.Manifest)
		}
	}

	// supportsCheck carries no omitempty, so a method without check
	// support must still say so in the document rather than leave the key
	// out, which a reader could not tell apart from nobody having asked.
	var shape struct {
		Methods []struct {
			Manifest map[string]json.RawMessage `json:"manifest"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("decoding the description's raw shape: %v", err)
	}
	for i, m := range shape.Methods {
		value, present := m.Manifest["supportsCheck"]
		if !present {
			t.Errorf("Methods[%d].manifest has no supportsCheck key: %s", i, raw)
			continue
		}
		if want := map[bool]string{true: "true", false: "false"}[descs[i].Manifest.SupportsCheck]; string(value) != want {
			t.Errorf("Methods[%d].manifest.supportsCheck = %s, want %s", i, value, want)
		}
	}

	if calls.invoke != 0 || calls.check != 0 {
		t.Errorf("describe ran a method (Invoke %d, Check %d times), want none", calls.invoke, calls.check)
	}
	if b.response.Len() != 0 || b.err.Len() != 0 {
		t.Errorf("describe wrote to the response stream %q or stderr %q, want only stdout", b.response.String(), b.err.String())
	}
}

// TestServe_UsageErrorsExitTwo proves anything but exactly one known
// command is refused with exit code 2 and a usage line on stderr, before
// anything registers or runs.
func TestServe_UsageErrorsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "empty args", args: []string{}},
		{name: "two args", args: []string{external.CommandDescribe, external.CommandInvoke}},
		{name: "a command and a flag", args: []string{external.CommandInvoke, "--verbose"}},
		{name: "unknown command", args: []string{"run"}},
		{name: "commands are case sensitive", args: []string{"Describe"}},
		{name: "help flag", args: []string{"--help"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls callCounter
			desc := countingDescriptor("acme.usage.method", true, &calls)

			code, b := runServe(t, tc.args, []collection.Descriptor{desc}, requestFrame(t, wire.ChildRequest{FQCN: desc.Name}))
			if code != 2 {
				t.Errorf("Serve(%q) = %d, want 2", tc.args, code)
			}
			usage := b.err.String()
			if !strings.HasPrefix(usage, "usage: ") || !strings.Contains(usage, "describe|invoke") {
				t.Errorf("stderr = %q, want a usage line naming describe|invoke", usage)
			}
			if b.out.Len() != 0 || b.response.Len() != 0 {
				t.Errorf("a usage error wrote stdout %q or a response %q, want neither", b.out.String(), b.response.String())
			}
			if calls.invoke != 0 || calls.check != 0 {
				t.Error("a usage error ran the method")
			}
			if _, registered := collection.Lookup(desc.Name); registered {
				t.Error("a usage error registered the program's methods, want a refusal before registration")
			}
		})
	}
}

// TestServe_RefusesADeclaredMethod proves a program cannot ship a stub.
// A declared method has no code to run, and a program exists to run code,
// so it is refused for both commands and named in the refusal.
func TestServe_RefusesADeclaredMethod(t *testing.T) {
	var calls callCounter
	stub := collection.Descriptor{Name: "acme.stub.method", Manifest: collection.Manifest{Status: collection.StatusDeclared}}
	implemented := countingDescriptor("acme.real.method", false, &calls)

	for _, command := range []string{external.CommandDescribe, external.CommandInvoke} {
		t.Run(command, func(t *testing.T) {
			frame := requestFrame(t, wire.ChildRequest{FQCN: implemented.Name})
			code, b := runServe(t, []string{command}, []collection.Descriptor{implemented, stub}, frame)
			if code != 2 {
				t.Errorf("Serve(%s) = %d, want 2", command, code)
			}
			if !strings.Contains(b.err.String(), `"acme.stub.method" is not implemented`) {
				t.Errorf("stderr = %q, want it to name the stub as not implemented", b.err.String())
			}
			if b.out.Len() != 0 || b.response.Len() != 0 {
				t.Errorf("a refused program wrote stdout %q or a response %q, want neither", b.out.String(), b.response.String())
			}
			if calls.invoke != 0 {
				t.Error("a refused program still ran one of its methods")
			}
		})
	}
}

// TestServe_RefusesWhatRegisterRefuses proves an external method is held
// to exactly the rules a built-in one is, because it goes through the same
// collection.Register: each descriptor here would be refused as a
// built-in, and each is refused here with Register's own reason on
// stderr and exit code 2, before anything is described or run.
func TestServe_RefusesWhatRegisterRefuses(t *testing.T) {
	var calls callCounter
	valid := func(name string) collection.Descriptor { return countingDescriptor(name, true, &calls) }

	cases := []struct {
		name   string
		descs  []collection.Descriptor
		reason string
	}{
		{
			name:   "bare name",
			descs:  []collection.Descriptor{valid("motd")},
			reason: "is not namespaced",
		},
		{
			name: "declares check support with no Check function",
			descs: []collection.Descriptor{func() collection.Descriptor {
				d := valid("acme.check.missing")
				d.Check = nil
				return d
			}()},
			reason: "declares check support but carries no Check function",
		},
		{
			name: "carries a Check function without declaring support",
			descs: []collection.Descriptor{func() collection.Descriptor {
				d := valid("acme.check.undeclared")
				d.Manifest.SupportsCheck = false
				return d
			}()},
			reason: "does not declare check support",
		},
		{
			name: "unknown capability",
			descs: []collection.Descriptor{func() collection.Descriptor {
				d := valid("acme.caps.unknown")
				d.Manifest.RequiredCapabilities = []capability.Name{"TeleportCapable"}
				return d
			}()},
			reason: `requires unknown capability "TeleportCapable"`,
		},
		{
			name: "not reversible with no reason",
			descs: []collection.Descriptor{func() collection.Descriptor {
				d := valid("acme.undo.silent")
				d.Manifest.Reversibility = collection.Reversibility{}
				return d
			}()},
			reason: "not reversible with no Notes",
		},
		{
			name:   "the same name twice",
			descs:  []collection.Descriptor{valid("acme.twice.method"), valid("acme.twice.method")},
			reason: `duplicate registration for "acme.twice.method"`,
		},
	}
	for _, tc := range cases {
		for _, command := range []string{external.CommandDescribe, external.CommandInvoke} {
			t.Run(tc.name+"/"+command, func(t *testing.T) {
				frame := requestFrame(t, wire.ChildRequest{FQCN: tc.descs[0].Name, Mode: "check"})
				code, b := runServe(t, []string{command}, tc.descs, frame)
				if code != 2 {
					t.Errorf("Serve(%s) = %d, want 2", command, code)
				}
				if !strings.Contains(b.err.String(), tc.reason) {
					t.Errorf("stderr = %q, want Register's reason containing %q", b.err.String(), tc.reason)
				}
				if b.out.Len() != 0 || b.response.Len() != 0 {
					t.Errorf("a refused program wrote stdout %q or a response %q, want neither", b.out.String(), b.response.String())
				}
			})
		}
	}
	if calls.invoke != 0 || calls.check != 0 {
		t.Errorf("a refused program ran a method (Invoke %d, Check %d times)", calls.invoke, calls.check)
	}
}

// TestServe_InvokeWritesTheResponseOnTheResponseStream proves invoke reads
// the request from In and writes the method's response to Response, and to
// nothing else: stdout is for a person, and the frame never goes there.
// Both modes run, so the program's own lookup is shown to reach Check too.
func TestServe_InvokeWritesTheResponseOnTheResponseStream(t *testing.T) {
	cases := []struct {
		mode        string
		wantChanged bool
		wantRan     string
		wantInvoke  int
		wantCheck   int
	}{
		{mode: string(collection.ModeExecute), wantChanged: true, wantRan: "invoke", wantInvoke: 1},
		{mode: string(collection.ModeCheck), wantChanged: false, wantRan: "check", wantCheck: 1},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			var calls callCounter
			desc := countingDescriptor("acme.motd.set", true, &calls)

			code, b := runServe(t, []string{external.CommandInvoke}, []collection.Descriptor{desc},
				requestFrame(t, wire.ChildRequest{FQCN: desc.Name, Mode: tc.mode}))
			if code != 0 {
				t.Fatalf("Serve(invoke) = %d, want 0 (stderr: %s)", code, b.err.String())
			}

			var resp wire.ChildResponse
			if err := json.NewDecoder(&b.response).Decode(&resp); err != nil {
				t.Fatalf("decoding the response frame: %v", err)
			}
			if resp.Error != "" {
				t.Fatalf("response.Error = %q, want empty", resp.Error)
			}
			if resp.Changed != tc.wantChanged || resp.Facts["ran"] != tc.wantRan {
				t.Errorf("response = %+v, want Changed %v and ran=%s", resp, tc.wantChanged, tc.wantRan)
			}
			if calls.invoke != tc.wantInvoke || calls.check != tc.wantCheck {
				t.Errorf("Invoke ran %d times and Check %d, want %d and %d", calls.invoke, calls.check, tc.wantInvoke, tc.wantCheck)
			}
			if b.out.Len() != 0 || b.err.Len() != 0 {
				t.Errorf("invoke wrote stdout %q or stderr %q, want the frame on the response stream only", b.out.String(), b.err.String())
			}
		})
	}
}

// TestServe_InvokeOnlyReachesTheProgramsOwnMethods proves the lookup Serve
// builds is restricted to the methods the program passed it. A method
// registered in the same process by something else (a library the program
// imports, registering from its own init) is in the process-wide registry
// but must not become reachable through this program: the program
// described what it provides, and nothing else may be run under its name.
func TestServe_InvokeOnlyReachesTheProgramsOwnMethods(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())

	var libraryCalls callCounter
	library := countingDescriptor("thirdparty.library.method", true, &libraryCalls)
	if err := collection.Register(library); err != nil {
		t.Fatalf("registering the library's method: %v", err)
	}
	// Control: the method really is registered process-wide, so the
	// refusal below is the restriction, not an absence.
	if _, ok := collection.Lookup(library.Name); !ok {
		t.Fatal("control failed: the library's method is not in the process-wide registry")
	}

	var ownCalls callCounter
	own := countingDescriptor("acme.motd.set", true, &ownCalls)

	for _, mode := range []string{string(collection.ModeExecute), string(collection.ModeCheck)} {
		t.Run(mode, func(t *testing.T) {
			code, b := runServe(t, []string{external.CommandInvoke}, []collection.Descriptor{own},
				requestFrame(t, wire.ChildRequest{FQCN: library.Name, Mode: mode}))
			if code != 0 {
				t.Fatalf("Serve(invoke) = %d, want 0: a refused name is a response, not a broken exchange (stderr: %s)", code, b.err.String())
			}
			var resp wire.ChildResponse
			if err := json.NewDecoder(&b.response).Decode(&resp); err != nil {
				t.Fatalf("decoding the response frame: %v", err)
			}
			if !strings.Contains(resp.Error, `"thirdparty.library.method" is not registered`) {
				t.Errorf("response.Error = %q, want the library's method refused as not registered", resp.Error)
			}
		})
	}
	if libraryCalls.invoke != 0 || libraryCalls.check != 0 {
		t.Errorf("the library's method ran through the program (Invoke %d, Check %d times)", libraryCalls.invoke, libraryCalls.check)
	}
	if ownCalls.invoke != 0 || ownCalls.check != 0 {
		t.Errorf("the program's own method ran for a request naming another (Invoke %d, Check %d times)", ownCalls.invoke, ownCalls.check)
	}
}

// TestServe_BrokenStreamsExitOne covers the two ways the exchange itself
// breaks after the program was found servable: a description that cannot
// be written, and an invoke request that cannot be read. Both are exit
// code 1, distinct from a program that cannot serve at all (2) and from a
// method's own failure (0 with an Error in the response).
func TestServe_BrokenStreamsExitOne(t *testing.T) {
	var calls callCounter
	desc := countingDescriptor("acme.motd.set", true, &calls)

	t.Run("describe cannot write stdout", func(t *testing.T) {
		t.Cleanup(collection.SnapshotForTest())
		var errOut bytes.Buffer
		code := external.Serve(context.Background(), []string{external.CommandDescribe}, []collection.Descriptor{desc},
			external.Streams{In: bytes.NewReader(nil), Out: errWriter{}, Err: &errOut, Response: &bytes.Buffer{}})
		if code != 1 {
			t.Errorf("Serve(describe) = %d, want 1 when stdout cannot be written", code)
		}
		if !strings.Contains(errOut.String(), "failed to write the description") {
			t.Errorf("stderr = %q, want the write failure reported", errOut.String())
		}
	})

	t.Run("invoke cannot read its request", func(t *testing.T) {
		code, b := runServe(t, []string{external.CommandInvoke}, []collection.Descriptor{desc}, []byte("{not json"))
		if code != 1 {
			t.Errorf("Serve(invoke) = %d, want 1 for a malformed request", code)
		}
		if b.response.Len() != 0 {
			t.Errorf("response frame = %q, want nothing written when the request never decoded", b.response.String())
		}
		if b.err.Len() == 0 {
			t.Error("stderr is empty, want the decode failure reported")
		}
	})

	if calls.invoke != 0 || calls.check != 0 {
		t.Errorf("a broken exchange ran the method (Invoke %d, Check %d times)", calls.invoke, calls.check)
	}
}
