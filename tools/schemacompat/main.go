// Command schemacompat holds the generated schemas in docs/reference/schemas
// to the last release.
//
// It compares each file with the same file at the newest release tag at or
// below this tree's release line (buildinfo.CurrentRelease) and reports
// every breaking change: for an input schema (a runbook, an inventory file,
// an API request) a change that refuses a document the release accepted, for
// an output schema (the run report, an API response) a change that lets the
// platform write a document a reader of the release was never told about,
// and for the module catalog a method, parameter, return or fact gone or
// changed. On a later minor line each break must be named by a
// `changelog/*.breaking.md` fragment; on a patch line nothing accepts one.
// docs/13-releases-and-stability.md states the contracts this enforces.
//
// Usage: go run ./tools/schemacompat [-base TAG] [-line VERSION] [-json]
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
)

// schemaDir is where the generated schemas live, relative to the module root.
const schemaDir = "docs/reference/schemas"

// kind says how a schema file is compared.
type kind int

const (
	// inputSchema is a JSON Schema for a document a user writes.
	inputSchema kind = iota
	// outputSchema is a JSON Schema for a document the platform writes.
	outputSchema
	// openAPI is the Controller's API.
	openAPI
	// catalog is the module catalog.
	catalog
	// notAContract is a generated file nothing outside the repository reads.
	notAContract
)

// contracts says how each generated file is compared. A file the generator
// writes that is missing here fails the check, so a new file is classified
// on purpose rather than left unchecked.
var contracts = map[string]kind{
	"runbook.schema.json":      inputSchema,
	"inventory.schema.json":    inputSchema,
	"run-report.json":          outputSchema,
	"doc-filters.json":         outputSchema,
	"doc-conditions.json":      outputSchema,
	"migration-report.json":    outputSchema,
	"pr-check-report.json":     outputSchema,
	"openapi.json":             openAPI,
	"module-catalog.json":      catalog,
	"collection-coverage.json": notAContract,
}

// main runs the check and exits non-zero when the tree fails it.
func main() {
	base := flag.String("base", "", "release tag to compare against (default: the newest at or below -line)")
	line := flag.String("line", buildinfo.CurrentRelease, "release line this tree is on")
	asJSON := flag.Bool("json", false, "print the verdict as one JSON document")
	flag.Parse()
	v, err := run(gitShow{}, *base, *line, schemaDir, "changelog")
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemacompat:", err)
		os.Exit(2)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
	} else {
		printVerdict(os.Stdout, v)
	}
	if !v.Pass {
		os.Exit(1)
	}
}

// tree reads files at a release tag.
type tree interface {
	// tags lists the repository's tags.
	tags() ([]string, error)
	// list names the files in a directory at a tag.
	list(tag, dir string) ([]string, error)
	// read returns a file at a tag.
	read(tag, path string) ([]byte, error)
}

// run compares the working tree's schemas in dir with the base's.
func run(t tree, base, lineArg, dir, fragments string) (Verdict, error) {
	line, ok := parseVersion(lineArg)
	if !ok {
		return Verdict{}, fmt.Errorf("release line %q is not a release version", lineArg)
	}
	if base == "" {
		tags, err := t.tags()
		if err != nil {
			return Verdict{}, err
		}
		if base = baseTag(tags, line); base == "" {
			return Verdict{}, fmt.Errorf("no release tag at or below %s: fetch the tags (git fetch --tags) or name one with -base", line)
		}
	}
	baseV, ok := parseVersion(base)
	if !ok {
		return Verdict{}, fmt.Errorf("base %q is not a release tag", base)
	}
	v := Verdict{SchemaVersion: 1, Base: base, Line: line.String(), Patch: line.sameMinor(baseV)}
	var findings []Finding
	names, err := t.list(base, dir)
	if err != nil {
		return Verdict{}, err
	}
	current, err := os.ReadDir(dir)
	if err != nil {
		return Verdict{}, err
	}
	for _, e := range current {
		if _, known := contracts[e.Name()]; !known && strings.HasSuffix(e.Name(), ".json") {
			v.Problems = append(v.Problems, fmt.Sprintf("%s is not classified: add it to tools/schemacompat's contracts table", e.Name()))
		}
	}
	for _, name := range names {
		k, known := contracts[name]
		if known && k == notAContract {
			continue
		}
		old, err := t.read(base, dir+"/"+name)
		if err != nil {
			return Verdict{}, err
		}
		cur, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- a generated file of this repository
		if errors.Is(err, os.ErrNotExist) {
			findings = append(findings, Finding{Rule: "file-removed", Path: name + "#"})
			continue
		} else if err != nil {
			return Verdict{}, err
		}
		if !known {
			v.Problems = append(v.Problems, fmt.Sprintf("%s was in %s and is not classified: add it to the contracts table", name, base))
			continue
		}
		f, err := compareFile(name, k, old, cur)
		if err != nil {
			v.Problems = append(v.Problems, err.Error())
			continue
		}
		findings = append(findings, f...)
	}
	accepted, err := acceptedTokens(fragments)
	if err != nil {
		return Verdict{}, err
	}
	v.Findings, v.Stale, v.Pass = judge(findings, accepted, v.Patch)
	v.Pass = v.Pass && len(v.Problems) == 0
	return v, nil
}

