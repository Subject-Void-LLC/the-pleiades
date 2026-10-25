// Reading the packages to test from a file, for a caller whose list is
// longer than a command line holds.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// importPath is what a listed package must be: an import path, or a
// pattern of one, and never an option or anything a shell would act on.
var importPath = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// readPackagesFile returns the packages path lists, separated by spaces or
// lines, refusing a word that is not an import path, so the file cannot
// smuggle an option to go test.
func readPackagesFile(path string) ([]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the caller names its own list
	if err != nil {
		return nil, fmt.Errorf("reading the packages file: %w", err)
	}
	words := strings.Fields(string(data))
	for _, w := range words {
		if strings.HasPrefix(w, "-") || !importPath.MatchString(w) {
			return nil, fmt.Errorf("the packages file %s lists %q, which is not an import path", path, w)
		}
	}
	return words, nil
}
