// This file guards .dockerignore, which is a security control rather than
// housekeeping: without it the image build context carries .SPECIFICATION/
// and .claude/, two trees that are gitignored precisely because they never
// ship, plus roughly 400 MB of history and stale binaries.
//
// It lives in internal/testsupport rather than beside the Dockerfiles
// because that is where this repository keeps the assertions that tie a
// deployment descriptor to something a test can check. The file it reads
// is plain text with no Go on either side, so nothing else would ever run
// it.
//
// Four claims are tested, and they only mean anything together:
//
//   - the file denies by default, so a new directory is out until someone
//     decides otherwise;
//   - nothing allows a forbidden path back in, including through a glob;
//   - everything the Dockerfiles actually copy is still allowed in, so
//     "deny more" cannot silently break a build;
//   - every allowance is one some Dockerfile needs, so the deny-by-default
//     doctrine applies to the allow list too.
package testsupport_test

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// dockerignoreFile is the build-context filter this test guards, relative
// to this package's own directory.
const dockerignoreFile = "../../.dockerignore"

// dockerfiles are every Dockerfile in this repository that builds from the
// repository root, and therefore every Dockerfile the file above filters.
//
// The third one is easy to forget and was: Dockerfile.legacy-ansible-runner
// is a Python image that copies exactly one path out of this repository,
// internal/redact/rules.json. It compiles no Go, so every Go-shaped
// intuition about what a build needs is wrong about it, and a narrowing of
// `!internal/` would break it with both Go builds still green.
var dockerfiles = []string{
	"../../Dockerfile.controller",
	"../../Dockerfile.runner",
	"../../Dockerfile.legacy-ansible-runner",
}

// mustNotEnterBuildContext are the paths whose presence in a build context
// is the defect this file exists to prevent. Each is listed with the
// reason it matters, because "excluded for tidiness" and "excluded because
// it must never ship" get maintained very differently by whoever reads
// this next.
var mustNotEnterBuildContext = map[string]string{
	".SPECIFICATION": "gitignored specification documents that never ship; tools/docs-lint enforces the same rule at every surface a user can see, and the image build was the one surface nothing guarded",
	".AGENTS":        "gitignored project rules that never ship, same rule as .SPECIFICATION",
	".claude":        "agent working directory, which has held a complete second checkout of this repository under .claude/worktrees/",
	".git":           "the whole version history, which is roughly 60 MB and is never a build input",
	"controller":     "a stale compiled binary left in the repository root; copying one into a builder that exists to compile a fresh one is pure waste and risks shipping the stale one",
	"runner":         "a stale compiled binary left in the repository root, same reasoning as controller",
	"pleiades":       "a stale compiled binary left in the repository root, same reasoning as controller",
}

// alwaysRequired are build inputs that cannot be read off a COPY line.
//
// Both Go Dockerfiles copy the whole context with `COPY . .` and then run
// `go build ./cmd/<binary>`, so the directories that build reaches are a
// requirement no parser can see. They are named here instead.
var alwaysRequired = []string{
	"cmd",      // the composition roots the two `go build` invocations name
	"internal", // everything those roots import, which is most of this repository
	"pkg",      // the public packages internal/ imports in turn
}

// TestDockerignoreExistsAndDeniesByDefault asserts the shape of the file
// rather than only its contents.
//
// A .dockerignore that lists exclusions fails open: the next directory
// added to the repository root joins the build context and nothing says
// so. A file that denies everything with a bare `*` and then allows
// specific paths back fails closed, which is the direction the error
// should point for a control whose whole job is keeping things out. This
// test would pass against a long exclusion list too, so it checks for the
// bare `*` explicitly.
func TestDockerignoreExistsAndDeniesByDefault(t *testing.T) {
	lines := readDockerignore(t)

	for _, line := range lines {
		if line == "*" {
			return
		}
	}

	t.Errorf(".dockerignore does not deny by default.\n"+
		"Expected a bare `*` line followed by `!`-prefixed allowances.\n"+
		"An exclusion list fails open: whatever is added to the repository root next joins the build context silently.\n"+
		"Read: %s", dockerignoreFile)
}

// TestDockerignoreExcludesNonBuildInputs asserts that each path in
// mustNotEnterBuildContext is genuinely excluded.
//
// Because the file denies by default, a path is excluded unless something
// allows it back, so this test looks for an allowance that would re-admit
// one of them. That is the realistic regression: nobody deletes the `*`,
// but somebody adds `!.claude/settings.json` for a plausible-sounding
// reason and re-admits the whole tree above it.
//
// It compares patterns, not prefixes, and that is the fix rather than a
// refinement. The earlier version asked only whether an allowance was
// equal to a forbidden path or started with it, so it caught
// `!.SPECIFICATION` and missed `!.S*`, which re-admits the same tree and
// is closer to what somebody would actually write. Every one of these
// tests passed against a file containing that line.
func TestDockerignoreExcludesNonBuildInputs(t *testing.T) {
	lines := readDockerignore(t)

	for forbidden, why := range mustNotEnterBuildContext {
		for _, line := range lines {
			if !strings.HasPrefix(line, "!") {
				continue
			}

			allowed := splitPattern(strings.TrimPrefix(line, "!"))
			if !readmits(t, allowed, forbidden) {
				continue
			}
			t.Errorf(".dockerignore re-admits %q into the build context, via the line %q.\n"+
				"Why that path must stay out: %s\n"+
				"If this allowance is genuinely needed, narrow it and record why here and in %s.",
				forbidden, line, why, dockerignoreFile)
		}
	}
}

