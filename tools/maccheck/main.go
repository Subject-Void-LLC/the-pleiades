// Command maccheck runs a branch's container-free packages on a real Mac
// over SSH, before a push, for what a Linux machine cannot show: a GNU-only
// tool or flag that macOS's BSD userland lacks. CI's fast-macos job finds
// the same, but only after the push (FAILURE_PATTERNS 420, 476, 494).
//
// The Mac is PLEIADES_MAC, from the environment or from tests/mac/.env
// (gitignored; tests/mac/.env.example shows it): anything ssh accepts, so
// user@host, user@ip, or a Host alias from ~/.ssh/config. It is driven
// through this branch's own pleiades binary, as a generic_ssh device in a
// private project under .git: a Pleiades user's path, so what Pleiades
// cannot yet do for it shows (docs/design/maccheck.md). Before anything
// else, maccheck probes the Mac's SSH port; then net.ssh.ping and
// exec.command check Pleiades reaches it and that it is a Mac with go and
// caffeinate. Each failure stops it with what to do, including how to
// point it at another Mac or stop using one. A linked worktree with no
// tests/mac/.env of its own reads the clone's main worktree's, so one file
// serves every worktree. The committed tree (never the
// working tree) is unpacked into a directory of its own on the Mac, and
// the packages run there through tools/testgate as pr-check runs them,
// under caffeinate so the Mac does not sleep partway.
//
// Usage:
//
//	go run ./tools/maccheck [-optional] [-mac target] [-commit HEAD] [-p N] package...
//
// With -optional, a clone with no Mac configured is a skip, said so, which
// is how pr-check calls it. Exit status: 0 passed or skipped, 1 a test
// failed, 2 the Mac could not be used.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"
)

// envKey names the Mac, in the environment or in envFile.
const envKey = "PLEIADES_MAC"

// envFile holds this clone's envKey, relative to the repository's top
// level. It is gitignored, so it is never committed and git archive never
// sends it.
const envFile = "tests/mac/.env"

// changeOrRemove is what every refusal ends with: how to point maccheck at
// another Mac, and how to stop using one. file is the settings file that
// named the Mac, or "" when the environment or -mac did.
func changeOrRemove(file string) string {
	if file == "" {
		return "  To test on another Mac:   name it with -mac or " + envKey + " (user@host, user@ip, or an ~/.ssh/config alias)\n" +
			"  To stop testing on a Mac: unset " + envKey + ", and delete " + envFile + " if one names a Mac"
	}
	return "  To test on another Mac:   set " + envKey + " in " + file + " (user@host, user@ip, or an ~/.ssh/config alias)\n" +
		"  To stop testing on a Mac: delete " + file
}

// importPath is what a package argument must look like, since each is
// written into the command the Mac runs.
var importPath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

func main() {
	project, err := privateProject()
	if err != nil {
		fmt.Fprintf(os.Stderr, "maccheck: finding this worktree's git directory: %v\n", err)
		os.Exit(2)
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, pleiadesRemote{project: project}))
}

