// The `controller mesh` subcommands: bootstrapping the mesh identity
// hierarchy, and issuing a credential for a Runner.
//
// # Why this is a host command and not an API route
//
// The same reason bootstrap-admin is. `mesh init` has to run before the
// broker requires authentication, which is before the Controller can
// reliably reach the broker, and on a clean machine before anybody can
// log in. A route that can only be called when the thing it configures is
// already working is not a bootstrap.
//
// # What leaves this process, and what must not
//
// The hierarchy has three levels and only one of them may stay online.
// `mesh init` writes the two offline keys to files and says so in its own
// output; it stores only the account SIGNING key, sealed, through
// internal/meshkey. Nothing here can persist an operator key: the store
// takes an account and keeps the one field meant to be online.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/meshid"
	"github.com/Subject-Void-LLC/the-pleiades/internal/meshkey"
)

// meshFilePerm is the mode every file these commands write carries.
//
// 0400 rather than 0600: nothing re-reads these after they are written,
// so write permission would only make an accidental truncation possible.
const meshFilePerm = 0o400

// runMesh dispatches the mesh subcommands.
func runMesh(ctx context.Context, deps *adminDeps, args []string) error {
	if len(args) == 0 {
		printMeshUsage()
		return errors.New("no mesh subcommand given")
	}
	switch args[0] {
	case "init":
		return runMeshInit(ctx, deps, args[1:])
	case "issue":
		return runMeshIssue(ctx, deps, args[1:])
	case "show":
		return runMeshShow(ctx, deps)
	default:
		printMeshUsage()
		return fmt.Errorf("unknown mesh subcommand %q", args[0])
	}
}

// printMeshUsage lists the mesh subcommands.
func printMeshUsage() {
	fmt.Fprint(os.Stderr, `Usage: controller mesh <subcommand> [flags]

  init  --dir <path>    mint the operator, the system account and this
                        deployment's account; store the account signing key
                        sealed in the database; write the broker's
                        configuration and the two OFFLINE keys to <path>
  issue --out <path>    issue a credential for a runner from the stored
                        signing key
        [--expiry <dur>]
  show                  report which account this deployment signs for and
                        which key it signs with

`)
}

// runMeshInit mints the hierarchy and records the one key that stays here.
func runMeshInit(ctx context.Context, deps *adminDeps, args []string) error {
	fs := flag.NewFlagSet("mesh init", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory to write the broker configuration and the offline keys into")
	name := fs.String("name", "pleiades", "a label for the account; NATS identifies an account by its public key, never by this")
	keyID := fs.String("key-id", "", "operator-facing name for this signing key (default: mesh-<date>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("--dir is required: it is where the broker configuration and the two offline keys are written")
	}
	if *keyID == "" {
		*keyID = "mesh-" + time.Now().UTC().Format("20060102")
	}

	// Refuse to write into a directory that already holds key material,
	// rather than overwriting it. Overwriting an operator key is
	// unrecoverable: every account JWT it signed becomes unverifiable and
	// the whole mesh has to be re-minted.
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", *dir, err)
	}

	op, err := meshid.NewOperator(*name + "-operator")
	if err != nil {
		return err
	}
	sys, err := meshid.NewSystemAccount(op, "SYS")
	if err != nil {
		return err
	}
	app, err := meshid.NewAccount(op, *name)
	if err != nil {
		return err
	}

	opSeed, err := op.Seed()
	if err != nil {
		return fmt.Errorf("reading the operator seed: %w", err)
	}

	// Written before the database is touched. A file that fails to write
	// after the key is stored leaves a deployment whose Controller can
	// mint credentials for a broker nobody can configure.
	files := []struct {
		name    string
		content []byte
		secret  bool
	}{
		{"nats.conf", []byte(brokerConfig(op, sys, app)), false},
		{"operator.jwt", []byte(op.JWT), false},
		{"sys-account.jwt", []byte(sys.JWT), false},
		{"account.jwt", []byte(app.JWT), false},
		{"operator.nk", opSeed, true},
	}
	root, err := os.OpenRoot(*dir)
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dir, err)
	}
	defer func() { _ = root.Close() }()
	for _, f := range files {
		if err := writeNewFile(root, f.name, f.content); err != nil {
			return err
		}
	}

	if err := meshkey.NewStore(deps.client).Save(ctx, *keyID, app); err != nil {
		return fmt.Errorf("storing the account signing key: %w", err)
	}

	fmt.Printf(`Mesh identity created.

  account          %s
  signing key      %s (stored here, sealed under MASTER_ENCRYPTION_KEY)

Written to %s:

  nats.conf        the broker's configuration. Public material: an operator
                   JWT and two account JWTs, no private key. Give it to the
                   broker and restart it.
  operator.jwt     the same operator JWT on its own.
  sys-account.jwt  the system account JWT. JetStream refuses to start in
                   operator mode without one.
  account.jwt      this deployment's account JWT.

  operator.nk      THE OPERATOR KEY. Move it off this machine now.

The operator key can mint a new account, and a new account is a new tenant
of this mesh. It is needed again only to re-mint an account or to publish a
revocation, so it belongs on an air-gapped machine or in an HSM, not here.
Nothing in this deployment reads it: the Controller holds the account
signing key alone, which is what keeps a Controller compromise from being a
mesh compromise.

Next: give nats.conf to the broker, then `+"`controller mesh issue --out runner.creds`"+`
for each runner, and point it at the file with %s.
`, app.Subject, *keyID, *dir, "NATS_CREDS_FILE")
	return nil
}