// TestDockerignoreAllowsEveryBuildInput is the negative control for the
// two tests above, and it exists because they are both satisfied by an
// empty allow list.
//
// Denying by default is only correct if the build still has what it
// needs. A .dockerignore consisting of one `*` passes every other
// assertion in this file and produces an image that cannot be built at
// all, so the useful assertion is in both directions.
//
// Most of the required set is read straight out of the Dockerfiles' own
// COPY lines rather than typed here, because a hand-kept list is exactly
// what went stale: internal/redact/rules.json was a real build input of a
// real Dockerfile and appeared in no list, so narrowing `!internal/` would
// have broken that image with every test in this file green.
func TestDockerignoreAllowsEveryBuildInput(t *testing.T) {
	required := append(append([]string{}, alwaysRequired...), dockerfileCopySources(t)...)

	allowances := dockerignoreAllowances(t)

	for _, want := range required {
		if coveredBy(t, allowances, want) {
			continue
		}
		t.Errorf(".dockerignore does not allow %q back into the build context, so a Dockerfile that copies it cannot build.\n"+
			"Add an allowance covering it to %s.", want, dockerignoreFile)
	}
}

// TestDockerignoreHasNoUnnecessaryAllowances applies the file's own
// doctrine to its allow list.
//
// The argument .dockerignore makes about itself is that unnecessary
// entries are how this kind of control fails, and until this test existed
// that argument was not applied to the allowances. `!tools/` sat in the
// file justified as "the build stage may run repo-local tooling" when no
// stage ran any and no Dockerfile copied from it. Nothing was wrong with
// the image it produced; what was wrong is that a reviewer had no way to
// tell a needed allowance from an unneeded one, which is the state in
// which "just allow one more thing" wins every argument.
//
// An allowance counts as justified when it covers a required path, or when
// a required path covers it (a deliberately narrower allowance inside a
// required directory). Anything else has to be added to alwaysRequired
// with a written reason, which is the point: the reason becomes reviewable.
func TestDockerignoreHasNoUnnecessaryAllowances(t *testing.T) {
	required := append(append([]string{}, alwaysRequired...), dockerfileCopySources(t)...)

	for _, allowed := range dockerignoreAllowances(t) {
		justified := false
		for _, want := range required {
			// Either the allowance admits a required path in full, or it
			// is a narrower allowance sitting inside one, which is a
			// legitimate way to tighten this file rather than loosen it.
			if covers(t, allowed, want) || covers(t, splitPattern(want), strings.Join(allowed, "/")) {
				justified = true
				break
			}
		}
		if !justified {
			t.Errorf(".dockerignore allows %q back into the build context, but nothing needs it.\n"+
				"No Dockerfile copies it by name, and it is not inside a directory the builds reach. Check the module graph with:\n"+
				"    go list -deps ./cmd/controller ./cmd/runner\n"+
				"Remove the line, or add the path to alwaysRequired in this file with the reason a build needs it.",
				strings.Join(allowed, "/"))
		}
	}
}

// dockerignoreAllowances returns every `!`-prefixed line in the file, each
// normalized and split into path segments ready for matching.
func dockerignoreAllowances(tb testing.TB) [][]string {
	tb.Helper()

	var out [][]string
	for _, line := range readDockerignore(tb) {
		if !strings.HasPrefix(line, "!") {
			continue
		}
		out = append(out, splitPattern(strings.TrimPrefix(line, "!")))
	}
	return out
}

