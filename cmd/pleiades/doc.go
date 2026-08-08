// Command pleiades's `doc` subcommand lives here: an offline reference
// reader over the same pkg/collection registry catalog_builtins.go's
// blank import already populates in this binary, and the same registry
// tools/gendocs reads to emit docs/reference/modules/*.md. A generated
// page and this command can never disagree about a method's contract,
// because both read collection.Lookup, never a second copy of the data.
//
// For a single static binary this is strictly better than `ansible-doc`:
// no virtualenv, no collection install, no execution environment, and it
// works on an air-gapped jump host, the normal environment for this
// product's own target user rather than an edge case.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/catalogdata"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

// catalogEntry pairs an FQCN with its live Manifest, in catalogdata's own
// registration order.
type catalogEntry struct {
	FQCN     string
	Manifest collection.Manifest
}

// catalogEntries walks catalogdata.Collections and resolves each FQCN's
// live Manifest via collection.Lookup, the same pairing
// tools/gendocs/schemas.go's moduleCatalogEntries performs for the
// generated module-catalog.json: catalogdata supplies the ordered list of
// names, pkg/collection supplies what each one actually is right now.
func catalogEntries() ([]catalogEntry, error) {
	entries := make([]catalogEntry, 0, len(catalogdata.Collections))
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			return nil, fmt.Errorf("pleiades doc: %q is in the catalog but never registered into pkg/collection", cfg.Name)
		}
		entries = append(entries, catalogEntry{FQCN: cfg.Name, Manifest: desc.Manifest})
	}
	return entries, nil
}

const docUsage = "usage: pleiades doc [--list [namespace]] [--snippet <fqcn>] [--json] [fqcn]"

// runDoc dispatches the doc subcommand's four modes: --list, --snippet,
// --json, and a bare fqcn lookup. They are mutually exclusive except that
// --json accepts an optional fqcn narrowing it to one Manifest instead of
// the whole catalog, checked in the order listed so a combination like
// `--list --json` (not a supported shape) fails on --list first rather
// than silently picking one.
func runDoc(args []string) error {
	fs := flag.NewFlagSet("doc", flag.ContinueOnError)
	listFlag := fs.Bool("list", false, "list every registered FQCN, or those in one namespace if fqcn is given as a prefix")
	snippetFlag := fs.Bool("snippet", false, "print a paste-ready runbook task stanza for fqcn")
	jsonFlag := fs.Bool("json", false, "print the full catalog, or one fqcn's Manifest, as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("%s", docUsage)
	}
	var fqcn string
	if fs.NArg() == 1 {
		fqcn = fs.Arg(0)
	}

	entries, err := catalogEntries()
	if err != nil {
		return err
	}

	switch {
	case *listFlag:
		return printDocList(entries, fqcn)
	case *snippetFlag:
		if fqcn == "" {
			return fmt.Errorf("--snippet requires an fqcn: pleiades doc --snippet <fqcn>")
		}
		return printDocSnippet(entries, fqcn)
	case *jsonFlag:
		return printDocJSON(entries, fqcn)
	case fqcn != "":
		return printDocEntry(entries, fqcn)
	default:
		return fmt.Errorf("%s", docUsage)
	}
}

// lookupEntry finds fqcn in entries, or returns a "not found" error
// naming --list as how to discover the real set of names, the same
// recovery path every mode below needs.
func lookupEntry(entries []catalogEntry, fqcn string) (catalogEntry, error) {
	for _, e := range entries {
		if e.FQCN == fqcn {
			return e, nil
		}
	}
	return catalogEntry{}, fmt.Errorf("pleiades doc: unknown fqcn %q (see 'pleiades doc --list')", fqcn)
}

// printDocList prints one line per FQCN matching prefix: every FQCN if
// prefix is empty, otherwise those equal to prefix or nested under it
// (prefix followed by "."), so `pleiades doc --list net.catalyst` and
// `pleiades doc --list net` both work as a reader expects a namespace
// filter to.
func printDocList(entries []catalogEntry, prefix string) error {
	matched := make([]catalogEntry, 0, len(entries))
	for _, e := range entries {
		if prefix == "" || e.FQCN == prefix || strings.HasPrefix(e.FQCN, prefix+".") {
			matched = append(matched, e)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].FQCN < matched[j].FQCN })

	if len(matched) == 0 {
		if prefix == "" {
			return fmt.Errorf("pleiades doc: no FQCNs are registered (internal/catalog blank import missing?)")
		}
		return fmt.Errorf("pleiades doc: no FQCN matches namespace %q", prefix)
	}

	width := 0
	for _, e := range matched {
		if len(e.FQCN) > width {
			width = len(e.FQCN)
		}
	}
	for _, e := range matched {
		fmt.Printf("%-*s  %-11s  %s\n", width, e.FQCN, e.Manifest.Status, e.Manifest.Doc.Summary)
	}
	return nil
}

