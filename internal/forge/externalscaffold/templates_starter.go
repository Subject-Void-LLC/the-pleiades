// Package externalscaffold: the templates for a generated program's test,
// README and go.mod.
package externalscaffold

// methodTestTemplateSource is the program's starter test. It drives
// external.ServeProgram, the program's real serving path, over buffers rather
// than calling the descriptor function and inspecting it, because Serve
// is what runs collection.Register and prints the describe document
// The Pleiades reads. A test that only looked at the struct would pass for a
// descriptor that Register refuses.
//
// Every case restores the process-wide registry afterward
// (collection.SnapshotForTest): Serve registers each method it is given,
// so a second Serve call in the same process would otherwise fail on a
// duplicate name.
const methodTestTemplateSource = `package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Test{{.FunctionName}}Serve runs this program's own serving path,
// external.ServeProgram, over buffers, for each command The Pleiades can
// send it.
//
// The describe case matters most. Serve registers every method through
// collection.Register before it answers, so a clean describe proves the
// descriptor passes the same validation a built-in method does, and the
// document it prints is exactly what The Pleiades reads when it loads this
// program.
func Test{{.FunctionName}}Serve(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		request  *wire.ChildRequest
		wantCode int
		check    func(t *testing.T, out, response []byte)
	}{
		{
			name:     "describe names this method and its contract",
			args:     []string{external.CommandDescribe},
			wantCode: 0,
			check: func(t *testing.T, out, _ []byte) {
				var desc external.Description
				if err := json.Unmarshal(out, &desc); err != nil {
					t.Fatalf("describe printed something that is not a Description: %v\n%s", err, out)
				}
				if desc.Protocol != external.ProtocolVersion {
					t.Errorf("Protocol = %d, want %d", desc.Protocol, external.ProtocolVersion)
				}
				if len(desc.Methods) != {{if .Capability}}2{{else}}1{{end}} || desc.Methods[0].Name != {{.Prefix}}FQCN {
					t.Fatalf("Methods = %+v, want %q first", desc.Methods, {{.Prefix}}FQCN)
				}
{{- if .Capability}}
				if len(desc.Capabilities) != 1 || desc.Capabilities[0].Name != {{.Prefix}}CapabilityName {
					t.Errorf("Capabilities = %+v, want exactly %q", desc.Capabilities, {{.Prefix}}CapabilityName)
				}
{{- end}}
				manifest := desc.Methods[0].Manifest
				if manifest.Status != collection.StatusImplemented {
					t.Errorf("Status = %q, want %q", manifest.Status, collection.StatusImplemented)
				}
{{- if .ScriptFile}}
				if manifest.SupportsCheck || manifest.NoCheckReason == "" {
					t.Error("SupportsCheck is true or NoCheckReason is empty: a script's wrapper cannot check, and says why")
				}
				if manifest.ExecutionContext.Device != collection.DeviceNone {
					t.Errorf("Device = %q, want %q: the script acts on no device", manifest.ExecutionContext.Device, collection.DeviceNone)
				}
{{- else}}
				if !manifest.SupportsCheck {
					t.Error("SupportsCheck = false, want true: update this when you turn check support off")
				}
{{- end}}
			},
		},
		{
			name:     "invoke answers an unknown method with an error response",
			args:     []string{external.CommandInvoke},
			request:  &wire.ChildRequest{FQCN: "no.such.method"},
			wantCode: 0,
			check: func(t *testing.T, _, response []byte) {
				var resp wire.ChildResponse
				if err := json.Unmarshal(response, &resp); err != nil {
					t.Fatalf("invoke wrote something that is not a ChildResponse: %v\n%s", err, response)
				}
				if !strings.Contains(resp.Error, "not registered") {
					t.Errorf("Error = %q, want it to say the method is not registered", resp.Error)
				}
			},
		},
		{
			name:     "no command is refused",
			args:     nil,
			wantCode: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			t.Cleanup(capability.SnapshotForTest())

			var in bytes.Buffer
			if tt.request != nil {
				if err := json.NewEncoder(&in).Encode(tt.request); err != nil {
					t.Fatalf("encoding the request: %v", err)
				}
			}

			var out, errOut, response bytes.Buffer
			code := external.ServeProgram(context.Background(), tt.args, program(),
				external.Streams{In: &in, Out: &out, Err: &errOut, Response: &response})
			if code != tt.wantCode {
				t.Fatalf("Serve(%q) exited %d, want %d; stderr:\n%s", tt.args, code, tt.wantCode, errOut.String())
			}
			if tt.check != nil {
				tt.check(t, out.Bytes(), response.Bytes())
			}
		})
	}
}
`

