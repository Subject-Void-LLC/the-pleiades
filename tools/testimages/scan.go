// Finding image references in Go source.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// imageRef is the shape of a pullable reference: a name, then a tag, a
// digest or both. It refuses anything with spaces, a scheme or a URL
// path, so a variable that happens to end in "Image" but holds prose or a
// parameter name is not mistaken for one.
var imageRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9._-]+)?(@sha256:[a-f0-9]{64})?$`)

// scanDir returns the image references in every Go file in dir, tests
// included, since tests are what start containers.
func scanDir(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, path := range files {
		src, err := os.ReadFile(path) // #nosec G304 -- a file go list reported
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		refs = append(refs, scanFile(file)...)
	}
	return refs, nil
}

// scanFile returns the image references one file names: the value of a
// constant or variable whose name contains "Image" (NATSImage, and the
// deliberately old NATSImageBeforeLimitMarkerTTL), and the value of an
// Image field in a composite literal.
func scanFile(file *ast.File) []string {
	var refs []string
	keep := func(lit ast.Expr) {
		if ref, ok := pullable(lit); ok {
			refs = append(refs, ref)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if strings.Contains(name.Name, "Image") && i < len(node.Values) {
					keep(node.Values[i])
				}
			}
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok && key.Name == "Image" {
				keep(node.Value)
			}
		}
		return true
	})
	return refs
}

// pullable returns the reference a string literal holds when it is one a
// registry can serve: it has a tag or a digest, and it is not an image
// this repository builds itself.
func pullable(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	ref, err := strconv.Unquote(lit.Value)
	if err != nil || !imageRef.MatchString(ref) || !strings.ContainsAny(ref, ":@") {
		return "", false
	}
	if strings.HasPrefix(ref, "pleiades/") {
		return "", false
	}
	// A name with no letter is not an image, whatever its shape: "65532:65532"
	// is a uid:gid pair (tests/e2e's packagingImageUID), and CI's pull step
	// asked Docker Hub for a repository called 65532.
	if !strings.ContainsFunc(repository(ref), unicode.IsLetter) {
		return "", false
	}
	return ref, true
}

// repository is ref without its digest or tag. A tag follows the last
// colon after the last slash, so a registry's port is not taken for one.
func repository(ref string) string {
	name, _, _ := strings.Cut(ref, "@")
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	return name
}

// sortedKeys returns set's keys in order.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
