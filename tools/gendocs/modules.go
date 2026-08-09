package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// generateModules emits one reference page per FQCN registered in
// catalogdata, plus a namespace index, into outDir/modules. It reads each
// FQCN's Manifest from the live pkg/collection registry via
// collection.Lookup, never from catalogdata directly: the registry is
// what pleiades doc and the real dispatcher both read, so a generated
// page and a live "pleiades doc <fqcn>" call can never disagree about
// what a method's contract is. catalogdata supplies only the ordered
// list of FQCNs to walk and the namespace grouping.
func generateModules(outDir string) error {
	modulesDir := filepath.Join(outDir, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil { // #nosec G301 -- generated docs, not secret material
		return fmt.Errorf("creating %s: %w", modulesDir, err)
	}

	byNamespace := map[string][]string{}
	var missing []string

	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			// A catalogdata entry that never registered means
			// internal/catalog/builtins.go's blank-import list is
			// missing it, or the generated package failed to build.
			// Either way this is real drift worth failing loudly on,
			// not silently skipping.
			missing = append(missing, cfg.Name)
			continue
		}

		ns := strings.SplitN(cfg.Name, ".", 2)[0]
		byNamespace[ns] = append(byNamespace[ns], cfg.Name)

		if err := writeModulePage(modulesDir, cfg.Name, desc.Manifest); err != nil {
			return err
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("gendocs: %d catalogdata entr(ies) never registered into pkg/collection (internal/catalog/builtins.go drift?): %s",
			len(missing), strings.Join(missing, ", "))
	}

	return writeModulesIndex(modulesDir, byNamespace)
}

