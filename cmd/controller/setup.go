// This file is the `controller setup` subcommand: it generates the master
// encryption key and the JWT secret, writes them where the deployment reads
// them, and creates the first administrator, in one command.
//
// The logic lives in internal/setup. What is here is only what a composition
// root has to own: flags, the environment the database is named in, whether a
// person is at a terminal, and the steps after the file is written that need
// this binary's own admin wiring (recording the key, and bootstrap-admin).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// setupCommand is the subcommand's name.
const setupCommand = "setup"

// exitIncomplete is what `setup --check` exits with when the env file is
// readable but not yet complete, so `make up` can tell "run setup" from
// "something is wrong" by exit status alone.
const exitIncomplete = 3

// hostDirEnv names the variable a container passes to say where the
// directory it writes into lives on the host, purely so messages name the
// file the operator will actually look for. It is never used to open
// anything.
const hostDirEnv = "PLEIADES_SETUP_HOST_DIR"

// isSetupCommand reports whether args ask for the setup subcommand.
func isSetupCommand(args []string) bool {
	return len(args) > 0 && args[0] == setupCommand
}

// setupFlags is the parsed command line.
type setupFlags struct {
	target, dir, adminEmail, maxOutage, secretName, namespace string
	check, nonInteractive, passwordStdin                      bool
	newJWT, force, destroyKey                                 bool
}

// parseSetupFlags parses args, which excludes the subcommand name.
func parseSetupFlags(args []string) (setupFlags, error) {
	var f setupFlags
	fs := flag.NewFlagSet(setupCommand, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = printSetupUsage
	fs.StringVar(&f.target, "target", "compose", "compose or helm")
	fs.StringVar(&f.dir, "dir", ".", "directory to write into")
	fs.BoolVar(&f.check, "check", false, "report whether the compose env file is complete, and write nothing")
	fs.BoolVar(&f.nonInteractive, "non-interactive", false, "ask nothing, even at a terminal")
	fs.StringVar(&f.adminEmail, "admin-email", "", "create the first administrator with this address")
	fs.BoolVar(&f.passwordStdin, "password-stdin", false, "read the administrator's password as one line on standard input")
	fs.StringVar(&f.maxOutage, "max-outage", "", "the longest link outage to survive, such as 30m")
	fs.BoolVar(&f.newJWT, "new-jwt-secret", false, "replace the JWT secret (with --force)")
	fs.BoolVar(&f.force, "force", false, "allow changing a reversible setting named with it")
	fs.BoolVar(&f.destroyKey, "destroy-existing-encryption-key", false, "replace the master key; refused while any stored data is encrypted under it")
	fs.StringVar(&f.secretName, "secret-name", "", "the Helm Secret's name")
	fs.StringVar(&f.namespace, "namespace", "", "the Helm release's namespace")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		return f, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return f, nil
}

// runSetup runs the subcommand and returns a process exit code. args
// includes the subcommand name at index 0, as runAdmin's does.
func runSetup(args []string) int {
	f, err := parseSetupFlags(args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller setup: %v\n", err)
		return 2
	}
	dsn, err := setupDatabaseDSN()
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller setup: %v\n", err)
		return 1
	}

	opts := setup.Options{
		Target: setup.Target(f.target), Dir: f.dir, DisplayDir: os.Getenv(hostDirEnv),
		Check: f.check, MaxOutage: f.maxOutage,
		NewJWTSecret: f.newJWT, Force: f.force, DestroyKey: f.destroyKey,
		SecretName: f.secretName, Namespace: f.namespace, DatabaseDSN: dsn,
	}

	// A person is at a terminal only when both ends are one. Standard error
	// matters as much as standard input: the key is shown there, and a
	// terminal on stdin with stderr redirected to a file would write the
	// key into that file.
	var screen *setup.Screen
	stopGuard := func() {}
	if !f.nonInteractive && !f.check && prompt.IsTerminal(os.Stdin) && prompt.IsTerminal(os.Stderr) {
		term, err := prompt.OpenTerminal()
		if err != nil {
			fmt.Fprintf(os.Stderr, "controller setup: %v\n", err)
			return 1
		}
		screen = setup.NewScreen(term)
		stopGuard = screen.GuardInterrupts("setup was interrupted, and wrote nothing.", os.Exit)
	}

	ctx := context.Background()
	result, err := setup.Run(ctx, opts, screen, os.Stdout)
	stopGuard()
	if code, done := reportSetupError(err, f.check); done {
		return code
	}
	if f.check {
		fmt.Fprintf(os.Stdout, "%s holds a MASTER_ENCRYPTION_KEY and a JWT_SECRET.\n", result.File)
		return 0
	}
	if result.Key == nil || dsn == "" {
		return 0
	}
	if err := finishSetup(ctx, dsn, result, f, screen); err != nil {
		fmt.Fprintf(os.Stderr, "controller setup: %v\n", err)
		return 1
	}
	return 0
}

// reportSetupError prints err and returns the exit code for it, or reports
// that there was no error.
func reportSetupError(err error, check bool) (int, bool) {
	var refusal *setup.Refusal
	switch {
	case err == nil:
		return 0, false
	case check && errors.Is(err, setup.ErrIncomplete):
		fmt.Fprintln(os.Stderr, err)
		return exitIncomplete, true
	case errors.Is(err, setup.ErrInterrupted):
		// Stopped on purpose, with a second Ctrl+C: the status a shell
		// reports for an interrupt, the same one the signal guard exits with.
		fmt.Fprintln(os.Stderr, err)
		return 130, true
	case errors.As(err, &refusal):
		fmt.Fprintln(os.Stderr, refusal.Message)
		return 1, true
	default:
		fmt.Fprintf(os.Stderr, "controller setup: %v\n", err)
		return 1, true
	}
}

