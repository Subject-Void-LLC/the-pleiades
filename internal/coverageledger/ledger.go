// Package coverageledger reads the collection coverage ledger: for each
// collection, the operations its endpoint's own contract defines (an OpenAPI
// document, a CLI reference), which method implements each one or why it is
// out of scope, and how each method is proven on each OS.
//
// A collection's ledger is one directory under coverage/collections, named for
// the collection's FQCN prefix (virt.clab, pkg.apt):
//
//	contract.json   the operations, extracted from the contract, with where
//	                the contract came from (URL, version, sha256)
//	map.yaml        hand-written: each operation to a method or an exclusion
//	                with its reason, and each method's proof per OS
//	source/         the vendored contract, when its license allows it, so the
//	                extraction can be checked offline
//
// This package only reads and computes. The repository-wide checks (that a
// mapped method is registered, that a proof's test exists and reaches the
// method, that no collection drops below its floor) live in internal/archtest,
// and the generated pages in tools/gendocs.
package coverageledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"
)

// CollectionsDir is where every collection's ledger lives, relative to the
// repository root.
const CollectionsDir = "coverage/collections"

// SourceKind says what sort of contract a collection's operations come from.
type SourceKind string

const (
	// SourceOpenAPI is an OpenAPI (or Swagger 2) document; its operations are
	// extracted by ExtractOpenAPI, from the vendored file in source/.
	SourceOpenAPI SourceKind = "openapi"
	// SourceCLI is a command-line tool's reference (a man page); its
	// operations are the tool's commands, listed by hand from the file whose
	// digest the contract records.
	SourceCLI SourceKind = "cli"
)

// Source says where a contract came from, precisely enough to fetch the same
// bytes again and to tell when upstream has moved.
type Source struct {
	// Kind is the sort of contract.
	Kind SourceKind `json:"kind"`
	// URL is where the contract file is published, at the revision named.
	URL string `json:"url"`
	// Version is the product version the contract describes.
	Version string `json:"version"`
	// Revision is the upstream commit or tag the file was taken from.
	Revision string `json:"revision,omitempty"`
	// SHA256 is the hex digest of the contract file's bytes.
	SHA256 string `json:"sha256"`
	// File is the vendored copy, relative to the collection's directory, or
	// empty when the contract is not vendored.
	File string `json:"file,omitempty"`
	// License is the contract file's license, which decides whether it may be
	// vendored.
	License string `json:"license,omitempty"`
}

// Op is one operation of a contract: "GET /api/v1/labs" for an API, a command
// name for a CLI.
type Op struct {
	// ID names the operation, unique within its contract.
	ID string `json:"id"`
	// Summary is the contract's own one-line description.
	Summary string `json:"summary,omitempty"`
}

// Contract is a collection's contract.json.
type Contract struct {
	// Collection is the FQCN prefix every method of the collection starts
	// with, and the directory's name.
	Collection string `json:"collection"`
	// Title names the endpoint for a reader.
	Title string `json:"title"`
	// Source is where the operations came from.
	Source Source `json:"source"`
	// Ops are every operation the contract defines, in its order.
	Ops []Op `json:"ops"`
}

// Ledger is one collection's contract and mapping, as read from its
// directory.
type Ledger struct {
	// Dir is the directory the ledger was read from.
	Dir string
	// Contract is contract.json.
	Contract Contract
	// Map is map.yaml.
	Map Map
}

// LoadAll reads every collection's ledger under root (a repository root),
// ordered by collection.
func LoadAll(root string) ([]Ledger, error) {
	entries, err := os.ReadDir(filepath.Join(root, CollectionsDir))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", CollectionsDir, err)
	}
	var ledgers []Ledger
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, err := Load(filepath.Join(root, CollectionsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		if l.Contract.Collection != e.Name() {
			return nil, fmt.Errorf("%s: contract.json names collection %q, not its directory's name", l.Dir, l.Contract.Collection)
		}
		ledgers = append(ledgers, l)
	}
	sort.Slice(ledgers, func(i, j int) bool { return ledgers[i].Contract.Collection < ledgers[j].Contract.Collection })
	return ledgers, nil
}

// Load reads one collection's ledger from dir. Both files are decoded
// strictly: an unknown key is an error, since a misspelled "excluded" would
// otherwise read as an unmapped operation and lower nothing anyone noticed.
func Load(dir string) (Ledger, error) {
	l := Ledger{Dir: dir}
	raw, err := os.ReadFile(filepath.Join(dir, "contract.json")) // #nosec G304 -- a path under the repository's own coverage directory
	if err != nil {
		return l, fmt.Errorf("%s: %w", dir, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l.Contract); err != nil {
		return l, fmt.Errorf("%s/contract.json: %w", dir, err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "map.yaml")) // #nosec G304 -- a path under the repository's own coverage directory
	if err != nil {
		return l, fmt.Errorf("%s: %w", dir, err)
	}
	ydec := yaml.NewDecoder(bytes.NewReader(raw))
	ydec.KnownFields(true)
	// An empty map.yaml (a collection with nothing mapped yet) decodes as
	// io.EOF, which is not an error here.
	if err := ydec.Decode(&l.Map); err != nil && !errors.Is(err, io.EOF) {
		return l, fmt.Errorf("%s/map.yaml: %w", dir, err)
	}
	return l, nil
}
