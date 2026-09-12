// Package main: the rules about code this repository generates.
//
// Each one catches a change that compiles, passes every unit test, and
// still does nothing. CLAUDE.md calls an ent schema edit without
// regenerated code "the worst failure shape available" for exactly that
// reason, and a missing dialect migration is worse still: enttest runs
// Schema.Create, so the tables exist in every test and exist nowhere in
// a real deployment.
package main

import (
	"fmt"
	"strings"
)

// pathPrefixes for the generated trees these rules reason about.
const (
	entSchemaDir        = "internal/ent/schema/"
	entGeneratedDir     = "internal/ent/"
	entMigrationsDir    = "internal/ent/migrate/migrations/"
	catalogDataDir      = "internal/forge/catalogdata/"
	catalogGeneratedDir = "internal/catalog/"
	specDir             = "internal/apispec/"
	cliSpecDir          = "internal/clispec/"
	referenceDir        = "docs/reference/"
	wellknownDir        = "internal/api/wellknown/"
)

// dialects are the two backends every migration must exist for. The
// names are the directory names under entMigrationsDir and the argument
// internal/ent/migrate/gen/main.go takes.
var dialects = []string{"sqlite", "postgres"}

// checkGenerated reports every generated tree this commit leaves behind
// its own source.
func checkGenerated(entries []stagedEntry) []Finding {
	var findings []Finding
	findings = append(findings, checkEntRegenerated(entries)...)
	findings = append(findings, checkEntMigrations(entries)...)
	findings = append(findings, checkCatalogRegenerated(entries)...)
	findings = append(findings, checkReferenceRegenerated(entries)...)
	return findings
}

// staged reports whether any staged entry satisfies match.
func staged(entries []stagedEntry, match func(stagedEntry) bool) bool {
	for _, e := range entries {
		if match(e) {
			return true
		}
	}
	return false
}

// isEntSchema reports whether a path is a hand-written ent schema file.
func isEntSchema(path string) bool {
	return strings.HasPrefix(path, entSchemaDir) && strings.HasSuffix(path, ".go")
}

// isEntGenerated reports whether an entry is ent's generated output.
//
// Both halves matter. The path narrows the question to internal/ent, and
// the content marker answers it, because internal/ent holds four
// hand-written files (generate.go, migrate/apply.go, migrate/parity_test.go
// and migrate/gen/main.go) that no path prefix separates from the
// generated ones.
func isEntGenerated(e stagedEntry) bool {
	return strings.HasPrefix(e.Path, entGeneratedDir) && isGeneratedSource(e.Content)
}

// checkEntRegenerated refuses a schema edit with no regenerated client
// beside it.
func checkEntRegenerated(entries []stagedEntry) []Finding {
	if !staged(entries, func(e stagedEntry) bool { return isEntSchema(e.Path) }) {
		return nil
	}
	if staged(entries, isEntGenerated) {
		return nil
	}
	return []Finding{{
		Severity: SeverityFail,
		Rule:     "an ent schema edit ships with its regenerated code",
		Detail: "this commit changes internal/ent/schema but stages no regenerated file under internal/ent\n" +
			"  run `go generate ./internal/ent` and stage the result\n" +
			"  a schema edit with no regenerated code compiles fine and silently does nothing",
	}}
}

// checkEntMigrations refuses a NEW ent entity that is missing a
// migration for either dialect.
//
// It fires only on an added schema file. A field added to an existing
// entity also needs a migration, but this check cannot tell such an edit
// from a comment fix, and a rule that cried wolf on every schema comment
// would be turned off. internal/ent/migrate/parity_test.go is what
// catches a dialect left behind in every other case.
func checkEntMigrations(entries []stagedEntry) []Finding {
	var added []string
	for _, e := range entries {
		if e.Kind == changeAdded && isEntSchema(e.Path) {
			added = append(added, e.Path)
		}
	}
	if len(added) == 0 {
		return nil
	}

	var missing []string
	for _, dialect := range dialects {
		dir := entMigrationsDir + dialect + "/"
		if !staged(entries, func(e stagedEntry) bool {
			return e.Kind == changeAdded && strings.HasPrefix(e.Path, dir)
		}) {
			missing = append(missing, dialect)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "this commit adds %s but no new migration for: %s\n",
		strings.Join(added, ", "), strings.Join(missing, ", "))
	for _, dialect := range missing {
		fmt.Fprintf(&b, "  run `go run internal/ent/migrate/gen/main.go %s <name>`\n", dialect)
	}
	b.WriteString("  the runtime applies versioned migration files, not Schema.Create, so an entity with no\n")
	b.WriteString("  migration has tables in every enttest unit test and no tables in any real deployment")
	return []Finding{{
		Severity: SeverityFail,
		Rule:     "a new ent entity ships with a migration for both dialects",
		Detail:   b.String(),
	}}
}

// checkCatalogRegenerated warns when the catalog's source data changes
// with no regenerated entry beside it.
//
// A warning rather than a failure because `pleiades forge` is invoked
// with --skip-existing and is genuinely idempotent: editing an entry
// that is already on disk legitimately produces no new file. What it
// cannot leave alone is a Doc change, which internal/archtest's
// TestCatalogDataDocsMatchTheRegistry does check.
func checkCatalogRegenerated(entries []stagedEntry) []Finding {
	if !staged(entries, func(e stagedEntry) bool { return strings.HasPrefix(e.Path, catalogDataDir) }) {
		return nil
	}
	if staged(entries, func(e stagedEntry) bool { return strings.HasPrefix(e.Path, catalogGeneratedDir) }) {
		return nil
	}
	return []Finding{{
		Severity: SeverityWarn,
		Rule:     "a catalog data edit usually ships with its regenerated entry",
		Detail: "this commit changes internal/forge/catalogdata but stages nothing under internal/catalog\n" +
			"  if the edit changed a method's Doc, run `go generate ./internal/forge/catalogdata`\n" +
			"  the command is idempotent and skips entries already on disk, so this can legitimately be nothing",
	}}
}

// checkReferenceRegenerated warns when a spec changes with no
// regenerated reference page beside it.
func checkReferenceRegenerated(entries []stagedEntry) []Finding {
	if !staged(entries, func(e stagedEntry) bool {
		return strings.HasPrefix(e.Path, specDir) || strings.HasPrefix(e.Path, cliSpecDir)
	}) {
		return nil
	}
	if staged(entries, func(e stagedEntry) bool {
		return strings.HasPrefix(e.Path, referenceDir) || strings.HasPrefix(e.Path, wellknownDir)
	}) {
		return nil
	}
	return []Finding{{
		Severity: SeverityWarn,
		Rule:     "a spec edit usually ships with its regenerated reference",
		Detail: "this commit changes internal/apispec or internal/clispec but stages nothing generated from it\n" +
			"  run `make docs-gen-check` to see whether docs/reference or internal/api/wellknown moved",
	}}
}
