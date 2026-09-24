// Go string literal pass for docs-lint.
//
// The file list in scanTargets covers documentation and the few Go files
// whose whole text a user reads (the CLI's help, the generated reference
// sources). A citation can also reach a user from any other Go file,
// inside an error message or a flag's help string, and three did: the
// runbook engine's refusal of an Ansible playbook, its refusal of type:
// ansible, and `pleiades forge new-filter --help` each told the reader to
// consult PLAN.md, which never ships. Comments in those same files cite
// internal documents all the time and legitimately, since only a
// contributor reads them, so this pass parses each Go file and checks
// its string literals alone, never its comments.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// literalRoots are the directories whose Go string literals can reach a
// user: the binaries (cmd), everything they are built from (internal),
// and the public packages a third party imports (pkg). tools/ is left
// out, since its programs run only inside this repository, and this very
// file's patterns would otherwise match themselves.
var literalRoots = []string{"cmd", "internal", "pkg"}

// scanGoLiterals reports every string literal in a non-test Go file under
// literalRoots that cites a gitignored document.
func scanGoLiterals(repoRoot string) ([]finding, int, error) {
	var findings []finding
	files := 0
	fset := token.NewFileSet()
	for _, root := range literalRoots {
		dir := filepath.Join(repoRoot, root)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			files++
			found, err := scanGoFile(fset, path)
			findings = append(findings, found...)
			return err
		})
		if err != nil {
			return nil, files, err
		}
	}
	return findings, files, nil
}

// scanGoFile parses path without its comments and reports each string
// literal that matches a forbidden pattern.
func scanGoFile(fset *token.FileSet, path string) ([]finding, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var findings []finding
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			text = lit.Value
		}
		for _, re := range forbidden {
			if re.MatchString(text) {
				findings = append(findings, finding{path: path, line: fset.Position(lit.Pos()).Line, text: text})
				break
			}
		}
		return true
	})
	return findings, nil
}