// run is main with its arguments, its output and the remote end given, so
// tests drive every step but the real SSH session.
func run(args []string, stdout, stderr io.Writer, remote remoteRunner) int {
	fs := flag.NewFlagSet("maccheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	optional := fs.Bool("optional", false, "a clone with no Mac configured is a skip rather than an error")
	target := fs.String("mac", "", "the Mac to use, instead of "+envKey)
	commit := fs.String("commit", "HEAD", "the commit whose tree runs on the Mac")
	parallel := fs.Int("p", 0, "how many packages testgate runs at once on the Mac (0: its default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	packages := fs.Args()
	for _, p := range packages {
		if !importPath.MatchString(p) {
			fmt.Fprintf(stderr, "maccheck: %q is not an import path\n", p)
			return 2
		}
	}

	if strings.HasPrefix(*commit, "-") {
		fmt.Fprintf(stderr, "maccheck: %q is not a commit: it begins with \"-\", which git reads as an option\n", *commit)
		return 2
	}

	mac, file := *target, ""
	if mac == "" {
		var err error
		if mac, file, err = configured(); err != nil {
			fmt.Fprintf(stderr, "maccheck: %v\n", err)
			return 2
		}
	}
	if mac == "" {
		if *optional {
			fmt.Fprintf(stdout, "maccheck: no Mac configured (%s in %s), so macOS was NOT checked\n", envKey, envFile)
			return 0
		}
		fmt.Fprintf(stderr, "maccheck: no Mac is configured. Name one, then run this again:\n  cp %[1]s.example %[1]s, and set %[2]s in it to user@host, user@ip, or an ~/.ssh/config alias\n", envFile, envKey)
		return 2
	}
	if err := checkTarget(mac); err != nil {
		fmt.Fprintf(stderr, "maccheck: %v\n%s\n", err, changeOrRemove(file))
		return 2
	}
	if len(packages) == 0 {
		fmt.Fprintln(stdout, "maccheck: the branch changes no container-free package, so nothing runs on the Mac")
		return 0
	}
	sha, dirty, err := resolveCommit(*commit)
	if err != nil {
		fmt.Fprintf(stderr, "maccheck: %v\n", err)
		return 2
	}

	if err := remote.Reachable(mac); err != nil {
		fmt.Fprintf(stderr, "maccheck: %v\n%s\n", err, changeOrRemove(file))
		return 2
	}
	if err := remote.Ready(mac); err != nil {
		fmt.Fprintf(stderr, "maccheck: %v\n%s\n", err, changeOrRemove(file))
		return 2
	}
	if dirty {
		fmt.Fprintf(stdout, "maccheck: the working tree has uncommitted changes; the Mac tests the committed tree of %s, without them\n", short(sha))
	}
	fmt.Fprintf(stdout, "maccheck: %d package(s) at %s on %s\n", len(packages), short(sha), mac)
	if err := remote.Ship(mac, sha); err != nil {
		fmt.Fprintf(stderr, "maccheck: sending the tree to %s: %v\n", mac, err)
		return 2
	}
	passed, err := remote.Test(mac, sha, testScript(sha, *parallel, packages), stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "maccheck: running the tests on %s: %v\n%s\n", mac, err, changeOrRemove(file))
		return 2
	}
	if !passed {
		fmt.Fprintf(stderr, "maccheck: the tests failed on %s (macOS)\n", mac)
		return 1
	}
	fmt.Fprintf(stdout, "maccheck: all tests passed on %s (macOS)\n", mac)
	return 0
}

// testScript is what the Mac runs in the unpacked tree: pr-check's two
// passes over the container-free packages, the full pass and three strict
// repeats, under caffeinate (display, idle, disk, and on power system
// sleep held off) so the Mac stays awake until they end. Standard error
// joins standard output so they read in order, and the script always
// exits 0, ending with exitMarker and the tests' exit status (see
// exitMarker).
func testScript(sha string, parallel int, packages []string) string {
	pkgs := strings.Join(packages, " ")
	p := fmt.Sprintf("-p %d", parallel)
	return fmt.Sprintf(`exec 2>&1
export PATH="$PATH:%[1]s"
cd "$HOME/%[2]s/%[3]s" || { echo "%[4]s2"; exit 0; }
caffeinate -dims /bin/sh -c 'go run ./tools/testgate -full %[5]s %[6]s && go run ./tools/testgate -repeat 3 -strict %[5]s %[6]s'
echo "%[4]s$?"`, macPath, checkoutDir, sha, exitMarker, p, pkgs)
}

// checkTarget refuses a Mac target that ssh would read as an option, or
// that cannot name one host: one beginning with "-", or holding a space or
// a control character (a trailing comment on its line of envFile, say).
func checkTarget(mac string) error {
	if strings.HasPrefix(mac, "-") {
		return fmt.Errorf("%q is not an ssh target: it begins with \"-\", which ssh reads as an option", mac)
	}
	if strings.IndexFunc(mac, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("%q is not an ssh target: it holds a space or a control character", mac)
	}
	return nil
}

// short is a commit's first twelve hex digits.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
