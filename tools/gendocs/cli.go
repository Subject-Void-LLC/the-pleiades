package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
)

// generateCLI emits outDir/cli.md: every command in clispec.Root,
// recursively, with its positional argument, flags, and examples. Reads
// the exact tree cmd/pleiades's own --help text renders from
// (internal/clispec), so this page and a real terminal's --help output
// can never describe two different command sets.
func generateCLI(outDir string) error {
	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# CLI reference\n\n")
	b.WriteString("Every pleiades subcommand, generated from the same command tree `pleiades <command> " +
		"--help` renders from (`internal/clispec`). Flags are listed in the order each command's own " +
		"`flag.FlagSet` declares them; running `--help` against a real binary shows the identical set.\n\n")

	for _, cmd := range clispec.Root.Subcommands {
		writeCommandSection(&b, "pleiades", cmd, 2)
	}

	return os.WriteFile(filepath.Join(outDir, "cli.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}

// writeCommandSection renders one command and, recursively, its own
// Subcommands, each as its own heading rather than a nested list: a
// forge subcommand's flags need the same table treatment a top-level
// command's do, and a heading is where writeFlagsTable and
// writeExamplesList already know how to write one.
func writeCommandSection(b *strings.Builder, parentPath string, cmd clispec.Command, level int) {
	fullPath := parentPath + " " + cmd.Name
	invocation := fullPath
	if cmd.Positional != "" {
		invocation += " " + cmd.Positional
	}

	fmt.Fprintf(b, "%s %s\n\n", strings.Repeat("#", level), fullPath)
	if cmd.Synopsis != "" {
		fmt.Fprintf(b, "%s\n\n", cmd.Synopsis)
	}
	fmt.Fprintf(b, "%s\n\n", code(invocation+" [flags]"))

	writeFlagsTable(b, cmd.Flags)
	writeExamplesList(b, cmd.Examples)

	for _, sub := range cmd.Subcommands {
		writeCommandSection(b, fullPath, sub, level+1)
	}
}

func writeFlagsTable(b *strings.Builder, flags []clispec.Flag) {
	if len(flags) == 0 {
		return
	}
	rows := make([][]string, len(flags))
	for i, f := range flags {
		rows[i] = []string{"--" + f.Name, code(f.Type), code(f.Default), f.Doc}
	}
	b.WriteString(table([]string{"Flag", "Type", "Default", "Description"}, rows))
	b.WriteString("\n")
}

func writeExamplesList(b *strings.Builder, examples []string) {
	if len(examples) == 0 {
		return
	}
	for _, ex := range examples {
		fmt.Fprintf(b, "%s\n\n", code(ex))
	}
}
