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
// cannot yet do for it shows (docs/design/os-checks.md). Before anything
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
//	go run ./tools/maccheck [-optional] [-phase all|prepare|test] [-mac target] [-commit HEAD] [-p N] package...
//	go run ./tools/maccheck -phase vuln -govulncheck v1.6.0 [-mac target] [-commit HEAD]
//
// -phase splits the run in two, so pr-check can start the Mac's tests and
// run the Linux ones while they go: prepare checks the Mac, builds
// pleiades and sends the tree, all of which uses this machine; test runs
// the tests on the tree prepare sent, which uses only the Mac. all (the
// default) is both. Interrupted (SIGINT or SIGTERM) while the tests run,
// it stops them on the Mac too. vuln runs govulncheck on the tree prepare
// sent, on the Mac's memory (it took 7 GB there with a vulnerable call to
// trace), scanning for this machine's operating system and architecture
// with its Go release, so its answer is the one a scan here gives.
// tools/internal/oscheck holds the flow it shares with tools/bsdcheck and
// tools/wincheck.
//
// With -optional, a clone with no Mac configured is a skip, said so, which
// is how pr-check calls it. Exit status: 0 passed or skipped, 1 a test (or
// govulncheck) failed, 2 the Mac could not be used.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
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

func main() {
	project, err := privateProject()
	if err != nil {
		fmt.Fprintf(os.Stderr, "maccheck: finding this worktree's git directory: %v\n", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, func(mac string) oscheck.Leg { return newRemote(project, mac) })
	stop()
	os.Exit(code)
}

// run is main with its arguments, its output and the Mac end given, so
// tests drive every step but the real SSH session.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, remote func(mac string) oscheck.Leg) int {
	fs := flag.NewFlagSet("maccheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := oscheck.Flags(fs, "the Mac")
	target := fs.String("mac", "", "the Mac to use, instead of "+envKey)
	containers := fs.Bool("containers", false, "run the container-dependent packages against the Mac's Podman, with a coverage measurement for -coverage-out, instead of the container-free passes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	o.Packages = fs.Args()
	if err := o.Check(); err != nil {
		fmt.Fprintf(stderr, "maccheck: %v\n", err)
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
		if o.Optional {
			fmt.Fprintf(stdout, "maccheck: no Mac configured (%s in %s), so macOS was NOT checked\n", envKey, envFile)
			return oscheck.RecordNotConfigured(o, "maccheck", "macOS", fmt.Sprintf("no Mac configured (%s in %s)", envKey, envFile), stderr)
		}
		fmt.Fprintf(stderr, "maccheck: no Mac is configured. Name one, then run this again:\n  cp %[1]s.example %[1]s, and set %[2]s in it to user@host, user@ip, or an ~/.ssh/config alias\n", envFile, envKey)
		return 2
	}
	if err := checkTarget(mac); err != nil {
		fmt.Fprintf(stderr, "maccheck: %v\n%s\n", err, changeOrRemove(file))
		return 2
	}
	leg := remote(mac)
	if *containers {
		c, ok := leg.(interface{ forContainers() pleiadesRemote })
		if !ok {
			fmt.Fprintf(stderr, "maccheck: -containers needs the real Mac end, which can run Podman\n")
			return 2
		}
		leg = c.forContainers()
	}
	flow := oscheck.Flow{Tool: "maccheck", Machine: "the Mac", OS: "macOS", Target: mac, Advice: changeOrRemove(file)}
	return flow.Run(ctx, o, stdout, stderr, leg)
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
