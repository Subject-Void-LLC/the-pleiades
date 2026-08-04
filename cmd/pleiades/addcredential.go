package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/SubjectVoidLLC/the-pleiades/internal/credential"
	"golang.org/x/term"
)

// runAddCredential stores one device's SSH credential, encrypted at rest,
// in <dir>/.pleiades/credentials.yaml (internal/credential's Walk-tier
// Store adapter, file_store.go). Unlike add-host's inventory.yaml, this
// file holds AES-256-GCM ciphertext, not something an operator can
// hand-edit, so this command is the only way to populate it.
//
// Exactly one authentication method is accepted per invocation:
// --password (a literal value, or an interactive no-echo prompt if
// omitted) or --key (a path to a PEM private key file, optionally
// protected by a passphrase, itself prompted for with no echo). Prompting
// by default, rather than requiring the secret as a bare flag value, is
// deliberate: a flag value is visible in shell history and in this
// process's argument list to any other user on the same machine for as
// long as the process runs, which a prompt avoids.
func runAddCredential(args []string) error {
	name, rest, err := splitPositional(args)
	if err != nil {
		return fmt.Errorf("usage: pleiades add-credential <device> --username <user> [--password <password> | --key <path> [--passphrase]]: %w", err)
	}

	fs := flag.NewFlagSet("add-credential", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	username := fs.String("username", "", "account name to authenticate as")
	password := fs.String("password", "", "password to authenticate with (prompted interactively if --key is also absent and this is empty)")
	keyPath := fs.String("key", "", "path to a PEM private key file to authenticate with")
	promptPassphrase := fs.Bool("passphrase", false, "prompt for the private key's passphrase (only meaningful with --key)")

	if err := fs.Parse(rest); err != nil {
		return err
	}

	if *username == "" {
		return fmt.Errorf("--username is required")
	}
	if *password != "" && *keyPath != "" {
		return fmt.Errorf("--password and --key are mutually exclusive: a device authenticates one way at a time")
	}
	if *promptPassphrase && *keyPath == "" {
		return fmt.Errorf("--passphrase only applies to --key")
	}

	cred := credential.Credential{Username: *username}

	switch {
	case *keyPath != "":
		keyBytes, err := os.ReadFile(*keyPath)
		if err != nil {
			return fmt.Errorf("failed to read private key %s: %w", *keyPath, err)
		}
		cred.PrivateKeyPEM = keyBytes
		if *promptPassphrase {
			passphrase, err := promptSecret("private key passphrase: ")
			if err != nil {
				return err
			}
			cred.Passphrase = passphrase
		}
	case *password != "":
		cred.Password = *password
	default:
		prompted, err := promptSecret("password: ")
		if err != nil {
			return err
		}
		cred.Password = prompted
	}

	key, err := credential.ResolveMasterKey(*dir)
	if err != nil {
		return fmt.Errorf("failed to resolve master key: %w", err)
	}

	if err := credential.SaveFileStore(*dir, key, name, cred); err != nil {
		return fmt.Errorf("failed to save credential: %w", err)
	}

	fmt.Printf("stored credential for device %q\n", name)
	return nil
}

// promptSecret prints prompt to stderr (stdout is reserved for this
// command's own machine-readable-ish confirmation output) and reads one
// line from stdin with terminal echo disabled, so the secret never
// appears on screen or in any terminal scrollback buffer. It requires
// stdin to be a real terminal; callers that need to supply a secret
// non-interactively (tests, scripts) should use --password or --key
// instead, which bypass this prompt entirely.
func promptSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	secret, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("failed to read secret from terminal (use --password or --key for non-interactive use): %w", err)
	}
	if len(secret) == 0 {
		return "", fmt.Errorf("empty secret is not allowed")
	}
	return string(secret), nil
}
