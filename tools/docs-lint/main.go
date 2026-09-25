// Command docs-lint fails the build when a gitignored internal document is
// cited anywhere a real user could see it.
//
// .SPECIFICATION/, .AGENTS/, and .IGNORE/ are all matched by .gitignore's
// own `.[A-Z]*` pattern and never ship: a checkout a user actually receives
// does not contain PLAN.md, PATTERNS.md, IMPLEMENTATION.md,
// .SPECIFICATION/AWX_PARITY.md, or any other file under those three
// directories. A citation into one of them, anywhere a user can see it, is
// a promise the shipped binary cannot keep: it sends a real reader to a
// file that does not exist on their machine.
//
// This tool scans exactly the surfaces that are user-facing, not the whole
// repository: rewriting every internal engineering comment that cites the
// spec would destroy real rationale for no user benefit, since a Go source
// comment is read by a contributor, not shipped as documentation. Scanned:
//
//   - docs/: the one directory that ships as user documentation.
//   - The CLI files that build the --help and `pleiades doc` output a
//     user's terminal actually prints (cmd/pleiades/main.go, forge.go,
//     inventory.go, doc.go) and the command tree they render it from
//     (internal/clispec), which tools/gendocs also folds into
//     docs/reference/cli.md.
//   - internal/inventory/project.go: the scaffold templates `pleiades
//     init`/`add-host` write to a real user's inventory.yaml, runbook,
//     and README on disk, a persistent file a user keeps and reads, not
//     just terminal output.
//   - The five packages (pkg/collection, pkg/capability,
//     internal/api/router.go, internal/forge/collectionscaffold,
//     internal/apispec) whose exported doc comments, error strings, and
//     (for internal/apispec) Summary/Description fields the documentation
//     generation pipeline (Part XIV, Phase 68) folds directly into
//     generated user-facing reference pages, and whose non-test Go files
//     can therefore leak a citation straight into a page a user reads.
//   - The root-level Markdown files (README.md, CONTRIBUTING.md,
//     SECURITY.md, CODE_OF_CONDUCT.md, CHANGELOG.md), web/README.md, and
//     every changelog/*.md fragment: real files a contributor or user
//     reads directly, no generation step involved at all.
//
// Test files are excluded throughout: a _test.go file is read by a
// contributor running the suite, never rendered into a page a user reads,
// and this repository's own convention already uses internal citations in
// test names and comments as legitimate engineering context.
//
// Usage: go run ./tools/docs-lint
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// forbidden is compiled once: one pattern per gitignored token, each
// anchored so it does not fire on a substring of a real, shipped filename
// (PATTERNS.md must not match inside FAILURE_PATTERNS.md, which is
// tracked and ships).
var forbidden = []*regexp.Regexp{
	regexp.MustCompile(`\.SPECIFICATION`),
	regexp.MustCompile(`\.AGENTS`),
	regexp.MustCompile(`\.IGNORE`),
	regexp.MustCompile(`\bPLAN\.md`),
	regexp.MustCompile(`\bPATTERNS\.md`),
	regexp.MustCompile(`\bIMPLEMENTATION\.md`),
}

// finding is one forbidden citation, located precisely enough to act on
// without re-reading the file.
type finding struct {
	path string
	line int
	text string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "docs-lint:", err)
		os.Exit(1)
	}
}

func run() error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	targets, err := scanTargets(repoRoot)
	if err != nil {
		return err
	}

	var findings []finding
	for _, path := range targets {
		fs, err := scanFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		findings = append(findings, fs...)
	}
	literalFindings, goFiles, err := scanGoLiterals(repoRoot)
	if err != nil {
		return fmt.Errorf("scanning Go string literals: %w", err)
	}
	findings = append(findings, literalFindings...)

	if len(findings) == 0 {
		fmt.Printf("docs-lint: %d file(s) and the string literals of %d Go file(s) scanned, no citation of a gitignored document found\n", len(targets), goFiles)
		return nil
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].path != findings[j].path {
			return findings[i].path < findings[j].path
		}
		return findings[i].line < findings[j].line
	})

	fmt.Fprintf(os.Stderr, "docs-lint: %d citation(s) of a gitignored document in a user-facing surface:\n\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "  %s:%d: %s\n", f.path, f.line, strings.TrimSpace(f.text))
	}
	fmt.Fprintln(os.Stderr, "\nThese files never ship (.gitignore's own \".[A-Z]*\" pattern). Rewrite the")
	fmt.Fprintln(os.Stderr, "citation to name a file the repository actually ships, or a public URL.")
	return fmt.Errorf("docs-lint check failed")
}