// brokerConfig renders the operator-mode configuration for the broker.
//
// It carries only what nats-server cannot be told as a command line flag,
// which is the same rule the Helm chart's own nats.conf states about
// itself, and there is a measured reason rather than a stylistic one:
// nats-server 2.14.4 refuses a file that repeats a flag, so a jetstream
// block naming store_dir beside "-sd /data" produces "Duplicate
// 'store_dir' configuration" and the server exits at boot.
func brokerConfig(op *meshid.Operator, sys, app *meshid.Account) string {
	return fmt.Sprintf(`# Generated by `+"`controller mesh init`"+`. Public material only.
#
# Add this to the broker with -c. Keep the existing flags: this file
# carries only what they cannot express.
operator: %s
system_account: %s
resolver: MEMORY
resolver_preload: {
  %s: %s
  %s: %s
}
`, op.JWT, sys.Subject, sys.Subject, sys.JWT, app.Subject, app.JWT)
}

// runMeshIssue mints one credential from the stored signing key.
func runMeshIssue(ctx context.Context, deps *adminDeps, args []string) error {
	fs := flag.NewFlagSet("mesh issue", flag.ContinueOnError)
	out := fs.String("out", "", "file to write the credential to")
	expiry := fs.Duration("expiry", meshid.DefaultFleetExpiry, "how long the credential is valid for")
	label := fs.String("label", "runner", "a label recorded in the credential's name, for reading connz")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out is required: a credential carries a private seed and is not printed to a terminal")
	}
	if *expiry <= 0 {
		return errors.New("--expiry must be positive: a credential that never expires is a permanent bearer token")
	}

	store := meshkey.NewStore(deps.client)
	account, err := store.ActiveAccount(ctx)
	if err != nil {
		if errors.Is(err, meshkey.ErrNoActiveKey) {
			return errors.New("this deployment holds no mesh signing key; run `controller mesh init` first")
		}
		return err
	}
	issuer, err := store.Issuer(ctx, account)
	if err != nil {
		return err
	}

	cred, err := issuer.Issue(meshid.FleetRunnerGrant(*label), *expiry)
	if err != nil {
		return err
	}
	outDir, outName := filepath.Split(*out)
	if outDir == "" {
		outDir = "."
	}
	root, err := os.OpenRoot(outDir)
	if err != nil {
		return fmt.Errorf("opening %s: %w", outDir, err)
	}
	defer func() { _ = root.Close() }()
	if err := writeNewFile(root, outName, cred.Creds); err != nil {
		return err
	}

	fmt.Printf(`Credential written to %s (mode 0400).

  account   %s
  expires   %s

Point the runner at it with NATS_CREDS_FILE=%s.

It expires. A runner whose credential lapses is evicted by the broker and
stops reconnecting, which looks exactly like a runner with no work, so
replace this before %s.
`, *out, account, cred.Expires.UTC().Format(time.RFC3339), *out, cred.Expires.UTC().Format(time.RFC3339))
	return nil
}

// runMeshShow reports what this deployment signs with.
func runMeshShow(ctx context.Context, deps *adminDeps) error {
	store := meshkey.NewStore(deps.client)
	account, err := store.ActiveAccount(ctx)
	if err != nil {
		if errors.Is(err, meshkey.ErrNoActiveKey) {
			fmt.Println("This deployment holds no mesh signing key, so it mints no identities and its broker is not authenticating clients.")
			fmt.Println("Run `controller mesh init` to create one.")
			return nil
		}
		return err
	}
	keyID, err := store.ActiveKeyID(ctx, account)
	if err != nil {
		return err
	}
	fmt.Printf("account      %s\nsigning key  %s\n", account, keyID)
	return nil
}

// writeNewFile writes content as name inside root, refusing to replace
// what is there.
//
// Through an os.Root rather than a bare path, which is the shape
// internal/setup's own file writer already uses for the env file it
// creates. The root confines every write to the directory the operator
// named, so a name carrying "../" cannot place an operator key somewhere
// else, and it is also what keeps this off gosec's G304 list without a
// waiver: there is no variable path to include.
//
// O_EXCL rather than a truncating create, because two of these files are
// key material and one of them is the operator key. Overwriting that is
// unrecoverable: every account JWT it signed stops verifying, and the
// whole mesh has to be re-minted from nothing.
//
// There are in fact two barriers here, and knowing which one is doing
// what matters if either is ever changed. The files are written 0400, so
// a truncating create fails anyway. What O_EXCL adds is a LEGIBLE
// refusal: swapping it for O_TRUNC was tried, and the overwrite is still
// refused, with "permission denied" naming a path and nothing else. An
// operator reading that would reasonably conclude the directory is
// misconfigured rather than that they are about to destroy their mesh.
// So the mode is the protection and this is the explanation.
func writeNewFile(root *os.Root, name string, content []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, meshFilePerm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; refusing to replace it, because overwriting key material cannot be undone", name)
		}
		return fmt.Errorf("creating %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}