// dockerfileCopySources returns every repository path the Dockerfiles copy
// out of the build context by name.
//
// It deliberately reads the real files rather than trusting a list. Lines
// copying from a previous build stage are skipped, because those sources
// are paths inside an image and not in the context at all. `.` is skipped
// because "copy everything" is not a requirement any single path can
// express; alwaysRequired covers what those builds reach. A source
// containing a wildcard is skipped too, since expanding it would mean
// resolving it against the working tree, and this guard is about the
// filter rather than about what happens to be checked in today.
func dockerfileCopySources(tb testing.TB) []string {
	tb.Helper()

	var out []string
	for _, file := range dockerfiles {
		raw, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			// Loudly, not skipped. A Dockerfile that moved without this
			// list moving with it means the guard quietly stopped
			// covering an image, which is the failure this whole file is
			// about.
			tb.Fatalf("reading %s: %v\n"+
				"If a Dockerfile was renamed or removed, update `dockerfiles` in this test to match.", file, err)
		}

		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(strings.ToUpper(line), "COPY ") {
				continue
			}

			fields := strings.Fields(line)[1:] // drop the COPY verb itself
			fromAnotherStage := false
			var operands []string
			for _, f := range fields {
				if strings.HasPrefix(f, "--") {
					if strings.HasPrefix(f, "--from=") {
						fromAnotherStage = true
					}
					continue
				}
				operands = append(operands, f)
			}
			// The last operand is the destination inside the image, and
			// there must be at least one source before it.
			if fromAnotherStage || len(operands) < 2 {
				continue
			}

			for _, src := range operands[:len(operands)-1] {
				if src == "." || strings.ContainsAny(src, "*?[") {
					continue
				}
				out = append(out, normalizeSlashes(src))
			}
		}
	}

	if len(out) == 0 {
		tb.Fatalf("found no named COPY sources across %v.\n"+
			"Every one of those files copies something by name, so an empty result means this parser stopped working and the check below is asserting nothing.", dockerfiles)
	}
	return out
}

// readmits reports whether an allowance pattern pulls a forbidden path, or
// anything inside it, back into the build context.
//
// It compares only as many segments as the shorter of the two has, which
// covers both shapes of the mistake. An allowance deeper than the
// forbidden path (`!.SPECIFICATION/notes.md`) lifts a file out of a
// directory that must stay out. An allowance shallower than it
// (`!.claude` against a forbidden `.claude/worktrees`) re-admits the
// parent and everything under it.
func readmits(tb testing.TB, allowed []string, forbidden string) bool {
	tb.Helper()

	want := splitPattern(forbidden)
	n := len(allowed)
	if len(want) < n {
		n = len(want)
	}
	return segmentsMatch(tb, allowed[:n], want[:n])
}

// covers reports whether an allowance pattern admits a required path in
// full: it has to match every one of its own segments against the leading
// segments of the required path, and it cannot be deeper than that path.
//
// The depth rule is what separates this from readmits above. `!internal/redact`
// re-admits something inside `internal`, which readmits treats as a hit,
// but it does NOT put `internal` in the context, so it cannot satisfy a
// requirement for `internal`.
func covers(tb testing.TB, allowed []string, required string) bool {
	tb.Helper()

	want := splitPattern(required)
	if len(allowed) > len(want) {
		return false
	}
	return segmentsMatch(tb, allowed, want[:len(allowed)])
}

// coveredBy reports whether any of the allowances covers the required path.
func coveredBy(tb testing.TB, allowances [][]string, required string) bool {
	tb.Helper()

	for _, allowed := range allowances {
		if covers(tb, allowed, required) {
			return true
		}
	}
	return false
}

// segmentsMatch matches two equal-length segment lists, treating the first
// as glob patterns.
//
// path.Match rather than filepath.Match, because .dockerignore patterns are
// always slash separated no matter which operating system reads them, and
// filepath.Match would interpret a backslash as a separator on Windows.
// Docker's own matcher does not let `*` cross a separator either, which is
// why this matches one segment at a time instead of matching whole paths.
func segmentsMatch(tb testing.TB, patterns, segments []string) bool {
	tb.Helper()

	for i := range patterns {
		ok, err := path.Match(patterns[i], segments[i])
		if err != nil {
			// A malformed pattern is a defect in .dockerignore worth
			// reporting, not a quiet false.
			tb.Errorf("%s contains the malformed pattern %q: %v", dockerignoreFile, patterns[i], err)
			return false
		}
		if !ok {
			return false
		}
	}
	return true
}

// splitPattern normalizes one .dockerignore path or pattern and splits it
// into segments. A leading `./` or `/` and a trailing `/` are all forms
// Docker accepts for the same path, so they are removed before matching
// rather than being allowed to make two spellings look different.
func splitPattern(p string) []string {
	return strings.Split(normalizePattern(p), "/")
}

// normalizePattern strips the decorations Docker ignores.
func normalizePattern(p string) string {
	p = normalizeSlashes(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	return p
}

// normalizeSlashes collapses the Windows separator so one spelling of a
// path cannot hide behind another.
func normalizeSlashes(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// readDockerignore returns the file's meaningful lines: comments and blank
// lines removed, surrounding whitespace trimmed. It fails the test rather
// than returning an error, because every caller here would do the same.
func readDockerignore(tb testing.TB) []string {
	tb.Helper()

	f, err := os.Open(filepath.Clean(dockerignoreFile))
	if err != nil {
		tb.Fatalf("opening %s: %v\n"+
			"This file is a security control, not housekeeping: without it the build context carries .SPECIFICATION/ and .claude/, which are gitignored precisely because they never ship.", dockerignoreFile, err)
	}
	defer func() { _ = f.Close() }()

	var lines []string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		tb.Fatalf("reading %s: %v", dockerignoreFile, err)
	}

	return lines
}