// printDocEntry renders one FQCN's full reference as plain text: the same
// fields tools/gendocs/modules.go's writeModulePage renders as a
// Markdown page, in the same fixed order (Synopsis, Attributes,
// Parameters, Returns, See also, Examples), so a terminal reader and a
// browsed reference page describe a method identically. A declared
// method stops after Attributes, for the same reason writeModulePage
// does: there is no real behavior yet for Parameters, Returns, or
// Examples to describe.
func printDocEntry(entries []catalogEntry, fqcn string) error {
	e, err := lookupEntry(entries, fqcn)
	if err != nil {
		return err
	}
	m := e.Manifest

	fmt.Println(e.FQCN)
	fmt.Println(strings.Repeat("=", len(e.FQCN)))
	fmt.Println()

	summary := m.Doc.Summary
	if summary == "" {
		summary = "(no summary yet)"
	}
	fmt.Println(summary)
	if m.Doc.Description != "" && m.Doc.Description != summary {
		fmt.Println()
		fmt.Println(m.Doc.Description)
	}
	fmt.Println()

	if m.Status != collection.StatusImplemented {
		fmt.Println("status: declared, not implemented. Calling it refuses with an explicit \"not implemented\" error.")
		fmt.Println()
	}

	capNames := make([]string, len(m.RequiredCapabilities))
	for i, c := range m.RequiredCapabilities {
		capNames[i] = string(c)
	}
	fmt.Println("attributes:")
	fmt.Printf("  capabilities        %s\n", joinOrDash(capNames))
	fmt.Printf("  transports          %s\n", joinOrDash(m.SupportedTransports))
	fmt.Printf("  requires elevation  %t\n", m.ExecutionContext.RequiresElevation)
	if m.EngineVersion != "" {
		fmt.Printf("  engine version      %s\n", m.EngineVersion)
	}

	if m.Status == collection.StatusImplemented {
		printParams(m.Doc)
		printReturns(m.Doc)
	}

	if len(m.Doc.SeeAlso) > 0 {
		fmt.Println("\nsee also:")
		for _, s := range m.Doc.SeeAlso {
			fmt.Printf("  %s\n", s)
		}
	}

	if m.Status == collection.StatusImplemented && len(m.Doc.Examples) > 0 {
		fmt.Println("\nexamples:")
		for _, ex := range m.Doc.Examples {
			fmt.Printf("\n  %s:\n\n%s\n", ex.Name, indent(ex.RunbookYAML, "  "))
		}
	}
	return nil
}

func printParams(doc collection.Doc) {
	var params []collection.Param
	for _, name := range doc.Fragments {
		if frag, ok := catalogdata.Fragments[name]; ok {
			params = append(params, frag.Params...)
		}
	}
	params = append(params, doc.Params...)
	if len(params) == 0 {
		return
	}
	fmt.Println("\nparameters:")
	for _, p := range params {
		req := ""
		if p.Required {
			req = ", required"
		}
		fmt.Printf("  %-20s %s%s\n    %s\n", p.Name, p.Type, req, p.Description)
	}
}

func printReturns(doc collection.Doc) {
	if len(doc.Returns) == 0 {
		return
	}
	fmt.Println("\nreturns:")
	for _, r := range doc.Returns {
		fmt.Printf("  %-20s %s\n    %s\n", r.Name, r.Type, r.Description)
	}
}

// printDocSnippet prints a paste-ready runbook task stanza for fqcn: its
// first documented Example if it has one, or a bare skeleton naming every
// parameter this method documents (declared methods document none, so a
// declared method's snippet is deliberately just fqcn and an empty
// params map, never a fabricated call).
func printDocSnippet(entries []catalogEntry, fqcn string) error {
	e, err := lookupEntry(entries, fqcn)
	if err != nil {
		return err
	}
	if len(e.Manifest.Doc.Examples) > 0 {
		fmt.Print(e.Manifest.Doc.Examples[0].RunbookYAML)
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "- name: TODO\n  fqcn: %s\n", e.FQCN)
	if len(e.Manifest.Doc.Params) == 0 {
		b.WriteString("  params: {}\n")
	} else {
		b.WriteString("  params:\n")
		for _, p := range e.Manifest.Doc.Params {
			fmt.Fprintf(&b, "    %s: # %s\n", p.Name, p.Type)
		}
	}
	fmt.Print(b.String())
	return nil
}

// printDocJSON prints entries as JSON: the full FQCN-to-Manifest map when
// fqcn is empty (byte-for-byte the same shape
// docs/reference/schemas/module-catalog.json carries, since both marshal
// collection.Manifest with its own JSON tags from the same registry), or
// one Manifest alone when fqcn narrows it.
func printDocJSON(entries []catalogEntry, fqcn string) error {
	var v any
	if fqcn == "" {
		m := make(map[string]collection.Manifest, len(entries))
		for _, e := range entries {
			m[e.FQCN] = e.Manifest
		}
		v = m
	} else {
		e, err := lookupEntry(entries, fqcn)
		if err != nil {
			return err
		}
		v = e.Manifest
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // a version constraint like ">=1.0.0" should read as itself, not >=1.0.0
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("pleiades doc: marshaling JSON: %w", err)
	}
	fmt.Print(buf.String())
	return nil
}

func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}

// indent prefixes every non-empty line of s with prefix, so a multi-line
// Example's RunbookYAML nests visually under its own heading instead of
// starting flush against the terminal's left edge.
func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
