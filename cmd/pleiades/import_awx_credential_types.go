package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// pleiades import awx-credential-types: read an AWX credential type export
// and report what this platform would do with each one.
//
// # Why this reports rather than writes
//
// The Walk tier has no controller, no database and no broker, and it does
// not dial one: that is the tier's defining property rather than a missing
// feature. So this command cannot create credential types, and pretending
// to would mean either building a controller client into the offline CLI
// or writing rows nothing reads.
//
// What it does instead is the question somebody actually has before a
// migration: will my credential types survive it, and which ones will not.
// Every type in the export is decoded through the same structs the
// Controller stores and validated through the same render engine the
// Controller validates with, so a type this command accepts is a type the
// Controller accepts. That is the whole value: it is a real answer,
// obtained offline, before anybody commits to a migration window.
//
// With --out it writes each importable type as a JSON document ready to
// POST to /credential-types, so the answer is also the input to the next
// step.

// importVerdict is what this platform would do with one exported type.
type importVerdict string

const (
	// verdictImportable is a custom type that decodes and validates.
	verdictImportable importVerdict = "importable"

	// verdictShipped is a namespace this platform already ships as a
	// managed type. AWX's own rule is that a managed type cannot be
	// edited, so an import reuses it rather than recreating it as a
	// custom type, and recreating it is the mistake this verdict exists
	// to prevent.
	verdictShipped importVerdict = "already shipped"

	// verdictNotImplemented is a namespace AWX manages and this platform
	// declares and has not built.
	verdictNotImplemented importVerdict = "not implemented"

	// verdictInvalid is a type that decodes and does not validate. It is
	// the one verdict that names a problem in the export rather than a
	// gap here.
	verdictInvalid importVerdict = "refused"
)

// importedType is one export entry and what would happen to it.
type importedType struct {
	Namespace string
	Name      string
	Verdict   importVerdict
	Reason    string
	Type      credtype.CredentialType
}

// awxExport is the two shapes an AWX response arrives in.
//
// A list endpoint returns {"count": N, "results": [...]}; a detail
// endpoint returns the bare object. Both are accepted for the reason
// tests/parity/testdata/README.md gives about the corpus loader: a
// response somebody had to reshape before it could be read is a response
// that can be reshaped wrongly.
type awxExport struct {
	Results []json.RawMessage `json:"results"`
}

func runImportAWXCredentialTypes(args []string) error {
	fs := flag.NewFlagSet("import awx-credential-types", flag.ContinueOnError)
	out := fs.String("out", "", "directory to write each importable type into as JSON, ready to POST to /credential-types")
	quiet := fs.Bool("quiet", false, "report only the types this platform would not import")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: pleiades import awx-credential-types [--out <dir>] [--quiet] <export.json>")
	}

	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("reading the export: %w", err)
	}

	entries, err := decodeExport(raw)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("%s holds no credential types", fs.Arg(0))
	}

	results := classify(entries)
	report(os.Stdout, results, *quiet)

	if *out != "" {
		if err := writeImportable(*out, results); err != nil {
			return err
		}
	}

	// A non-zero exit when something will not import, so this is usable in
	// a migration script as a gate rather than only by eye. "Already
	// shipped" is not a failure: reusing a managed type is the correct
	// outcome, not a degraded one.
	for _, r := range results {
		if r.Verdict == verdictInvalid || r.Verdict == verdictNotImplemented {
			return fmt.Errorf("%d of %d credential types would not import", countProblems(results), len(results))
		}
	}
	return nil
}

// decodeExport reads either AWX response shape into a list of raw types.
func decodeExport(raw []byte) ([]json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "["):
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decoding the export as a list: %w", err)
		}
		return list, nil
	case strings.HasPrefix(trimmed, "{"):
		var export awxExport
		if err := json.Unmarshal(raw, &export); err != nil {
			return nil, fmt.Errorf("decoding the export: %w", err)
		}
		if len(export.Results) > 0 {
			return export.Results, nil
		}
		// A bare object from a detail endpoint.
		return []json.RawMessage{json.RawMessage(raw)}, nil
	default:
		return nil, errors.New("the export is neither a JSON object nor a JSON array")
	}
}

