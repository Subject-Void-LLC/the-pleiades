//go:build unix

// Package loader: what an external program may reach while it runs, and
// the private scratch directory each run gets.
package loader

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// accessClass is one kind of access a confinement rule grants beneath a
// path. The platform file turns each into the kernel's own access bits.
type accessClass int

const (
	// readExec is reading, listing and executing: the program's own
	// directory, and the system directories its libraries and tools live
	// in.
	readExec accessClass = iota

	// readOnly is reading and listing, nothing else: certificates, the
	// resolver's files, the known_hosts file.
	readOnly

	// device is reading and writing an existing device file, such as
	// /dev/null, without being able to create or remove anything.
	device

	// readWrite is everything: the run's private scratch directory.
	readWrite
)

// confineRule grants one access class beneath one path. A required rule
// whose path cannot be opened refuses the run; an optional one is
// skipped, since not every system has every directory.
type confineRule struct {
	path     string
	access   accessClass
	required bool
}

// systemRules is what every program may reach on any host: the places a
// program's own code, its dynamic libraries, the tools it shells out to,
// certificates, name resolution and the time zone database live. Nothing
// here holds a credential, and nothing here is writable except /dev/null.
//
// It is deliberately a list of files and narrow directories under /etc
// rather than all of /etc, which on a Runner host can hold a service's
// own configuration and secrets.
var systemRules = []confineRule{
	{path: "/usr", access: readExec},
	{path: "/bin", access: readExec},
	{path: "/sbin", access: readExec},
	{path: "/lib", access: readExec},
	{path: "/lib32", access: readExec},
	{path: "/lib64", access: readExec},
	{path: "/libx32", access: readExec},
	{path: "/etc/ld.so.cache", access: readOnly},
	{path: "/etc/ld.so.conf", access: readOnly},
	{path: "/etc/ld.so.conf.d", access: readOnly},
	{path: "/etc/ssl", access: readOnly},
	{path: "/etc/pki", access: readOnly},
	{path: "/etc/ca-certificates", access: readOnly},
	{path: "/etc/resolv.conf", access: readOnly},
	{path: "/run/systemd/resolve", access: readOnly},
	{path: "/etc/hosts", access: readOnly},
	{path: "/etc/host.conf", access: readOnly},
	{path: "/etc/nsswitch.conf", access: readOnly},
	{path: "/etc/gai.conf", access: readOnly},
	{path: "/etc/services", access: readOnly},
	{path: "/etc/protocols", access: readOnly},
	{path: "/etc/passwd", access: readOnly},
	{path: "/etc/group", access: readOnly},
	{path: "/etc/localtime", access: readOnly},
	{path: "/etc/timezone", access: readOnly},
	{path: "/dev/zero", access: readOnly},
	{path: "/dev/urandom", access: readOnly},
	{path: "/dev/random", access: readOnly},
	{path: "/dev/null", access: device},
}

// confinementRules is the full rule set for one run of a program in
// programDir: the system rules, the program's own directory, the
// known_hosts file its SSH connections verify against, the run's scratch
// directory, and every path the operator granted.
//
// The home directory, the project and every credential store are absent
// by omission, which is the point: a program reaches the credential it is
// handed on stdin and nothing else a credential is kept in.
func confinementRules(programDir, scratch string, grants []string) []confineRule {
	rules := append([]confineRule(nil), systemRules...)
	rules = append(rules,
		confineRule{path: programDir, access: readExec, required: true},
		confineRule{path: scratch, access: readWrite, required: true},
	)
	// The one file the program needs from inside a home directory. A
	// missing file is not this function's business: the program's SSH
	// connection refuses it with its own, clearer error.
	if knownHosts, err := remoteexec.KnownHostsFile(); err == nil {
		rules = append(rules, confineRule{path: knownHosts, access: readOnly})
	}
	for _, g := range grants {
		rules = append(rules, confineRule{path: g, access: readOnly, required: true})
	}
	return rules
}

