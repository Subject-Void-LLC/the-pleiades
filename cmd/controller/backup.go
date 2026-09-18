// The backup, restore and decommission subcommands: flag parsing, where the
// key for a restore comes from, and the typed confirmation decommissioning
// needs.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// The three subcommands this file runs. Each is run by a make target in the
// compose stack's backup service, whose image carries PostgreSQL's own
// pg_dump and pg_restore; the controller image does not.
const (
	backupCommand       = "backup"
	restoreCommand      = "restore"
	decommissionCommand = "decommission"
)

// backupHostDirEnv and restoreHostDirEnv name, for messages only, where the
// backup and restore directories are on the host, as hostDirEnv does for
// setup's.
const (
	backupHostDirEnv  = "PLEIADES_BACKUP_HOST_DIR"
	restoreHostDirEnv = "PLEIADES_RESTORE_HOST_DIR"
)

// isBackupCommand reports whether args ask for one of this file's
// subcommands.
func isBackupCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case backupCommand, restoreCommand, decommissionCommand:
		return true
	}
	return false
}

// backupFlags is the parsed command line of any of the three.
type backupFlags struct {
	dir, backups, from, file string
	keyStdin, destroy        bool
}

// parseBackupFlags parses args for the subcommand named by args[0].
func parseBackupFlags(args []string) (backupFlags, error) {
	var f backupFlags
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&f.dir, "dir", ".", "the directory holding .env")
	fs.StringVar(&f.backups, "backups", "backups", "the directory backups are written to")
	switch args[0] {
	case restoreCommand:
		fs.StringVar(&f.from, "from", "", "the directory holding the backup to restore (default: --backups)")
		fs.StringVar(&f.file, "file", "", "the backup's file name, inside --from")
		fs.BoolVar(&f.keyStdin, "key-stdin", false, "when .env holds no key, read the backup's key as one line on standard input")
	case decommissionCommand:
		fs.BoolVar(&f.destroy, "destroy-deployment", false, "confirm without a terminal")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		return f, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if args[0] == restoreCommand && f.file == "" {
		return f, errors.New("--file names the backup to restore")
	}
	if f.from == "" {
		f.from = f.backups
	}
	return f, nil
}

// runBackupCommand runs one of the three and returns a process exit code.
func runBackupCommand(args []string) int {
	f, err := parseBackupFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller %s: %v\n", args[0], err)
		return 2
	}
	if args[0] == decommissionCommand {
		return runDecommission(f, os.Stdin, os.Stdout)
	}

	// Only an explicitly configured database: the embedded SQLite fallback
	// would back up a file nobody meant.
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		fmt.Fprintf(os.Stderr, "controller %s: DB_DSN is not set; it names the PostgreSQL database to %s\n", args[0], args[0])
		return 1
	}
	opts := backup.Options{
		DSN: dsn, SetupDir: f.dir, SetupDisplay: os.Getenv(hostDirEnv),
		BackupDir: f.backups, BackupDisplay: os.Getenv(backupHostDirEnv),
	}
	ctx := context.Background()
	if args[0] == backupCommand {
		if _, err := backup.Take(ctx, opts, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "controller backup: %v\n", err)
			return 1
		}
		return 0
	}

	source, stop, err := restoreKeySource(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller restore: %v\n", err)
		return 1
	}
	_, err = backup.Restore(ctx, backup.RestoreOptions{
		Options: opts, ArchiveDir: f.from, ArchiveDisplay: os.Getenv(restoreHostDirEnv), Archive: f.file,
	}, source, os.Stdout)
	stop()
	if errors.Is(err, setup.ErrInterrupted) {
		fmt.Fprintln(os.Stderr, "restore was interrupted, and changed nothing.")
		return 130
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller restore: %v\n", err)
		return 1
	}
	return 0
}

// restoreKeySource is where a restore gets the backup's key when .env holds
// none: standard input with --key-stdin, a hidden prompt at a terminal, or
// nowhere, which the restore turns into a refusal naming --key-stdin. stop
// ends the interrupt guard the terminal needs.
func restoreKeySource(f backupFlags) (backup.KeySource, func(), error) {
	if f.keyStdin {
		return func(string) ([]byte, error) { return readKeyLine(os.Stdin) }, func() {}, nil
	}
	if !prompt.IsTerminal(os.Stdin) || !prompt.IsTerminal(os.Stderr) {
		return nil, func() {}, nil
	}
	term, err := prompt.OpenTerminal()
	if err != nil {
		return nil, nil, err
	}
	stop := setup.NewScreen(term).GuardInterrupts("restore was interrupted, and changed nothing.", os.Exit)
	return func(label string) ([]byte, error) {
		question := ".env holds no key. Type or paste the master encryption key this backup was taken under: "
		if label != "" {
			question = fmt.Sprintf(".env holds no key. Type or paste master encryption key %s, which this backup was taken under: ", label)
		}
		text, err := term.Secret(question)
		if err != nil {
			return nil, err
		}
		return crypto.DecodeKey(strings.TrimSpace(text), "the key you entered")
	}, stop, nil
}

// readKeyLine reads one line from r as a key. The line is the whole input
// a script sends, so anything after it is ignored rather than read.
func readKeyLine(r io.Reader) ([]byte, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return crypto.DecodeKey(strings.TrimSpace(line), "the key on standard input")
}

// runDecommission shows what `make decom` removes and keeps, and exits 0
// only once that is confirmed: by the typed phrase at a terminal, or by
// --destroy-deployment without one. It removes nothing itself.
func runDecommission(f backupFlags, in *os.File, out io.Writer) int {
	plan, err := backup.PlanDecommission(f.dir, os.Getenv(hostDirEnv), f.backups, os.Getenv(backupHostDirEnv))
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller decommission: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, plan.Warning(time.Now()))
	fmt.Fprintln(out)
	if f.destroy {
		fmt.Fprintln(out, "Confirmed by --destroy-deployment.")
		return 0
	}
	if !prompt.IsTerminal(in) {
		fmt.Fprintln(os.Stderr, "Nothing was removed. Without a terminal to type the confirmation at, pass --destroy-deployment: make decom DECOM_FLAGS=--destroy-deployment")
		return 1
	}
	term, err := prompt.OpenTerminal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller decommission: %v\n", err)
		return 1
	}
	answer, err := term.Line(fmt.Sprintf("Type %q to remove it, or anything else to stop: ", plan.Phrase()))
	if err != nil || answer != plan.Phrase() {
		fmt.Fprintln(os.Stderr, "The phrase did not match. Nothing was removed.")
		return 1
	}
	return 0
}
