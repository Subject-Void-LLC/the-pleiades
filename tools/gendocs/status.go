package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/catalogdata"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

// generateStatus emits outDir/implementation-status.md: the full
// declared-versus-implemented matrix across every registered Collection
// method, cross-linked from docs/01-start-here.md's own hand-written
// overview. This page answers "which one, specifically" for a claim that
// page only states in aggregate.
func generateStatus(outDir string) error {
	var implemented, declared []string
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			continue
		}
		if desc.Manifest.Status == collection.StatusImplemented {
			implemented = append(implemented, cfg.Name)
		} else {
			declared = append(declared, cfg.Name)
		}
	}

	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# Implementation status\n\n")
	b.WriteString("The full declared-versus-implemented matrix for every registered Collection method. " +
		"See [Start here](../01-start-here.md#implementation-status) for what this means for the product " +
		"as a whole.\n\n")

	fmt.Fprintf(&b, "**%d of %d methods are implemented.**\n\n", len(implemented), len(implemented)+len(declared))

	b.WriteString("## Implemented\n\n")
	if len(implemented) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, fqcn := range implemented {
			link := strings.ReplaceAll(fqcn, ".", "/") + ".md"
			fmt.Fprintf(&b, "- [%s](modules/%s)\n", fqcn, link)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Declared, not yet implemented\n\n")
	b.WriteString("Registered, capability-checked, reachable through the real dispatcher. Calling one " +
		"refuses with an explicit \"declared but not implemented\" error rather than running.\n\n")
	for _, fqcn := range declared {
		fmt.Fprintf(&b, "- `%s`\n", fqcn)
	}
	b.WriteString("\n")

	return os.WriteFile(filepath.Join(outDir, "implementation-status.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}