// classify decides what would happen to each exported type.
//
// The order of the checks is the point. A namespace this platform ships is
// reported as already shipped even if the exported document would also
// validate, because reusing the managed type is what AWX's own rule
// requires and recreating it as a custom type is the migration mistake
// that is hardest to notice afterwards: everything works until an upgrade
// changes the managed one and the custom copy silently does not move.
func classify(entries []json.RawMessage) []importedType {
	eng := render.New()
	out := make([]importedType, 0, len(entries))

	for _, entry := range entries {
		var ct credtype.CredentialType
		if err := json.Unmarshal(entry, &ct); err != nil {
			out = append(out, importedType{
				Namespace: "?", Name: "?",
				Verdict: verdictInvalid,
				Reason:  "this entry is not a credential type: " + err.Error(),
			})
			continue
		}

		result := importedType{Namespace: ct.Namespace, Name: ct.Name, Type: ct}

		switch {
		case managed.Has(ct.Namespace):
			result.Verdict = verdictShipped
			result.Reason = "this platform ships it, so the import reuses it rather than creating a copy"
		case managed.CheckNamespace(ct.Namespace) != nil:
			result.Verdict = verdictNotImplemented
			result.Reason = strings.TrimPrefix(
				managed.CheckNamespace(ct.Namespace).Error(),
				managed.ErrNotImplemented.Error()+": ")
		default:
			if err := ct.Validate(eng); err != nil {
				result.Verdict = verdictInvalid
				result.Reason = err.Error()
				break
			}
			result.Verdict = verdictImportable
			result.Reason = describeImport(ct)
		}
		out = append(out, result)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Namespace < out[j].Namespace })
	return out
}

// describeImport says what an importable type would bring with it, so a
// reader can see the consequential part without opening the document.
func describeImport(ct credtype.CredentialType) string {
	var parts []string
	if secrets := ct.Inputs.SecretFields(); len(secrets) > 0 {
		parts = append(parts, fmt.Sprintf("%d secret input(s)", len(secrets)))
	}
	if len(ct.Injectors.Env) > 0 {
		parts = append(parts, fmt.Sprintf("%d environment variable(s)", len(ct.Injectors.Env)))
	}
	if len(ct.Injectors.ExtraVars) > 0 {
		parts = append(parts, fmt.Sprintf("%d extra variable(s)", len(ct.Injectors.ExtraVars)))
	}
	if labels := ct.Injectors.FileLabels(); len(labels) > 0 {
		parts = append(parts, fmt.Sprintf("%d generated file(s)", len(labels)))
	}
	if prompts := ct.Inputs.AskAtRuntimeFields(); len(prompts) > 0 {
		// Worth calling out separately: a template binding one of these
		// cannot be relaunched, because the answer was never stored.
		parts = append(parts, fmt.Sprintf("%d input(s) prompted at launch, so a job using it cannot be relaunched", len(prompts)))
	}
	if len(parts) == 0 {
		return "no injectors: this type reaches a run directly rather than through injection"
	}
	return strings.Join(parts, ", ")
}

// report writes the verdicts as a readable table.
func report(w *os.File, results []importedType, quiet bool) {
	counts := map[importVerdict]int{}
	for _, r := range results {
		counts[r.Verdict]++
	}

	for _, r := range results {
		if quiet && (r.Verdict == verdictImportable || r.Verdict == verdictShipped) {
			continue
		}
		fmt.Fprintf(w, "%-14s %-24s %s\n", r.Verdict, r.Namespace, r.Name)
		if r.Reason != "" {
			fmt.Fprintf(w, "%-14s %s\n", "", r.Reason)
		}
	}

	fmt.Fprintf(w, "\n%d credential type(s): %d importable, %d already shipped, %d not implemented, %d refused\n",
		len(results),
		counts[verdictImportable], counts[verdictShipped],
		counts[verdictNotImplemented], counts[verdictInvalid])
}

// writeImportable writes one JSON document per importable type.
//
// The document is re-encoded from the decoded struct rather than copied
// from the export, which is deliberate: what lands on disk is exactly what
// this platform understood, so a field the export carried and this
// platform ignores is visibly absent rather than silently carried along
// and dropped later.
func writeImportable(dir string, results []importedType) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating the output directory: %w", err)
	}

	for _, r := range results {
		if r.Verdict != verdictImportable {
			continue
		}
		body, err := json.MarshalIndent(r.Type, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding %s: %w", r.Namespace, err)
		}
		path := filepath.Join(dir, r.Namespace+".json")
		if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Fprintf(os.Stdout, "wrote %s\n", path)
	}
	return nil
}

// countProblems counts the verdicts that make this command exit non-zero.
func countProblems(results []importedType) int {
	n := 0
	for _, r := range results {
		if r.Verdict == verdictInvalid || r.Verdict == verdictNotImplemented {
			n++
		}
	}
	return n
}