// writeModulePage renders one FQCN's reference page in the fixed section
// order this project borrows from Ansible's own module pages: Synopsis,
// Attributes, Parameters, Returns, See also, Examples. A declared method
// (Status != StatusImplemented) renders Attributes and nothing past it:
// there is no real behavior yet for Parameters, Returns, or Examples to
// describe, and fabricating any of them would be exactly the kind of
// aspirational documentation this whole generator exists to replace.
func writeModulePage(modulesDir, fqcn string, m collection.Manifest) error {
	segments := strings.Split(fqcn, ".")
	dir := filepath.Join(append([]string{modulesDir}, segments[:len(segments)-1]...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- generated docs, not secret material
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	status := "declared"
	if m.Status == collection.StatusImplemented {
		status = "beta"
	}

	var b strings.Builder
	b.WriteString(frontMatter(status))
	fmt.Fprintf(&b, "# %s\n\n", fqcn)

	summary := m.Doc.Summary
	if summary == "" {
		summary = "*No summary yet.*"
	}
	b.WriteString(summary + "\n\n")

	if m.Doc.Description != "" && m.Doc.Description != summary {
		b.WriteString(m.Doc.Description + "\n\n")
	}

	if m.Status != collection.StatusImplemented {
		b.WriteString("**Status: declared, not implemented.** Registered with the manifest below, so " +
			"`pleiades validate` and editor tooling already know about it, but calling it refuses with an " +
			"explicit \"not implemented\" error rather than running.\n\n")
	}

	capNames := make([]string, len(m.RequiredCapabilities))
	for i, c := range m.RequiredCapabilities {
		capNames[i] = string(c)
	}

	b.WriteString("## Attributes\n\n")
	b.WriteString(table([]string{"", ""}, [][]string{
		{"Capabilities", quoteList(capNames)},
		{"Transports", quoteList(m.SupportedTransports)},
		{"Requires elevation", yesNo(m.ExecutionContext.RequiresElevation)},
		{"Engine version", code(m.EngineVersion)},
	}))
	b.WriteString("\n")

	if m.Status == collection.StatusImplemented {
		writeParameters(&b, m.Doc)
		writeReturns(&b, m.Doc)
	}

	if len(m.Doc.SeeAlso) > 0 {
		b.WriteString("## See also\n\n")
		for _, s := range m.Doc.SeeAlso {
			fmt.Fprintf(&b, "- %s\n", code(s))
		}
		b.WriteString("\n")
	}

	if m.Status == collection.StatusImplemented {
		writeExamples(&b, m.Doc)
	}

	path := filepath.Join(modulesDir, filepath.Join(segments...)+".md")
	return os.WriteFile(path, []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}

// writeParameters resolves doc.Fragments against catalogdata.Fragments and
// renders every fragment's params ahead of the method's own, so a shared
// parameter (a credential shape, a pagination knob) always appears first
// and identically worded everywhere it is used.
func writeParameters(b *strings.Builder, doc collection.Doc) {
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

	rows := make([][]string, len(params))
	for i, p := range params {
		rows[i] = []string{code(p.Name), code(p.Type), yesNo(p.Required), code(p.Default), p.Description}
	}
	b.WriteString("## Parameters\n\n")
	b.WriteString(table([]string{"Name", "Type", "Required", "Default", "Description"}, rows))
	b.WriteString("\n")
}

func writeReturns(b *strings.Builder, doc collection.Doc) {
	if len(doc.Returns) == 0 {
		return
	}
	rows := make([][]string, len(doc.Returns))
	for i, r := range doc.Returns {
		rows[i] = []string{code(r.Name), code(r.Type), r.Returned, r.Description}
	}
	b.WriteString("## Returns\n\n")
	b.WriteString(table([]string{"Name", "Type", "Returned", "Description"}, rows))
	b.WriteString("\n")
}

func writeExamples(b *strings.Builder, doc collection.Doc) {
	if len(doc.Examples) == 0 {
		return
	}
	b.WriteString("## Examples\n\n")
	for _, ex := range doc.Examples {
		fmt.Fprintf(b, "%s:\n\n```yaml\n%s```\n\n", ex.Name, ex.RunbookYAML)
	}
}

// writeModulesIndex renders docs/reference/modules/index.md: one row per
// namespace, with an implemented/declared count so "is there a module for
// X" and "does it actually work yet" are both answerable from one page.
func writeModulesIndex(modulesDir string, byNamespace map[string][]string) error {
	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# Module catalog\n\n")
	b.WriteString("Every registered Collection method, grouped by namespace. A `declared` method is " +
		"registered but not yet implemented; see each namespace's own page for which methods those are.\n\n")

	rows := make([][]string, 0, len(byNamespace))
	totalImplemented, total := 0, 0
	for _, ns := range sortedKeys(byNamespace) {
		names := byNamespace[ns]
		implemented := 0
		for _, fqcn := range names {
			desc, _ := collection.Lookup(fqcn)
			if desc.Manifest.Status == collection.StatusImplemented {
				implemented++
			}
		}
		totalImplemented += implemented
		total += len(names)
		rows = append(rows, []string{code(ns), itoa(len(names)), itoa(implemented)})
	}
	rows = append(rows, []string{"**total**", "**" + itoa(total) + "**", "**" + itoa(totalImplemented) + "**"})

	b.WriteString(table([]string{"Namespace", "Methods", "Implemented"}, rows))
	b.WriteString("\n## All methods\n\n")

	allRows := make([][]string, 0, total)
	for _, ns := range sortedKeys(byNamespace) {
		names := byNamespace[ns]
		sortedNames := append([]string(nil), names...)
		sort.Strings(sortedNames)
		for _, fqcn := range sortedNames {
			desc, _ := collection.Lookup(fqcn)
			status := "declared"
			if desc.Manifest.Status == collection.StatusImplemented {
				status = "implemented"
			}
			link := strings.ReplaceAll(fqcn, ".", "/") + ".md"
			allRows = append(allRows, []string{
				fmt.Sprintf("[%s](%s)", fqcn, link),
				status,
				desc.Manifest.Doc.Summary,
			})
		}
	}
	b.WriteString(table([]string{"FQCN", "Status", "Summary"}, allRows))

	return os.WriteFile(filepath.Join(modulesDir, "index.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}
