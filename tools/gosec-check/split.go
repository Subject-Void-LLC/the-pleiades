// This file finds the packages gosec would load in pieces from this checkout.
//
// gosec lists each package directory's files with go/build's ImportDir and
// loads them through go/packages as absolute file paths (gosec v2.30.0,
// analyzer.go, Analyzer.load). go/packages splits a list of patterns longer
// than 16,383 characters into separate `go list` calls (golang.org/x/tools
// v0.52.0, packages.go, defaultDriver and splitIntoChunks), and each part is
// type-checked as a package of its own, so a file in one part cannot see a
// type declared in another. gosec then reports "undefined: Template" under
// "# command-line-arguments" for a package that builds. The length of the list
// grows with the length of the checkout's path times the number of files:
// internal/ent's 180 files came to 14,445 characters from a worktree at
// /home/noot/auto-roboto/.claude/worktrees/open-b (2026-11-21), so a checkout
// whose path is longer than 57 characters fails, and each new file in
// internal/ent lowers that.
//
// gosec offers no way to pass shorter paths: it joins every file name to the
// absolute path of its directory. So the packages over the limit are found
// here, before gosec runs, and scanned again from a shorter path to the same
// checkout (shortpath.go). gosec's own fix, loading a module's package by its
// directory (securego/gosec #1803, 2026-10-26), is in no release as of
// v2.30.0; once the pin moves past it, TestScan_RescuesAPackageSplitByALongPath
// fails on its first check, and this rescue can go.
package main

import (
	"fmt"
	"go/build"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// goPackagesArgMax is the longest pattern list go/packages hands to one
// `go list` call: Windows' command line limit, 32,767, less the 16,384 it
// keeps for the environment (golang.org/x/tools v0.52.0, packages.go,
// safeArgMax).
const goPackagesArgMax = 32767 - 16384

// listLength is the length go/packages counts for patterns: each one, and one
// separator after it (splitIntoChunks).
func listLength(patterns []string) int {
	total := 0
	for _, p := range patterns {
		total += len(p) + 1
	}
	return total
}

// overLimit reports whether go/packages would split patterns into more than
// one `go list` call: splitIntoChunks starts a new chunk once the running
// length passes goPackagesArgMax.
func overLimit(patterns []string) bool {
	return listLength(patterns) > goPackagesArgMax
}

// gosecFiles returns the files gosec loads for the package in dir, a path
// relative to root, as gosec makes them: go/build's ImportDir with no build
// tags, its GoFiles then its CgoFiles, each joined to the directory's
// absolute path under root. gosec-check passes gosec neither -tags nor
// -tests, so this is the whole list. A directory ImportDir refuses yields no
// files, as gosec loads none from it.
func gosecFiles(root, dir string) []string {
	abs := filepath.Join(root, dir)
	pkg, err := build.Default.ImportDir(abs, build.ImportComment)
	if err != nil {
		return nil
	}
	files := make([]string, 0, len(pkg.GoFiles)+len(pkg.CgoFiles))
	for _, name := range pkg.GoFiles {
		files = append(files, filepath.Join(abs, name))
	}
	for _, name := range pkg.CgoFiles {
		files = append(files, filepath.Join(abs, name))
	}
	return files
}

// gosecDirRegexp compiles an -exclude-dir value as gosec v2.30.0's
// ExcludedDirsRegExp does: forward slashes, each one escaped, and an optional
// separator on each side.
func gosecDirRegexp(value string) (*regexp.Regexp, error) {
	return regexp.Compile(fmt.Sprintf(`([\\/])?%s([\\/])?`, strings.ReplaceAll(filepath.ToSlash(value), "/", `\/`)))
}

// packageDirs returns the package directories gosec's "./..." finds from the
// working directory root, as relative paths, in walk order: each directory
// holding a path that ends in .go, unless one of excludes, -exclude-dir
// values, matches it (gosec v2.30.0, helpers.go, PackagePaths). Like gosec it
// walks every directory, excluded ones too, and follows no symlink.
func packageDirs(root string, excludes []string) ([]string, error) {
	var res []*regexp.Regexp
	for _, value := range excludes {
		re, err := gosecDirRegexp(value)
		if err != nil {
			return nil, fmt.Errorf("compiling -exclude-dir %q: %w", value, err)
		}
		res = append(res, re)
	}
	seen := map[string]bool{}
	var dirs []string
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, _ error) error {
		// gosec's walk function ignores the error it is handed, so a
		// directory it cannot read is passed over rather than failing the
		// scan, and so is it here.
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		for _, re := range res {
			if re.MatchString(filepath.ToSlash(rel)) {
				return nil
			}
		}
		dirs = append(dirs, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	return dirs, nil
}

// longPackage is one package gosec would load in pieces from root.
type longPackage struct {
	// dir is the package's directory relative to root.
	dir string
	// length is its file list's length from root, as listLength counts it.
	length int
	// files is the number of files in that list.
	files int
}

// gosecDefaultExcludes are the -exclude-dir values gosec v2.30.0 sets itself
// before reading its command line (cmd/gosec/main.go).
var gosecDefaultExcludes = []string{"vendor", `\.git/`}

// longPackages returns the packages gosec's "./..." scans from root, with
// excludes and gosec's own, whose file list from root is over
// goPackagesArgMax.
func longPackages(root string, excludes []string) ([]longPackage, error) {
	dirs, err := packageDirs(root, append(append([]string(nil), gosecDefaultExcludes...), excludes...))
	if err != nil {
		return nil, err
	}
	var long []longPackage
	for _, dir := range dirs {
		files := gosecFiles(root, dir)
		if overLimit(files) {
			long = append(long, longPackage{dir: dir, length: listLength(files), files: len(files)})
		}
	}
	return long, nil
}

// exactDirPattern returns an -exclude-dir value that matches dir, a relative
// path, and nothing else: anchored at both ends, with each dot in a character
// class rather than escaped by a backslash, since gosec turns every backslash
// into a slash on Windows (ToSlash) before compiling. A directory name holding
// any other regular expression character is refused rather than matched
// loosely.
func exactDirPattern(dir string) (string, error) {
	slashed := filepath.ToSlash(dir)
	var b strings.Builder
	b.WriteString("^")
	for _, r := range slashed {
		switch {
		case r == '.':
			b.WriteString("[.]")
		case strings.ContainsRune(`\+*?()|[]{}^$`, r):
			return "", fmt.Errorf("package directory %q holds %q, which gosec-check cannot match exactly in an -exclude-dir pattern", dir, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("$")
	return b.String(), nil
}