// readmeTemplateSource is the program's README.md: what the program is,
// how to build it, and where to install it. It uses ~~~ fences rather
// than backtick fences because this whole template is a Go raw string,
// which cannot contain a backtick; {{code}} supplies inline code spans
// for the same reason.
const readmeTemplateSource = `# {{.Name}}

An external Collection for The Pleiades that provides one method,
{{code .Name}}. Generated by {{code "pleiades forge new-external"}}.

{{if .ScriptFile -}}
Pleiades runs this program as a child process beside itself, on the
machine running {{code "pleiades run"}}. It is never copied onto a managed
device. Its method runs the script {{code .ScriptFile}}, embedded in the
program, with {{code .Interpreter}}.

## The script

Each run writes the script into the run's private temporary directory and
runs it there, with the task's params as one JSON document on stdin. The
script answers by printing exactly one JSON object on stdout:

~~~json
{"changed": true, "stats": {"lines": 3}, "facts": {"seen": "yes"}}
~~~

{{code "changed"}} is required, true or false. {{code "stats"}} and
{{code "facts"}} are optional objects, and every key in either is a value
the task's register reads. The method acts on no device, so no fact reaches
a device's fact store. Anything else on stdout fails the task, and so does
printing nothing. A non-zero exit fails the task too, reporting the start of
stderr, masked.

Declare every parameter the script reads in {{code (printf "%s.go" .FileBase)}}
({{code "Params"}}), or {{code "pleiades validate"}} refuses a task that
passes it.

The method acts on no device, so a task calling it needs no
{{code "hosts:"}}, and it is never handed a credential: none reaches the
script, on stdin, in its arguments or in its environment, which holds only
{{code "HOME"}} and {{code "TMPDIR"}} (both the run's own directory) and
{{code "PATH"}}, {{code "LANG"}}, {{code "LC_ALL"}} and {{code "TZ"}}. It
supports no check mode, since a wrapper cannot tell a script that only
reads from one that writes, and records no undo.

Two limits are worth knowing before you approve a build:

- The interpreter is outside the approved digest. The digest covers the
  script, so a changed script is a new build to approve, but whatever is at
  {{code .Interpreter}} on the machine running The Pleiades runs it.
- The confinement The Pleiades runs the program under restricts files, not
  the network. The script can read only what the program can, but it can
  connect anywhere the machine can.
{{- else -}}
Pleiades runs this program as a child process beside itself, on the
machine running {{code "pleiades run"}} or on a Runner. It is never copied
onto a managed device. The method reaches its target over SSH, with the one
credential The Pleiades hands it for each task.

As generated, the method runs {{code "uname -a"}} on the target and records
the output as the {{code "uname"}} stat. Replace the body in
{{code (printf "%s.go" .FileBase)}} with the method's real work. Read the
comments on {{code "SupportsCheck"}} and {{code "Reversibility"}} there
first: both answers hold only while the body writes nothing.
{{- end}}

## Build

This directory is its own Go module, {{code .BinaryName}}. Its
{{code "go.mod"}} names no version of The Pleiades: {{code "go mod tidy"}} finds
the newest one through the Go module proxy and records its checksum in
{{code "go.sum"}}, so what you build against is verified.

~~~sh
go mod tidy
go test ./...
CGO_ENABLED=0 GOTOOLCHAIN={{.Toolchain}} go build -trimpath -buildvcs=false -o {{.BinaryName}} .
~~~

Pleiades has no tagged release yet, so {{code "go mod tidy"}} takes the
newest commit on its main branch. To pin another, run
{{code "go get github.com/Subject-Void-LLC/the-pleiades@<commit>"}} first.
To give the module a path of your own, run
{{code (printf "go mod edit -module example.com/you/%s" .BinaryName)}}.
If The Pleiades is private to your organization, set {{code "GOPRIVATE"}} to its
module path and give the go command credentials for it.

Offline, or to build against a local checkout of The Pleiades, point the
module at the checkout. An offline {{code "go mod tidy"}} reads every module
the checkout's packages and their tests use from the module cache, so fill
it from the checkout once while still online:

~~~sh
cd /path/to/the-pleiades && go mod download
~~~

Then, in this directory:

~~~sh
go mod edit -replace github.com/Subject-Void-LLC/the-pleiades=/path/to/the-pleiades
go mod tidy
go test ./...
CGO_ENABLED=0 GOTOOLCHAIN={{.Toolchain}} go build -trimpath -buildvcs=false -o {{.BinaryName}} .
~~~

A replace is not checked against any checksum: whatever is at that path is
compiled in. Point it only at a checkout you trust, and remove it
({{code "go mod edit -dropreplace github.com/Subject-Void-LLC/the-pleiades"}})
before you share the program. Offline, the Go release
{{code "GOTOOLCHAIN"}} names must already be on this machine, as its own
{{code "go"}} or downloaded by an earlier build.

Build for the machine The Pleiades runs on, not for your devices. Set
{{code "GOOS"}} and {{code "GOARCH"}} when those differ from the machine you
build on.

### The same source, the same digest

The build line is reproducible: this directory, built with it, gives the
same bytes, and so the same SHA-256 digest, in any directory and on any
machine building for the same {{code "GOOS"}} and {{code "GOARCH"}}. That is
what lets someone else check that an approved digest came from this source,
by building it and comparing. Each setting removes one thing that otherwise
differs between two builds:

- {{code "-trimpath"}} leaves out the directories the source and the Go module
  cache sit in.
- {{code "-buildvcs=false"}} leaves out the version control state, which
  differs between a clone, an archive and an uncommitted edit.
- {{code "CGO_ENABLED=0"}} leaves out the C toolchain, which the Go command
  otherwise uses only on machines that have one.
- {{code (printf "GOTOOLCHAIN=%s" .Toolchain)}} builds with the Go release the
  {{code "toolchain"}} line in {{code "go.mod"}} names, the one The Pleiades
  itself is built with. That line alone is a minimum, so a newer Go would
  otherwise build with itself.

The source is everything in this directory, {{code "go.mod"}} and
{{code "go.sum"}} included, so the build reproduces only from the same
{{code "go.sum"}}. A build through a {{code "replace"}} records the path the
{{code "replace"}} names, so it reproduces only against a checkout at that
same path; build what you share without one. Any other flag or variable
that reaches the build ({{code "-ldflags"}}, {{code "-tags"}},
{{code "GOAMD64"}}, {{code "GOFLAGS"}}) is part of the build too, and must be
the same on both sides. To compare, run
{{code (printf "sha256sum %s" .BinaryName)}}: its hex is the digest
{{code "pleiades collection approve"}} shows after {{code "sha256:"}}.

## Install

Put the binary in the directory named by the
{{code "PLEIADES_COLLECTIONS_DIR"}} environment variable, on each machine
that runs The Pleiades: the one running {{code "pleiades run"}}, and every
Runner.

~~~sh
mkdir -p "$PLEIADES_COLLECTIONS_DIR"
chmod 755 "$PLEIADES_COLLECTIONS_DIR"
install -m 755 {{.BinaryName}} "$PLEIADES_COLLECTIONS_DIR/"
pleiades collection approve {{.BinaryName}}
~~~

Nothing in the directory runs until its exact build is approved. The
approve command shows the build's digest and the methods it provides,
and asks first. Every rebuild is a new build to approve; on a Runner,
approve the build by digest where the image is built
({{code "pleiades collection approve <program> --digest sha256:<hex>"}}).

That directory must be owned by you, the user The Pleiades runs as, and must
not be writable by group or others. Whoever can write to it can run code
that receives device credentials.

Pleiades runs the program confined, on Linux through Landlock: it can
read its own directory, the system's libraries and certificates, the
known_hosts file and a private temporary directory, and nothing else on
the local disk. If the method needs a local file, grant its directory
with {{code "PLEIADES_COLLECTIONS_READ_PATHS"}}.

Once installed, {{code .Name}} is usable from any runbook, and
{{code "pleiades validate"}} and {{code "pleiades doc"}} see it like any
built-in method:

~~~sh
pleiades doc {{.Name}}
~~~

~~~yaml
- name: Read the kernel details
  {{.Name}}:
  register: details
~~~

{{- if .Capability}}

## The capability

This program defines {{code .Capability}}, a capability The Pleiades knows
only because this program describes it, and {{code .Name}} requires it. A
device holds it once it is granted, in one of two ways:

~~~sh
# its probe, {{.ProbeName}}, decides, and the grant records that it did
pleiades onboard <device>

# or an operator asserts it, with a reason the grant records
pleiades grant-capability <device> {{.Capability}} --set kernel=Linux --reason "why it holds"
~~~

Change its properties to what your methods need to know about a device
(in {{code (printf "%s.go" .CapabilityFile)}}), and the probe to how you
find them out. No property may be a secret or an address.
{{- end}}

## Test

{{code "go test ./..."}} runs the program's own describe check. It passes
only if the method clears the same validation The Pleiades applies to every
method, built-in or external.
`
