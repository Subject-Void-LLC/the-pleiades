// Package lockcheck verifies a lockfile against its manifest: every
// dependency the manifest declares is locked, every locked entry carries
// an integrity hash, and every entry comes from a source the caller
// allows. A lockfile format is a plug-in (Register); npm's package-lock.json
// and Cargo's Cargo.lock are built in. build.lock.verify is its caller.
package lockcheck

import (
	"fmt"
	"path"
	"sort"
	"sync"
)

// MaxFileBytes bounds a lockfile or manifest.
const MaxFileBytes = 32 << 20

// Finding is one way a lockfile falls short.
type Finding struct {
	// Package names the package, with its version where the lockfile has
	// one.
	Package string
	// Problem says what is wrong, for a person.
	Problem string
}

// Format is one lockfile format.
type Format interface {
	// Name names the format ("npm", "cargo").
	Name() string
	// LockFile and ManifestFile are the files' usual names, by which a
	// lockfile's format is recognized.
	LockFile() string
	ManifestFile() string
	// DefaultSources are the sources allowed when a caller names none.
	DefaultSources() []string
	// Verify checks lock against manifest, allowing entries from sources.
	// An error is a file this format cannot read (or a version it does not
	// support), never a finding.
	Verify(lock, manifest []byte, sources []string) ([]Finding, error)
}

// formats is every registered format, by name.
var formats = struct {
	sync.RWMutex
	byName map[string]Format
}{byName: map[string]Format{}}

// Register adds a format; registering a name twice panics, as a duplicate
// collection method does.
func Register(f Format) {
	formats.Lock()
	defer formats.Unlock()
	if _, dup := formats.byName[f.Name()]; dup {
		panic(fmt.Sprintf("lockcheck: format %q registered twice", f.Name()))
	}
	formats.byName[f.Name()] = f
}

// Lookup returns the format named name.
func Lookup(name string) (Format, bool) {
	formats.RLock()
	defer formats.RUnlock()
	f, ok := formats.byName[name]
	return f, ok
}

// ForLockFile returns the format whose lockfile is named like file.
func ForLockFile(file string) (Format, bool) {
	formats.RLock()
	defer formats.RUnlock()
	base := path.Base(file)
	for _, f := range formats.byName {
		if f.LockFile() == base {
			return f, true
		}
	}
	return nil, false
}

// Names is every registered format's name, sorted.
func Names() []string {
	formats.RLock()
	defer formats.RUnlock()
	names := make([]string, 0, len(formats.byName))
	for n := range formats.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// sorted orders findings by package, then problem, so a report reads the
// same every run.
func sorted(findings []Finding) []Finding {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Package != findings[j].Package {
			return findings[i].Package < findings[j].Package
		}
		return findings[i].Problem < findings[j].Problem
	})
	return findings
}

// allowed reports whether source is in sources.
func allowed(sources []string, source string) bool {
	for _, s := range sources {
		if s == source {
			return true
		}
	}
	return false
}

func init() {
	Register(npmFormat{})
	Register(cargoFormat{})
}
