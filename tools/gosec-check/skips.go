// Package main: the files gosec could not type-check, which it scans
// nothing in.
//
// gosec reports a file it cannot type-check under "Golang errors" and goes
// on, so every finding it would have made there is simply missing. Read as
// "no findings", that passes a scan that never ran: on 2026-11-08 a gosec
// built by go1.26 could not decode go1.27's export data, skipped 1,345
// packages, and failed only because 15 waivers happened to sit in them. Had
// none, it would have printed "all individually waived" over a module it
// had not read. So a skip is never a pass: gosec's own warning (a
// toolchain it was not built with) fails, and so does every skipped file
// gosec-waivers.json does not name in its skips, each with a reason.
package main

import (
	"fmt"
	"path/filepath"
	"sort"
)

// gosecGlobalKey is the key gosec files a warning about the whole run
// under, rather than a file's error: the source-processing packages it was
// built with differ from the go list it runs.
const gosecGlobalKey = "-"

// gosecError is one entry gosec reports for a file it could not type-check.
type gosecError struct {
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Error  string `json:"error"`
}

// skipWaiver is one file accepted as never type-checking, with the reason
// (a deliberately unbuildable test fixture, for one).
type skipWaiver struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// skipVerdict is what checkSkips found.
type skipVerdict struct {
	// global is gosec's warning about the whole run, empty when there is
	// none.
	global string
	// unwaived are the skipped files no skip waiver names, sorted, each
	// with gosec's first error for it.
	unwaived []skippedFile
	// stale are the skip waivers naming a file gosec did not skip, sorted.
	stale []string
}

// skippedFile is one file gosec skipped and why.
type skippedFile struct {
	file, err string
}

// failed reports whether the verdict fails the check.
func (v skipVerdict) failed() bool {
	return v.global != "" || len(v.unwaived) > 0 || len(v.stale) > 0
}

// checkSkips holds gosec's "Golang errors" to skips: the global warning
// fails; a file, named relative to repoRoot, fails unless a skip waiver
// names it; and a skip waiver gosec did not need is stale.
func checkSkips(errs map[string][]gosecError, skips []skipWaiver, repoRoot string) skipVerdict {
	var v skipVerdict
	waived := make(map[string]bool, len(skips))
	for _, s := range skips {
		waived[s.File] = true
	}
	seen := map[string]bool{}
	for file, list := range errs {
		first := ""
		if len(list) > 0 {
			first = list[0].Error
		}
		if file == gosecGlobalKey {
			v.global = first
			continue
		}
		rel, err := filepath.Rel(repoRoot, file)
		if err != nil {
			rel = file
		}
		if waived[rel] {
			seen[rel] = true
			continue
		}
		v.unwaived = append(v.unwaived, skippedFile{file: rel, err: first})
	}
	for _, s := range skips {
		if !seen[s.File] {
			v.stale = append(v.stale, s.File)
		}
	}
	sort.Slice(v.unwaived, func(i, j int) bool { return v.unwaived[i].file < v.unwaived[j].file })
	sort.Strings(v.stale)
	return v
}

// report writes the verdict's failures for a person, naming at most limit
// skipped files (the 2026-11-08 run skipped thousands) and how many more.
func (v skipVerdict) report(limit int) string {
	out := ""
	if v.global != "" {
		out += fmt.Sprintf("gosec-check: gosec warns about the whole run: %s\n  Rebuild it with this toolchain (make tools): a gosec built by another Go release cannot read this one's export data, and skips what it cannot read.\n\n", v.global)
	}
	if len(v.unwaived) > 0 {
		out += fmt.Sprintf("gosec-check: gosec could not type-check %d file(s), so it scanned nothing in them; a skip is never a pass:\n\n", len(v.unwaived))
		for i, f := range v.unwaived {
			if i == limit {
				out += fmt.Sprintf("  ... and %d more\n", len(v.unwaived)-limit)
				break
			}
			out += fmt.Sprintf("  %s: %s\n", f.file, f.err)
		}
		out += "\nFix the build, rebuild gosec with this toolchain (make tools), or name a deliberately unbuildable file in gosec-waivers.json's skips with a written reason.\n\n"
	}
	if len(v.stale) > 0 {
		out += fmt.Sprintf("gosec-check: %d skip waiver(s) in gosec-waivers.json name a file gosec type-checked (remove them):\n\n", len(v.stale))
		for _, f := range v.stale {
			out += "  " + f + "\n"
		}
		out += "\n"
	}
	return out
}
