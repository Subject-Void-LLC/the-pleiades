// Package externalscaffold: Config, the input to Generate.
package externalscaffold

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/genutil"
)

// Config is the input to Generate: everything needed to emit one external
// Collection program providing one method.
//
// It is deliberately just the name. The generated method's manifest is
// fixed by what its generated body does (it needs SSH and nothing else,
// it only reads, so it is not reversible and supports check), and a flag
// that let a caller declare anything else would produce a manifest that
// lies about the code beside it. An author changes the manifest when they
// change the body, in the same file.
type Config struct {
	// Name is the method's full dotted namespaced name, for example
	// "acme.motd.read". It follows exactly the rules `forge
	// new-collection` applies: at least one dot, and every segment a
	// lowercase identifier that is not a Go keyword. Keeping the two
	// commands' rules identical means a method can move between an
	// external program and the built-in catalog without being renamed.
	Name string

	// Engine is the version of The Pleiades build generating the program
	// (internal/buildinfo.Version), which decides the method's engine
	// version constraint: a release build states its own release as the
	// minimum, and a development build states none, since its 0.0.0
	// would mean nothing and any fixed guess (such as >=1.0.0) would be
	// refused by the first release that did not meet it. It is not a
	// choice a caller makes, so it is not a flag.
	Engine string

	// NoGoMod leaves out the program's go.mod, for a program placed inside
	// a module of the author's own that it should join. Without it the
	// program is a module of its own, which is what keeps one scaffolded
	// inside another checkout (this one included) out of that checkout's
	// ./... and its builds.
	NoGoMod bool
}

// segments splits Name on every dot.
func (c Config) segments() []string {
	return strings.Split(c.Name, ".")
}

// MethodSegment returns Name's final segment, for example "read" for
// "acme.motd.read".
func (c Config) MethodSegment() string {
	segs := c.segments()
	return segs[len(segs)-1]
}

// FunctionName returns the exported Go function name of the generated
// method body, for example "Read" for "acme.motd.read" and "DaemonReload"
// for "acme.svc.daemon_reload".
func (c Config) FunctionName() string {
	return genutil.ToExportedIdent(c.MethodSegment())
}

// identPrefix returns FunctionName with its first letter lowered, the
// prefix every unexported identifier in the generated method file carries
// ("readFQCN", "readCommand"). The prefix keeps a second method, added to
// the same program later, from colliding with the first one's constants.
// A suffix is always appended to it, so it never stands alone as a
// keyword such as "type" or "func".
func (c Config) identPrefix() string {
	fn := c.FunctionName()
	return strings.ToLower(fn[:1]) + fn[1:]
}

// FileBase returns the base name, without extension, of the generated
// method file and its test: the method segment with every underscore
// replaced by a hyphen, for example "daemon-reload" for
// "acme.svc.daemon_reload".
//
// The replacement is not cosmetic. The go command reads meaning into an
// underscore in a file name: "read_test.go" is a test file, and
// "read_linux.go" or "read_arm64.go" builds only on that platform. A
// method segment ending in "_test" would put the descriptor in a file the
// build never compiles, and one ending in a platform name would build on
// one machine and not another. A hyphen carries no such meaning, so no
// accepted method name can give its file a build constraint.
func (c Config) FileBase() string {
	return strings.ReplaceAll(c.MethodSegment(), "_", "-")
}

// DefaultDir returns the program directory `forge new-external` writes
// into when the caller names none: every segment of Name joined with a
// hyphen, for example "acme-motd-read". It is also the binary name the
// generated README builds, so the program on disk is recognizably the
// method it provides.
//
// A fresh directory named after the method, rather than the current one,
// is the default because the generated main.go is a package main. Written
// into a directory that already holds another Go package, it would break
// that package's build rather than start a new one.
func (c Config) DefaultDir() string {
	return strings.Join(c.segments(), "-")
}

// Validate reports whether c is safe to generate from. Name must have at
// least two segments, every segment must pass genutil.ValidateSegments
// (the same check `forge new-collection` applies), and the method segment
// must not be "main".
//
// "main" is refused because FileBase would then name the method file
// main.go, the same path as the program's entry point, and Generate would
// return two files at one path.
func (c Config) Validate() error {
	segs := c.segments()
	if len(segs) < 2 {
		return fmt.Errorf("externalscaffold: %q is not namespaced (requires <namespace>.<method>)", c.Name)
	}
	if err := genutil.ValidateSegments(segs); err != nil {
		return fmt.Errorf("externalscaffold: invalid name %q: %w", c.Name, err)
	}
	if c.MethodSegment() == "main" {
		return fmt.Errorf("externalscaffold: invalid name %q: the method segment %q would collide with the program's own main.go", c.Name, "main")
	}
	return nil
}
