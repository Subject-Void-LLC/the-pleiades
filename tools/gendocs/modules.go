package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
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

		if err := writeModulePage(modulesDir, cfg.Name, desc.Manifest, desc.CheckCall != nil); err != nil {
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
//
// someCalls is true for a method whose check covers only some calls
// (Descriptor.CheckCall), which the Attributes table says rather than a
// flat "Supported".
func writeModulePage(modulesDir, fqcn string, m collection.Manifest, someCalls bool) error {
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
		{"Runs", runsWhere(m.ExecutionContext)},
		{"Check mode", checkModeSupport(m.SupportsCheck, someCalls, m.NoCheckReason)},
		{"Engine version", code(m.EngineVersion)},
	}))
	b.WriteString("\n")

	if m.Status == collection.StatusImplemented {
		writeParameters(&b, m.Doc)
		writeReturns(&b, m.Doc)
		writeReversibility(&b, m.Reversibility)
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

// writeParameters resolves doc.Fragments against fragment.Builtin and
// renders every fragment's params ahead of the method's own, so a shared
// parameter (a credential shape, a pagination knob) always appears first
// and identically worded everywhere it is used.
func writeParameters(b *strings.Builder, doc collection.Doc) {
	var params []collection.Param
	for _, name := range doc.Fragments {
		if frag, ok := fragment.Builtin[name]; ok {
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

// writeReversibility renders whether this method can be undone, which is
// what a reader planning a rollback needs and what nothing else on the
// page says.
//
// It renders for every implemented method including the read-only ones,
// because "nothing to undo" is a real and useful answer and its absence
// would read as an omission rather than as a fact.
//
// What it deliberately does NOT render is the inverse itself. The
// manifest no longer holds one, and an earlier version of this function
// printed a static inverse FQCN taken from it, which was documentation of
// something that could not be right: the real inverse depends on what a
// run found rather than on what the method is. A method emits its own
// concrete inverse at run time instead, so the honest thing a generated
// page can say is whether one is ever produced and what it will not
// cover.
func writeReversibility(b *strings.Builder, r collection.Reversibility) {
	b.WriteString("## Undoing this\n\n")

	if r.Reversible {
		b.WriteString("**Can be undone.** A run that changes something records the instruction that " +
			"reverses it, as an `inverse` stat holding the method to call and the parameters to call it " +
			"with, resolved from the state this run actually found. A run that changed nothing records " +
			"no instruction, which is how it says that undoing it means doing nothing.\n\n")
	} else {
		b.WriteString("**Cannot be undone.** This method never records a reversing instruction, so a " +
			"rollback reaching a task that used it stops rather than guessing.\n\n")
	}

	if r.Notes != "" {
		fmt.Fprintf(b, "%s\n\n", r.Notes)
	}

	b.WriteString("Note that no rollback engine reads this yet. What exists today is the recording, " +
		"which has to happen during the forward run because the values an undo needs are gone once the " +
		"change is applied.\n\n")
}

// checkModeSupport renders a method's answer to `pleiades run --mode
// check`. It says what happens either way rather than a bare yes or no,
// because the "no" case has a consequence a reader planning a dry run
// needs to know: the task is named as unchecked and the check ends
// non-zero, rather than being skipped quietly. A method that can check
// only some calls says so, since its description is where the reader
// learns which.
//
// noCheckReason, a method's own account of why it cannot be checked, is
// appended to the "no" answer when the method gives one.
func checkModeSupport(supported, someCalls bool, noCheckReason string) string {
	switch {
	case supported && someCalls:
		return "Supported for some calls, named in the description: those report what they would change " +
			"and change nothing, a check run names any other as unchecked, and validation refuses " +
			"check_mode on one"
	case supported:
		return "Supported: reports what it would change and changes nothing"
	}
	if noCheckReason != "" {
		return "Not supported: a check run names this task as unchecked, since " + noCheckReason
	}
	return "Not supported: a check run names this task as unchecked"
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

// runsWhere says, for a reference page, where a method's code runs and
// whether it acts on a device (PLAN.md Section 14's execution context). An
// unstated field reads as target-side with a device required, as the engine
// reads it.
func runsWhere(ec collection.ExecutionContext) string {
	site := "on or against the target device"
	switch ec.Site {
	case collection.SiteController:
		site = "in the host process (the CLI or a Runner)"
	case collection.SiteHybrid:
		site = "partly in the host process and partly on the target device"
	}
	switch ec.Device {
	case collection.DeviceOptional:
		return site + "; acts on a device only for some calls, and a call that needs none skips the runbook's hosts: and runs once"
	case collection.DeviceNone:
		return site + "; acts on no device, so a task skips the runbook's hosts: and runs once"
	default:
		return site + "; acts on its target device"
	}
}
