// Package loader: the knobs Load and every proxy it registers run with,
// and the defaults a zero value stands for.
package loader

import (
	"log/slog"
	"time"
)

// The defaults Options falls back to for any zero field.
const (
	// DefaultDescribeTimeout bounds one "describe" run. Describing is a
	// program printing a static document, so ten seconds is generous;
	// anything slower is a program doing work it was not asked for.
	DefaultDescribeTimeout = 10 * time.Second

	// DefaultInvokeTimeout bounds one method call. It matches the order of
	// a long real task (a package upgrade, a firmware copy) rather than a
	// typical one, because killing a legitimate slow task halfway through
	// is worse than waiting on a stuck one.
	DefaultInvokeTimeout = 30 * time.Minute

	// DefaultMaxOutput caps how much of stdout, and separately of stderr,
	// is kept from one run. The rest is read and thrown away.
	DefaultMaxOutput int64 = 1 << 20

	// DefaultMaxResponse caps the one response frame, and the describe
	// document, a program may write.
	DefaultMaxResponse int64 = 4 << 20
)

// killGrace is how long a program is given to exit after SIGTERM before
// os/exec kills it outright, and also how long a finished program's
// stdout and stderr may stay held open by something it left behind.
const killGrace = 5 * time.Second

// responseGrace is how long the response channel is given to reach end
// of file after the program itself has exited. A program that started a
// background process may have handed it the channel, and that process
// could hold it open forever; this bounds the wait.
const responseGrace = time.Second

// Options configures Load and every proxy it registers.
type Options struct {
	// EngineVersion is the version of the running build, compared against
	// each method's engine version constraint. "dev" or empty means an
	// unreleased build, which cannot be compared: a constrained method is
	// then accepted with a warning (Set.Warnings) rather than refused.
	EngineVersion string

	// DescribeTimeout bounds each "describe" run. Zero or less means
	// DefaultDescribeTimeout.
	DescribeTimeout time.Duration

	// InvokeTimeout bounds each method call. Zero or less means
	// DefaultInvokeTimeout. The caller's own context still applies, so a
	// run canceled sooner stops sooner.
	InvokeTimeout time.Duration

	// MaxOutput caps how many bytes of stdout, and separately of stderr,
	// are kept from each run. Zero or less means DefaultMaxOutput.
	MaxOutput int64

	// MaxResponse caps the response frame and the describe document. Zero
	// or less means DefaultMaxResponse.
	MaxResponse int64

	// Logger receives what Load loaded, its warnings, and every run's
	// captured (masked) output at debug level. Nil means slog.Default().
	Logger *slog.Logger

	// ReadPaths are extra directories or files an operator lets every
	// program read, such as a directory of files a method uploads to a
	// device. A program is otherwise confined to its own directory, the
	// system's libraries, certificates and resolver files, the known_hosts
	// file, and a private scratch directory. Each path must be absolute
	// and must exist, and Load refuses one that overlaps a protected path
	// or contains the home directory.
	ReadPaths []string

	// ProtectedPaths are directories that hold credentials, such as a
	// project's .pleiades directory. Neither the collections directory nor
	// any ReadPaths entry may be, contain, or sit inside one. The home
	// directory is always protected against being granted whole.
	ProtectedPaths []string

	// waitDelay overrides killGrace. It is unexported because a deployment
	// has no reason to tune it, while a test proving the escalation from
	// SIGTERM to a kill should not have to wait five real seconds for it.
	waitDelay time.Duration

	// graceAfterExit overrides responseGrace, for the same reason.
	graceAfterExit time.Duration

	// abi is the Landlock ABI version Load found, which every run's
	// ruleset is built for.
	abi int

	// testWritable are directories a program may also write, so a test's
	// fixture can record what it received somewhere the test can read it
	// back. The program's own directory stays read-only, as in production.
	testWritable []string

	// beforeStart, when set, runs after a program has been opened,
	// verified and found approved, immediately before it is started. It
	// exists for the test that swaps the file in that window.
	beforeStart func()

	// execByPath starts a program by its path instead of through the file
	// that was verified. It exists only as that test's control, proving
	// the window is real.
	execByPath bool

	// unconfined starts programs without confinement and leaves the
	// process dumpable. It is unexported so that only this package's own
	// tests can set it, as the control that proves confinement is what
	// stops a program reaching what the confined tests say it cannot.
	unconfined bool
}

// withDefaults returns o with every zero field replaced by its default.
func (o Options) withDefaults() Options {
	if o.DescribeTimeout <= 0 {
		o.DescribeTimeout = DefaultDescribeTimeout
	}
	if o.InvokeTimeout <= 0 {
		o.InvokeTimeout = DefaultInvokeTimeout
	}
	if o.MaxOutput <= 0 {
		o.MaxOutput = DefaultMaxOutput
	}
	if o.MaxResponse <= 0 {
		o.MaxResponse = DefaultMaxResponse
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.waitDelay <= 0 {
		o.waitDelay = killGrace
	}
	if o.graceAfterExit <= 0 {
		o.graceAfterExit = responseGrace
	}
	return o
}