// compareFile decodes one file at both versions and compares it by kind.
func compareFile(name string, k kind, oldB, newB []byte) ([]Finding, error) {
	var o, n any
	if err := json.Unmarshal(oldB, &o); err != nil {
		return nil, fmt.Errorf("%s at the base is not JSON: %w", name, err)
	}
	if err := json.Unmarshal(newB, &n); err != nil {
		return nil, fmt.Errorf("%s is not JSON: %w", name, err)
	}
	c := newSchemaComparer(name, o, n)
	om, oOK := o.(map[string]any)
	nm, nOK := n.(map[string]any)
	if !oOK || !nOK {
		return nil, fmt.Errorf("%s is not a JSON object", name)
	}
	switch k {
	case inputSchema:
		c.compare(om, nm, "", input)
	case outputSchema:
		c.compare(om, nm, "", output)
	case openAPI:
		c.compareOpenAPI(om, nm)
	case catalog:
		if c.compareCatalog(om, nm) == 0 && len(om) > 0 {
			return nil, fmt.Errorf("%s at the base has %d methods and none is implemented: the catalog's shape changed, so the check compared nothing", name, len(om))
		}
	}
	return c.findings, nil
}

// printVerdict writes the verdict for a person.
func printVerdict(w io.Writer, v Verdict) {
	fmt.Fprintf(w, "schemacompat: %s against %s (line %s", schemaDir, v.Base, v.Line)
	if v.Patch {
		fmt.Fprint(w, ", a patch line: no fragment accepts a break")
	}
	fmt.Fprintln(w, ")")
	for _, f := range v.Findings {
		if f.AcceptedBy != "" {
			fmt.Fprintf(w, "  accepted  %s  (%s)\n", f.Token(), f.AcceptedBy)
		} else {
			fmt.Fprintf(w, "  BREAKING  %s\n", f.Token())
		}
	}
	for _, s := range v.Stale {
		fmt.Fprintf(w, "  STALE     %s: a fragment names a break this tree does not make\n", s)
	}
	for _, p := range v.Problems {
		fmt.Fprintf(w, "  PROBLEM   %s\n", p)
	}
	if v.Pass {
		fmt.Fprintln(w, "schemacompat: no unaccepted breaking change")
		return
	}
	if v.Patch {
		fmt.Fprintln(w, "schemacompat: a point release never breaks a contract: undo each BREAKING change here, and make it on main for the next minor release")
		return
	}
	fmt.Fprintln(w, "schemacompat: name each BREAKING token, exactly as printed and in backticks, in a changelog/<slug>.breaking.md fragment saying what changed for a user")
}

// gitShow reads a tag through the git command.
type gitShow struct{}

// tags implements tree.
func (gitShow) tags() ([]string, error) {
	out, err := git("tag", "--list")
	return strings.Fields(out), err
}

// list implements tree.
func (gitShow) list(tag, dir string) ([]string, error) {
	out, err := git("ls-tree", "--name-only", tag+":"+dir)
	return strings.Fields(out), err
}

// read implements tree.
func (gitShow) read(tag, path string) ([]byte, error) {
	out, err := git("show", tag+":"+path)
	return []byte(out), err
}

// git runs one git command and returns its output.
func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...) // #nosec G204 -- fixed git subcommands; arguments are tags and paths
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