// checkReach refuses a directory the program would be allowed to read
// when it would expose what confinement exists to hide: the collections
// directory itself, and every path in Options.ReadPaths.
//
// A path is refused when it is, contains, or sits inside any protected
// path (a credential store), and when it is or contains the home
// directory. A path inside the home directory is fine, since most
// operators keep their work there. Each path must be absolute and must
// exist, and is compared after resolving symbolic links, so a link cannot
// smuggle a protected directory in under another name.
func checkReach(root string, o Options) error {
	var protected []string
	for _, p := range o.ProtectedPaths {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			protected = append(protected, resolved)
		} else {
			protected = append(protected, filepath.Clean(p))
		}
	}
	home := ""
	if h, err := os.UserHomeDir(); err == nil {
		if resolved, err := filepath.EvalSymlinks(h); err == nil {
			home = resolved
		}
	}

	check := func(what, path string) error {
		for _, p := range protected {
			if within(path, p) || within(p, path) {
				return fmt.Errorf("%s %s overlaps %s, which holds credentials, so a program would be able to read them", what, path, p)
			}
		}
		if home != "" && within(home, path) {
			return fmt.Errorf("%s %s is or contains the home directory %s, which a program must not be able to read", what, path, home)
		}
		return nil
	}

	if err := check("the collections directory", root); err != nil {
		return err
	}
	for _, g := range o.ReadPaths {
		if !filepath.IsAbs(g) {
			return fmt.Errorf("granted read path %q is not absolute", g)
		}
		resolved, err := filepath.EvalSymlinks(g)
		if err != nil {
			return fmt.Errorf("granted read path %s: %w", g, err)
		}
		if err := check("granted read path", resolved); err != nil {
			return err
		}
	}
	return nil
}

// resolvedGrants returns Options.ReadPaths with symbolic links resolved,
// the form checkReach approved and the kernel rules are built from.
func resolvedGrants(o Options) []string {
	grants := make([]string, 0, len(o.ReadPaths))
	for _, g := range o.ReadPaths {
		if resolved, err := filepath.EvalSymlinks(g); err == nil {
			grants = append(grants, resolved)
		}
	}
	return grants
}

// within reports whether path is parent or lies beneath it. Both must be
// clean and absolute.
func within(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// sandbox is one run of one program: the scratch directory it may write,
// the rules it starts under, and whether confinement applies at all.
type sandbox struct {
	scratch string
	rules   []confineRule
	abi     int
	off     bool
}

// newSandbox creates the run's private scratch directory, readable and
// writable by this user only, and the rules the program starts under.
// The caller must call close when the run is over.
func newSandbox(programPath string, o Options) (*sandbox, error) {
	scratch, err := os.MkdirTemp("", "pleiades-external-")
	if err != nil {
		return nil, fmt.Errorf("failed to create the program's scratch directory: %w", err)
	}
	rules := confinementRules(filepath.Dir(programPath), scratch, resolvedGrants(o))
	for _, w := range o.testWritable {
		rules = append(rules, confineRule{path: w, access: readWrite, required: true})
	}
	return &sandbox{scratch: scratch, rules: rules, abi: o.abi, off: o.unconfined}, nil
}

// env is the program's environment: scrubbedEnv, with TMPDIR pointing at
// the run's own scratch directory, the only place it may write.
func (s *sandbox) env() []string {
	env := make([]string, 0, len(allowedEnv))
	for _, kv := range scrubbedEnv() {
		if !strings.HasPrefix(kv, "TMPDIR=") {
			env = append(env, kv)
		}
	}
	return append(env, "TMPDIR="+s.scratch)
}

// start starts cmd confined to the sandbox's rules.
func (s *sandbox) start(cmd *exec.Cmd) error {
	if s.off {
		return cmd.Start()
	}
	return startConfined(cmd, s.rules, s.abi)
}

// close removes the scratch directory and everything the program left in
// it.
func (s *sandbox) close() {
	_ = os.RemoveAll(s.scratch)
}
