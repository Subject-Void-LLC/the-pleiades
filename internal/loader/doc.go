// Package loader finds external Collections in one directory, checks
// them, and registers their methods so a runbook can call them by name
// exactly as it calls a built-in method.
//
// An external Collection is a program built outside this repository with
// pkg/external. The Pleiades runs it as a child process, beside itself, on the
// machine running `pleiades run` or on the Runner. It is never copied onto
// a managed device. It reaches a device the way a built-in method does,
// through pkg/sdk.Connect and the one credential it is handed on stdin.
//
// Load works in two passes. The first inspects and describes every
// program in the directory and validates everything it was told. The
// second registers every method through collection.Register. If anything
// in the first pass is wrong, Load refuses the whole directory, names
// each program (and method) it refused and why, and registers nothing.
// A refused method never falls through to some other implementation of
// the same name: a name that is already taken, or claimed twice, is a
// refusal, not a tie to break.
//
// # The trust model
//
// The directory is the trust root, and it is a small one on purpose.
// Whoever can write to it can make The Pleiades run a program of their
// choosing, as the user The Pleiades runs as. So Load refuses a directory, or
// a program in it, that is group- or world-writable, or owned by anyone
// but this process's own user or root. A program must be a regular file
// with its owner-execute bit set. A symlink is refused rather than
// followed, because a link can point anywhere, including somewhere other
// people can write. A setuid or setgid program is refused too: nothing an
// external Collection does needs to change who it runs as.
//
// Load pins each program by the SHA-256 digest of its bytes. Every call
// re-checks the file and re-hashes it immediately before running it, and
// refuses to run a program whose bytes no longer match, naming both
// digests. That catches a program changed after it was loaded. It does
// not protect against a program that was malicious when it was loaded:
// nothing here verifies who built it. Signing and verification are a later
// phase. Until then the honest statement is that an external Collection is
// trusted exactly as much as the account that put it in the directory.
//
// # Confinement
//
// A program runs as the same OS user as The Pleiades, so the process boundary
// alone would decide only what The Pleiades hands it, not what it can go and
// read for itself: the project's credential store and its master key,
// the user's SSH keys, and The Pleiades's own starting environment under
// /proc (FAILURE_PATTERNS 251). So every run is confined, through Linux's
// Landlock, to what the program needs: its own directory, the system's
// libraries, certificates, resolver files and time zone database, the
// known_hosts file, /dev/null, a private scratch directory set as its
// TMPDIR, and any path the operator grants (Options.ReadPaths). From
// Landlock ABI 6 it also cannot signal The Pleiades or reach an abstract Unix
// socket outside its domain. Before the first program starts, Load marks
// this process not dumpable, so its memory and environment are closed to
// the program even through /proc.
//
// Network access is not confined: a method must reach devices on their
// own ports, which no rule can list in advance. Where Landlock is absent
// (an older Linux kernel, and every other platform) Load refuses rather
// than running anything unconfined.
//
// # What crosses the boundary
//
// In, on stdin only: the method name, the mode (execute or check), the
// task's params, the target device's identity, address and capabilities,
// and the credential The Pleiades resolved for the task. That credential comes
// from the credential manager exactly as it does for a built-in method (the
// machine credential bound to the template, resolved at fan-out, or the
// device's own stored credential when the template binds none), through
// sdk.RunbookContext.InjectSecrets; the loader adds no credential path of
// its own. Nothing secret ever goes in
// argv, which is a single command word, or in the environment, which is
// rebuilt from a short allowlist (PATH, HOME, TMPDIR, LANG, LC_ALL, TZ and
// PLEIADES_KNOWN_HOSTS, each only when set). Nothing else the parent holds
// in its environment is handed over, and neither is SSH_AUTH_SOCK, which
// would hand the program every key in the user's agent. That decides what
// the program is handed; confinement (above) is what keeps it from
// reading the rest, the parent's starting environment included.
//
// Out: one JSON response on file descriptor 3, carrying whether anything
// changed, the stats the method recorded, and an error string. Nothing
// else is read back. Output on stdout and stderr is captured, capped,
// masked and logged at debug level, and never parsed.
//
// # Bounds
//
// Every run has a wall-clock limit, every captured stream has a size cap,
// and each way a program can misbehave has its own outcome and its own
// message: it did not finish in time, it exited non-zero, it exited
// without writing a response, it wrote a malformed one, it wrote one that
// ended partway through, or it wrote one over the size cap. A program
// that floods stdout or stderr has the excess discarded while it runs, so
// it cannot stall on a full pipe, and a program that floods its response
// channel has that channel closed on it. Every value of the credential the
// program was handed is masked out of everything captured before it
// reaches an error or a log line.
//
// # Engine version
//
// A method may declare the oldest engine it runs against as
// ">=MAJOR.MINOR.PATCH". Load compares that with Options.EngineVersion,
// the version of the running build, and refuses a method the build is too
// old for. Every build today is an unreleased "dev" build, since nothing
// cuts release versions yet, and an unreleased build cannot be compared
// with anything. Such a build accepts the method and records a warning
// naming it and its constraint (Set.Warnings), rather than refusing every
// constrained method or quietly pretending the check ran.
package loader