// setupDatabaseDSN returns the database the controller would open, but only
// when DB_DSN or DB_PATH names one explicitly. The server's own fallback, a
// controller.db in the working directory, is exactly the database setup must
// not count: it would count a file that is not the one the controller uses,
// or create one. A relative SQLite path is refused for the same reason, since
// it is relative to wherever setup happens to run.
func setupDatabaseDSN() (string, error) {
	dsn, path := os.Getenv("DB_DSN"), os.Getenv("DB_PATH")
	switch {
	case dsn != "" && path != "":
		return "", errors.New("DB_DSN and DB_PATH are both set; set exactly one")
	case path != "":
		dsn = "sqlite://" + path
	case dsn == "":
		return "", nil
	}
	sqlitePath, isSQLite := strings.CutPrefix(dsn, "sqlite://")
	if !isSQLite && !strings.Contains(dsn, "://") {
		sqlitePath, isSQLite = dsn, true
	}
	if isSQLite && !filepath.IsAbs(sqlitePath) {
		return "", fmt.Errorf("the SQLite database path %q is relative, so it names a different file depending on where setup runs; give an absolute path", sqlitePath)
	}
	return dsn, nil
}

// finishSetup opens the database under the key setup just wrote, records the
// key in the activity trail, and creates the first administrator.
//
// It reuses the admin wiring and the bootstrap-admin subcommand itself rather
// than a copy of either. The key reaches them as an in-memory envelope
// service, never through the environment.
func finishSetup(ctx context.Context, dsn string, result setup.Result, f setupFlags, screen *setup.Screen) error {
	svc, err := crypto.NewEnvelopeService(result.Key, result.KeyVersion, nil, "")
	if err != nil {
		return fmt.Errorf("the key was written to %s, and could not be loaded to finish setting up: %w", result.File, err)
	}
	deps, cleanup, err := openAdminDepsWith(ctx, dsn, svc)
	if err != nil {
		return fmt.Errorf("the key was written to %s, and the database could not be opened to finish setting up: %w", result.File, err)
	}
	defer cleanup()

	registry := keyregistry.NewAuditedStore(
		keyregistry.NewEntStore(deps.client), activity.NewEntStore(deps.client),
		func(context.Context) string { return "controller-setup" }, deps.logger)
	record, _, err := registry.Register(ctx, keyregistry.Record{
		Fingerprint: crypto.Fingerprint(result.Key), Version: result.KeyVersion,
		Origin: keyregistry.OriginSetup, Possession: result.Possession,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "The key was written, and could not be recorded in the activity trail (%v). The controller records it when it first starts.\n", err)
	} else {
		fmt.Fprintf(os.Stdout, "The activity trail records encryption key %s.\n", record.Describe())
	}

	email := f.adminEmail
	if email == "" && screen != nil {
		email, err = screen.AskAdminEmail()
		if err != nil {
			return err
		}
	}
	if email == "" {
		fmt.Fprintf(os.Stdout, "No administrator was created. Create one with: %s\n", bootstrapHint(f.target, "<address>"))
		return nil
	}

	bootstrapArgs := []string{"--email", email}
	if f.passwordStdin {
		bootstrapArgs = append(bootstrapArgs, "--password-stdin")
	}
	stopGuard := func() {}
	if screen != nil {
		stopGuard = screen.GuardInterrupts(fmt.Sprintf("setup wrote %s, and was interrupted before the administrator was created. Create it with: %s", result.File, bootstrapHint(f.target, email)), os.Exit)
	}
	err = runBootstrapAdmin(ctx, deps, bootstrapArgs)
	stopGuard()
	if err != nil {
		return fmt.Errorf("%s was written, and the administrator was not created: %w. Create it with: %s", result.File, err, bootstrapHint(f.target, email))
	}
	return nil
}

// bootstrapHint is the command that creates the administrator later, in the
// form the target runs it.
func bootstrapHint(target, email string) string {
	if setup.Target(target) == setup.TargetCompose {
		return "docker compose run --rm controller bootstrap-admin --email " + email
	}
	return "controller bootstrap-admin --email " + email
}

// printSetupUsage explains the subcommand.
func printSetupUsage() {
	fmt.Fprint(os.Stderr, `Usage: controller setup [flags]

Generates the master encryption key and the JWT secret, writes them where the
deployment reads them, and creates the first administrator.

  --target compose    write .env in --dir, the file docker compose reads (the default)
  --target helm       write pleiades-secret.yaml and pleiades-values.yaml in --dir

It never replaces the master key while any stored data is encrypted under it,
and it counts that data in the database DB_DSN or DB_PATH names. At a
terminal it shows the new key once, clears it, and has you type it back.

Changing an existing .env names the setting to change:

  --max-outage <duration> --force     change the outage budget
  --new-jwt-secret --force            replace the JWT secret
  --destroy-existing-encryption-key   replace the master key, which also asks
                                      you to type a phrase naming the key

Without a terminal, or with --non-interactive, it asks nothing:
--max-outage sets the budget (default 30m), and --admin-email with
--password-stdin creates the administrator.

  --check    exit 0 if .env holds a key and a JWT secret, 3 if it does not
             yet, 1 if it cannot be read; writes nothing
`)
}
