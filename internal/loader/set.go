// Package loader: Set, the record of what one Load call registered.
package loader

import (
	"slices"
	"sort"
)

// Set is what one successful Load registered: which programs, pinned to
// which digests, provide which methods, and any warnings worth reading.
//
// A nil *Set is valid and owns nothing, so a caller with no directory
// configured can pass one around without a special case.
type Set struct {
	// programs is every loaded program, sorted by path.
	programs []Program

	// owners maps each registered method name to the path of the program
	// that provides it.
	owners map[string]string

	// warnings are the non-fatal findings Load recorded, in the order it
	// found them.
	warnings []string
}

// Program is one loaded external Collection program.
type Program struct {
	// Path is the program's absolute path, with every symlink in the
	// directory's own path already resolved.
	Path string

	// Digest is "sha256:" and the hex SHA-256 of the program's bytes when
	// it was loaded. Every call re-hashes the file and refuses to run it
	// if the two differ.
	Digest string

	// Methods are the fully-qualified names the program provides, sorted.
	Methods []string
}

// newSet builds a Set over programs, which are sorted by path and have
// their method lists sorted, so every accessor's order is stable.
func newSet(programs []Program, warnings []string) *Set {
	s := &Set{owners: map[string]string{}, warnings: warnings}
	for _, p := range programs {
		p.Methods = slices.Clone(p.Methods)
		sort.Strings(p.Methods)
		for _, m := range p.Methods {
			s.owners[m] = p.Path
		}
		s.programs = append(s.programs, p)
	}
	sort.Slice(s.programs, func(i, j int) bool { return s.programs[i].Path < s.programs[j].Path })
	return s
}

// Owns reports whether fqcn is a method this Set registered. It is what
// the Runner asks to decide that a method already crosses a process
// boundary of its own (internal/adapters/native.WithExternalCollections).
func (s *Set) Owns(fqcn string) bool {
	if s == nil {
		return false
	}
	_, ok := s.owners[fqcn]
	return ok
}

// Programs returns every loaded program, sorted by path. The slice and
// each program's method list are copies the caller may keep or change.
func (s *Set) Programs() []Program {
	if s == nil {
		return nil
	}
	out := make([]Program, len(s.programs))
	for i, p := range s.programs {
		p.Methods = slices.Clone(p.Methods)
		out[i] = p
	}
	return out
}

// Warnings returns what Load accepted but wants a person to know, such as
// an engine version constraint an unreleased build could not evaluate.
func (s *Set) Warnings() []string {
	if s == nil {
		return nil
	}
	return slices.Clone(s.warnings)
}
