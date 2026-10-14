// The add-registry-credential command: an account for one container
// registry, kept apart from every device's credential.
package main

import (
	"flag"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// runAddRegistryCredential stores an account for the registry at its
// positional host. A run gives it only to a method pulling or pushing an
// image on that registry (sdk.RegistryCredentials), so it never reaches a
// device or another registry. It lives in the credential file's own
// registries section, which no device name can address.
func runAddRegistryCredential(args []string) error {
	host, rest, err := splitPositional(args, map[string]bool{"password-stdin": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades add-registry-credential <registry host> --username <user> [--password-stdin]: %w", err)
	}
	fs := flag.NewFlagSet("add-registry-credential", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	username := fs.String("username", "", "the registry account")
	stdinPassword := fs.Bool("password-stdin", false, "read the password or token as one line on standard input")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *username == "" {
		return fmt.Errorf("--username is required")
	}
	normalized := wire.RegistryHost(host)
	if normalized == "" {
		return fmt.Errorf("the registry host is empty")
	}
	var password string
	if *stdinPassword {
		password, err = prompt.SecretFromStdin()
	} else {
		password, err = promptSecret("password or token: ")
	}
	if err != nil {
		return err
	}
	if password == "" {
		return fmt.Errorf("the password or token is empty")
	}
	key, err := credential.ResolveMasterKey(*dir)
	if err != nil {
		return fmt.Errorf("failed to resolve credential master key: %w", err)
	}
	if err := credential.SaveRegistryCredential(*dir, key, normalized, *username, password); err != nil {
		return err
	}
	fmt.Printf("stored credential for registry %q\n", normalized)
	return nil
}