// scanTargets returns every file this tool checks, relative to repoRoot.
func scanTargets(repoRoot string) ([]string, error) {
	var targets []string

	docsDir := filepath.Join(repoRoot, "docs")
	if err := filepath.Walk(docsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		targets = append(targets, path)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walking docs/: %w", err)
	}

	explicit := []string{
		filepath.Join(repoRoot, "cmd", "pleiades", "main.go"),
		filepath.Join(repoRoot, "cmd", "pleiades", "forge.go"),
		filepath.Join(repoRoot, "cmd", "pleiades", "inventory.go"),
		filepath.Join(repoRoot, "cmd", "pleiades", "doc.go"),
		filepath.Join(repoRoot, "internal", "clispec", "clispec.go"),
		filepath.Join(repoRoot, "internal", "inventory", "project.go"),
		// Root-level Markdown and the UI's own documentation page: real
		// files a contributor or user reads directly. Caught in practice
		// while writing these very files: CONTRIBUTING.md and
		// changelog/README.md both initially cited IMPLEMENTATION.md
		// before this check existed to catch it.
		//
		// web/README.md used to be on this list. It is gone with the rest
		// of the React SPA (Phase 19), and it is worth noting how it left:
		// every entry here is os.Stat-guarded, so a path that stops
		// existing is silently skipped rather than failing. That is
		// convenient and it is also how a target rots unnoticed, which is
		// why the replacement page is named explicitly below rather than
		// left to be discovered.
		filepath.Join(repoRoot, "README.md"),
		filepath.Join(repoRoot, "CONTRIBUTING.md"),
		filepath.Join(repoRoot, "SECURITY.md"),
		filepath.Join(repoRoot, "CODE_OF_CONDUCT.md"),
		filepath.Join(repoRoot, "CHANGELOG.md"),
		filepath.Join(repoRoot, "docs", "12-web-ui.md"),
	}
	for _, p := range explicit {
		if _, err := os.Stat(p); err == nil {
			targets = append(targets, p)
		}
	}

	changelogDir := filepath.Join(repoRoot, "changelog")
	if entries, err := os.ReadDir(changelogDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				targets = append(targets, filepath.Join(changelogDir, e.Name()))
			}
		}
	}

	genPackages := []string{
		filepath.Join(repoRoot, "pkg", "collection"),
		filepath.Join(repoRoot, "pkg", "capability"),
		filepath.Join(repoRoot, "internal", "forge", "collectionscaffold"),
		filepath.Join(repoRoot, "internal", "apispec"),
	}
	for _, dir := range genPackages {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			targets = append(targets, filepath.Join(dir, e.Name()))
		}
	}

	routerGo := filepath.Join(repoRoot, "internal", "api", "router.go")
	if _, err := os.Stat(routerGo); err == nil {
		targets = append(targets, routerGo)
	}

	return targets, nil
}

// scanFile reports every forbidden-token match in path, one finding per
// matching line.
func scanFile(path string) ([]finding, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed target list built by scanTargets, not user input
	if err != nil {
		return nil, err
	}

	var findings []finding
	for i, line := range strings.Split(string(data), "\n") {
		for _, re := range forbidden {
			if re.MatchString(line) {
				findings = append(findings, finding{path: path, line: i + 1, text: line})
				break
			}
		}
	}
	return findings, nil
}
