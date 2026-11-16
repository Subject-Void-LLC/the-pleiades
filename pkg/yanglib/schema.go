// Package yanglib: where a server serves each module's YANG source from, as
// its library names it, for a client that reads the sources themselves
// (net.yang.schema). RFC 8525's library names a module's source by its
// location leaf-list and RFC 7895's by its schema leaf (which RFC 8040 section
// 3.7 has a RESTCONF client read); both list each module's submodules beside
// it, and the modules the server lists for their definitions only
// (import-only), whose sources a reader of the implemented modules needs for
// their types. NETCONF fetches a source by name and revision instead (RFC
// 6022's get-schema), so a NETCONF client reads the same entries for what to
// ask for. Nothing here reaches ModulesDigest: where a module is served from
// is no part of what it is.
package yanglib

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The bounds on what a Schema keeps.
const (
	// MaxLocations is the most locations one entry may name; an entry naming
	// more is refused whole.
	MaxLocations = 8
	// MaxLocationBytes bounds one location.
	MaxLocationBytes = 2048
)

// Schema is one YANG module or submodule a server's library lists, and where
// its source can be fetched from.
type Schema struct {
	// Name is the module's or submodule's name.
	Name string
	// Revision is its revision date, empty when the library names none.
	Revision string
	// Locations are the URLs the library names for its source, as written:
	// RFC 8525's location leaf-list or RFC 7895's schema leaf. Empty when it
	// names none, which a server may (retrieval is optional, RFC 8040 section
	// 3.7). Whose origin a location names is the fetching client's question.
	Locations []string
	// ImportOnly marks a module the server lists for its definitions only:
	// RFC 8525's import-only-module, RFC 7895's conformance-type import.
	ImportOnly bool
	// Submodules are a module's submodules, each with its own locations;
	// always empty on a submodule.
	Submodules []Schema
}

// Key is the entry's name and revision as one word, name@revision, or the
// name alone for an entry with no revision: RFC 6020 section 5.2's form, and
// how a task names a module.
func (s Schema) Key() string {
	if s.Revision == "" {
		return s.Name
	}
	return s.Name + "@" + s.Revision
}

// MaxFileNameBytes is the longest file name a saved source may have: the
// most one name may be on Linux, macOS and Windows alike. An identifier may
// be 255 bytes, so its name@revision.yang may not fit.
const MaxFileNameBytes = 255

// windowsDevices are the names Windows takes for a device rather than a
// file, whatever follows them after a dot and in any case.
var windowsDevices = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// IsWindowsDevice reports whether a file named name would be one of
// Windows' device names (nul, con, com1, ...), which it takes for that
// device whatever follows a dot and in any case.
func IsWindowsDevice(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	return windowsDevices[strings.ToLower(base)]
}

// FileName is the file the entry's source is saved as: RFC 6020 section
// 5.2's name@revision.yang, or name.yang for an entry with no revision. It is
// built only from a YANG identifier and a revision date, so it holds no path
// separator, no "..", and nothing a device chose but those two words; anything
// else is an error rather than a name, and so is a name longer than
// MaxFileNameBytes or one Windows reads as a device (nul.yang).
func (s Schema) FileName() (string, error) {
	if !IsIdentifier(s.Name) {
		return "", fmt.Errorf("%q is not a YANG identifier, so it names no file", s.Name)
	}
	if s.Revision != "" && !yangRevision.MatchString(s.Revision) {
		return "", fmt.Errorf("%q is not a revision date, so it names no file", s.Revision)
	}
	name := s.Key() + ".yang"
	switch {
	case len(name) > MaxFileNameBytes:
		return "", fmt.Errorf("%s would be saved as a file name of %d bytes, more than the %d a file system takes", s.Key(), len(name), MaxFileNameBytes)
	case IsWindowsDevice(name):
		return "", fmt.Errorf("%s would be saved as %s, which Windows takes for a device, not a file", s.Key(), name)
	}
	return name, nil
}

// ParseKey reads name or name@revision, as a task names a module, holding
// the name to the identifier grammar and the revision to a date.
func ParseKey(key string) (name, revision string, err error) {
	name, revision, _ = strings.Cut(strings.TrimSpace(key), "@")
	switch {
	case !IsIdentifier(name):
		return "", "", fmt.Errorf("%q does not name a module: a module is a YANG identifier, optionally followed by @ and a revision date", key)
	case strings.Contains(key, "@") && !yangRevision.MatchString(revision):
		return "", "", fmt.Errorf("%q does not name a revision: write it as a date, name@2024-07-01", key)
	}
	return name, revision, nil
}

