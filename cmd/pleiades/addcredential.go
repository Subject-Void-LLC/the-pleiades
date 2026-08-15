package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
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
	name, rest, err := splitPositional(args, map[string]bool{"passphrase": true})
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

// promptSecret reads one secret with terminal echo disabled.
//
// A thin wrapper over internal/prompt rather than its own implementation:
// cmd/controller's administrative subcommands need identical behavior, and
// two copies of "keep this off the screen and out of the argument list" is
// two places for it to drift. The wrapper stays so this file's call sites
// read the same as before.
func promptSecret(promptText string) (string, error) {
	return prompt.Secret(promptText)
}
