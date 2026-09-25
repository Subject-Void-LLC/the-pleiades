// Command gendocs's Ansible module conversions page, generated from the
// converter's own tables and finding codes.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// generateAnsibleModules writes ansible-modules.md: what `pleiades forge
// migrate-playbook` does with each Ansible module it knows, read from the
// translator's own tables, and every finding code a migration report can
// carry, read from its closed set. A module the page does not list is
// blocked with module.unmapped.
func generateAnsibleModules(outDir string) error {
	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString(`# Ansible module conversions

What ` + "`pleiades forge migrate-playbook`" + ` does with each Ansible module it knows, generated from the
converter's own tables. A module not on this page is not converted: its task becomes an unrunnable
placeholder and the report raises ` + "`module.unmapped`" + `. See the
[migration guide](../03-migrating-from-ansible.md) for the playbook keywords, conditions, loops and
variables around a module, and the [report schema](schemas/migration-report.json) for the report itself.

Each native call has a **class**, its state semantics: *asserted* names a desired state the method
compares before it acts, *computed* is a desired state decided only at run time, *imperative* has no
desired state (running it is the only way to learn what it does), and *observe* only reads.

## Modules that convert

`)
	var manual []playbook.Entry
	for _, e := range playbook.Entries() {
		if e.Rating == playbook.RatingManual {
			manual = append(manual, e)
			continue
		}
		writeModule(&b, e)
	}
	b.WriteString("## Modules a person converts\n\n| Module | Why |\n|---|---|\n")
	for _, e := range manual {
		fmt.Fprintf(&b, "| %s | %s (`%s`) |\n", moduleNames(e), cell(e.Reason), e.Code)
	}
	b.WriteString("\n## Finding codes\n\nEvery finding in a migration report carries one of these codes. The set is closed: a code is added here before the converter can raise it.\n\n| Code | Outcome | Meaning | Instead |\n|---|---|---|---|\n")
	codes := playbook.Codes()
	for _, code := range slices.Sorted(func(yield func(playbook.Code) bool) {
		for c := range codes {
			if !yield(c) {
				return
			}
		}
	}) {
		doc := codes[code]
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", code, doc.Outcome, cell(doc.Summary), orDash(cell(doc.Native)))
	}
	return os.WriteFile(filepath.Join(outDir, "ansible-modules.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}

// writeModule renders one converting module: its calls, then its
// arguments.
func writeModule(b *strings.Builder, e playbook.Entry) {
	fmt.Fprintf(b, "### `%s`\n\n", e.Module)
	if len(e.Aliases) > 0 {
		fmt.Fprintf(b, "Also written as %s.\n\n", quoteList(e.Aliases))
	}
	b.WriteString("| When | Native call | Class |\n|---|---|---|\n")
	for _, s := range e.Selectors {
		for _, c := range s.Choices {
			when := fmt.Sprintf("`%s`: %s", s.Arg, quoteList(c.Values))
			if slices.Contains(c.Values, s.Absent) {
				when += " (the default)"
			}
			switch {
			case c.Call != nil:
				fmt.Fprintf(b, "| %s | %s | %s |\n", when, callText(c.Call), c.Call.Class)
			case c.Code != "":
				fmt.Fprintf(b, "| %s | blocked (`%s`): %s | |\n", when, c.Code, cell(c.Reason))
			default:
				fmt.Fprintf(b, "| %s | no call | |\n", when)
			}
			if len(c.Ignores) > 0 {
				fmt.Fprintf(b, "| | ignores %s, as Ansible does | |\n", quoteList(c.Ignores))
			}
		}
	}
	if e.Default != nil {
		fmt.Fprintf(b, "| otherwise | %s | %s |\n", callText(e.Default), e.Default.Class)
	}
	if len(e.Selectors) > 1 {
		b.WriteString("\nEach row that applies becomes its own native task, in the order above.\n")
	}
	b.WriteString("\n| Argument | Becomes |\n|---|---|\n")
	for _, a := range e.Args {
		fmt.Fprintf(b, "| %s | %s |\n", quoteList(append([]string{a.Name}, a.Aliases...)), argText(a))
	}
	if e.Adjust != nil {
		b.WriteString("\nThe converter also writes the parameters whose native default differs from Ansible's, so the task does what the playbook did.\n")
	}
	if len(e.Returns) > 0 {
		fmt.Fprintf(b, "\nA condition may read %s from its registered result.\n", quoteList(sortedKeys(e.Returns)))
	}
	b.WriteString("\n")
}

// callText renders a native call, its fixed parameters and its note.
func callText(c *playbook.Call) string {
	text := "`" + c.FQCN + "`"
	if len(c.Fixed) > 0 {
		var fixed []string
		for _, k := range sortedKeys(c.Fixed) {
			fixed = append(fixed, fmt.Sprintf("`%s: %v`", k, c.Fixed[k]))
		}
		text += " with " + strings.Join(fixed, ", ")
	}
	if c.Note != "" {
		text += " (review: " + cell(c.Note) + ")"
	}
	return text
}

// argText renders what happens to one argument.
func argText(a playbook.Arg) string {
	switch a.Handling {
	case playbook.ArgSelector:
		return "chooses the call"
	case playbook.ArgUnroll:
		return "`" + a.To + "`, one task per item of a list"
	case playbook.ArgDrop:
		return fmt.Sprintf("dropped (`%s`): %s", a.Code, cell(a.Reason))
	case playbook.ArgBlock:
		return fmt.Sprintf("blocked (`%s`): %s", a.Code, cell(a.Reason))
	}
	text := "`" + a.To + "`"
	if a.Note != "" {
		text += " (review: " + cell(a.Note) + ")"
	}
	return text
}

// moduleNames renders an entry's name and aliases in one cell.
func moduleNames(e playbook.Entry) string {
	return quoteList(append([]string{e.Module}, e.Aliases...))
}

// cell makes text safe inside a Markdown table cell.
func cell(text string) string {
	return strings.ReplaceAll(text, "|", `\|`)
}

// orDash renders an empty cell as a dash.
func orDash(text string) string {
	if text == "" {
		return "-"
	}
	return text
}