// schema is a library module entry as a Schema.
func (m libraryModule) schema(importOnly bool) Schema {
	s := Schema{Name: strings.TrimSpace(m.Name), Revision: strings.TrimSpace(m.Revision), Locations: m.Locations, ImportOnly: importOnly}
	for _, sub := range m.Submodules {
		s.Submodules = append(s.Submodules, Schema{Name: strings.TrimSpace(sub.Name), Revision: strings.TrimSpace(sub.Revision), Locations: sub.Locations})
	}
	return s
}

// schema is a modules-state entry as a Schema, its one schema leaf its one
// location.
func (m stateModule) schema(importOnly bool) Schema {
	s := Schema{Name: strings.TrimSpace(m.Name), Revision: strings.TrimSpace(m.Revision), Locations: oneLocation(m.Schema), ImportOnly: importOnly}
	for _, sub := range m.Submodules {
		s.Submodules = append(s.Submodules, Schema{Name: strings.TrimSpace(sub.Name), Revision: strings.TrimSpace(sub.Revision), Locations: oneLocation(sub.Schema)})
	}
	return s
}

// oneLocation is a schema leaf's location as a list: none for an empty one.
func oneLocation(schema string) []string {
	if schema = strings.TrimSpace(schema); schema == "" {
		return nil
	}
	return []string{schema}
}

// errBadSchema is an entry KeepSchemas refuses.
var errBadSchema = errors.New("malformed schema entry")

// KeepSchemas is schemas with every malformed entry refused whole, duplicates
// removed (the first listing of one name and revision wins), sorted by name
// then revision. An entry is refused when its name is not a YANG identifier,
// its revision (when given) not a date, one of its locations empty, not
// printable or longer than MaxLocationBytes, when it names more than
// MaxLocations, or lists more than MaxListEntries submodules, or one of its
// submodules is refused by the same rules; past MaxModules every further
// entry is refused. A refused entry names no source a client could fetch, so
// a task naming it is told the library does not list it.
func KeepSchemas(schemas []Schema) []Schema {
	seen := map[string]bool{}
	var out []Schema
	for _, s := range schemas {
		if len(out) == MaxModules {
			break
		}
		kept, err := keepSchema(s, true)
		if err != nil || seen[kept.Key()] {
			continue
		}
		seen[kept.Key()] = true
		out = append(out, kept)
	}
	sortSchemas(out)
	return out
}

// keepSchema is s held to KeepSchemas' rules, its locations trimmed and its
// submodules sorted; module says whether s may have submodules.
func keepSchema(s Schema, module bool) (Schema, error) {
	if !IsIdentifier(s.Name) || (s.Revision != "" && !yangRevision.MatchString(s.Revision)) || len(s.Locations) > MaxLocations {
		return Schema{}, errBadSchema
	}
	locations := make([]string, 0, len(s.Locations))
	for _, l := range s.Locations {
		l = strings.TrimSpace(l)
		if l == "" || len(l) > MaxLocationBytes || !Printable(l) {
			return Schema{}, errBadSchema
		}
		locations = append(locations, l)
	}
	out := Schema{Name: s.Name, Revision: s.Revision, Locations: locations, ImportOnly: s.ImportOnly}
	if len(s.Submodules) == 0 {
		return out, nil
	}
	if !module || len(s.Submodules) > MaxListEntries {
		return Schema{}, errBadSchema
	}
	seen := map[string]bool{}
	for _, sub := range s.Submodules {
		// Handed over whole, so a submodule claiming submodules of its own
		// is refused, and its module with it; ImportOnly means nothing on a
		// submodule and is dropped.
		sub.ImportOnly = false
		kept, err := keepSchema(sub, false)
		if err != nil {
			return Schema{}, err
		}
		if !seen[kept.Key()] {
			seen[kept.Key()] = true
			out.Submodules = append(out.Submodules, kept)
		}
	}
	sortSchemas(out.Submodules)
	return out, nil
}

// sortSchemas sorts schemas by name, then revision.
func sortSchemas(schemas []Schema) {
	sort.Slice(schemas, func(i, j int) bool {
		if schemas[i].Name != schemas[j].Name {
			return schemas[i].Name < schemas[j].Name
		}
		return schemas[i].Revision < schemas[j].Revision
	})
}
