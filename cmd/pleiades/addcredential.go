package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
)

// runAddCredential stores one device's SSH credential, encrypted at rest,
// in <dir>/.pleiades/credentials.yaml (internal/credential's Crawl-tier
// Store adapter, file_store.go). Unlike add-host's inventory.yaml, this
// file holds AES-256-GCM ciphertext, not something an operator can
// hand-edit, so this command is the only way to populate it.
//
// Exactly one authentication method is accepted per invocation:
//
//   - --password, a literal value or an interactive no-echo prompt if
//     omitted, or --password-stdin, one line on standard input, for a
//     script, which has no terminal to prompt and should not put the
//     password on its command line.
//   - --key, a path to a PEM private key file, optionally protected by a
//     passphrase, itself prompted for with no echo.
//   - --certificate together with --key, a PEM client certificate and the
//     key that proves it, for a device reached by TLS mutual
//     authentication rather than by a password. The key must be
//     unencrypted: nothing decrypts a loose private key on that path, so a
//     passphrase is refused here rather than stored and ignored.
//   - --pfx, a PKCS#12 bundle holding both of those sealed together,
//     unlocked with --passphrase at the moment it is used rather than
//     here.
//   - --generate, for a machine Pleiades is about to create: a new random
//     ed25519 key and a new random password, neither chosen, typed nor
//     shown by anyone. Only the key's public half is printed. It refuses
//     to replace a stored credential without --replace, since the machine
//     that credential reaches would then be out of reach.
//
// Prompting by default, rather than requiring the secret as a bare flag
// value, is deliberate: a flag value is visible in shell history and in
// this process's argument list to any other user on the same machine for
// as long as the process runs, which a prompt avoids. A certificate and a
// bundle are named by PATH for the same reason and one more: neither fits
// on a command line.
//
// --username is required for the first two and refused for the last two.
// A certificate names the account it authenticates as, by carrying a
// principal the target maps to one, so a username beside it is a second
// answer to a question that already has one.
func runAddCredential(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"password-stdin": true, "passphrase": true, "passphrase-stdin": true, "generate": true, "replace": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades add-credential <device> [--username <user>] "+
			"[--password <password> | --password-stdin | --key <path> [--passphrase] | --certificate <path> --key <path> | --pfx <path> --passphrase | --generate [--replace]]: %w", err)
	}

	fs := flag.NewFlagSet("add-credential", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	username := fs.String("username", "", "account name to authenticate as")
	password := fs.String("password", "", "password to authenticate with (prompted interactively if --key is also absent and this is empty)")
	stdinPassword := fs.Bool("password-stdin", false, "read the password to authenticate with as one line on standard input")
	keyPath := fs.String("key", "", "path to a PEM private key file to authenticate with")
	certPath := fs.String("certificate", "", "path to a PEM client certificate to present, which requires --key")
	pfxPath := fs.String("pfx", "", "path to a PKCS#12 (.pfx/.p12) bundle holding a certificate and its key")
	promptPassphrase := fs.Bool("passphrase", false, "prompt for the private key's or the bundle's passphrase")
	stdinPassphrase := fs.Bool("passphrase-stdin", false,
		"read the private key's or the bundle's passphrase as one line on standard input")
	generate := fs.Bool("generate", false, "generate a new random ed25519 key and password, for a machine Pleiades will create; prints only the public key")
	replace := fs.Bool("replace", false, "with --generate, replace a credential already stored for the device")

	if err := fs.Parse(rest); err != nil {
		return err
	}

	if *replace && !*generate {
		return fmt.Errorf("--replace only applies to --generate")
	}
	if *generate {
		if *password != "" || *stdinPassword || *keyPath != "" || *certPath != "" || *pfxPath != "" || *promptPassphrase || *stdinPassphrase {
			return fmt.Errorf("--generate makes its own key and password, so it cannot be combined with --password, --key, --certificate, --pfx or a passphrase")
		}
		if *username == "" {
			return fmt.Errorf("--username is required")
		}
		return generateCredential(*dir, name, *username, *replace)
	}

	usesCertificate := *certPath != "" || *pfxPath != ""

	if *username == "" && !usesCertificate {
		return fmt.Errorf("--username is required")
	}
	if *username != "" && usesCertificate {
		return fmt.Errorf("--username does not apply to a certificate: the target maps the certificate to an account, so naming one here would be a second answer")
	}
	if (*password != "" || *stdinPassword) && (*keyPath != "" || usesCertificate) {
		return fmt.Errorf("--password is mutually exclusive with --key, --certificate and --pfx: a device authenticates one way at a time")
	}
	if *password != "" && *stdinPassword {
		return fmt.Errorf("--password and --password-stdin are mutually exclusive: a password is read one way")
	}
	if *pfxPath != "" && (*certPath != "" || *keyPath != "") {
		return fmt.Errorf("--pfx already holds a certificate and its key, so it cannot be combined with --certificate or --key")
	}
	if *certPath != "" && *keyPath == "" {
		return fmt.Errorf("--certificate needs --key: a certificate alone cannot prove possession")
	}
	if (*promptPassphrase || *stdinPassphrase) && *keyPath == "" && *pfxPath == "" {
		return fmt.Errorf("--passphrase and --passphrase-stdin only apply to --key or --pfx")
	}
	if (*promptPassphrase || *stdinPassphrase) && *certPath != "" {
		// Refused rather than stored and ignored. Nothing decrypts a loose
		// private key on the certificate path, so a passphrase here would be
		// written to disk, carried to the device and discarded, and the run
		// would fail with a parse error naming neither.
		return fmt.Errorf("a passphrase does not apply to --certificate: nothing decrypts a loose private key, " +
			"so supply the key unencrypted, or supply --pfx, which is unlocked with its passphrase at the point of use")
	}
	if *promptPassphrase && *stdinPassphrase {
		return fmt.Errorf("--passphrase and --passphrase-stdin are mutually exclusive: a passphrase is read one way")
	}

	// readPassphrase is the one place the two non-interactive and
	// interactive forms are chosen between, so the three branches below
	// cannot drift. Reading from a pipe is opted into by a flag rather than
	// entered automatically when stdin is not a terminal, for the reason
	// internal/prompt states: an automatic fallback means the same command
	// echoes a secret on some machines and not others.
	readPassphrase := func(promptText string) (string, error) {
		switch {
		case *stdinPassphrase:
			return prompt.SecretFromStdin()
		case *promptPassphrase:
			return promptSecret(promptText)
		default:
			return "", nil
		}
	}

	cred := credential.Credential{Username: *username}

	switch {
	case *pfxPath != "":
		// Base64 because a credential's values travel as strings the whole
		// way, and choosing the encoding once here beats every reader
		// choosing one. The bundle stays sealed: this command does not
		// unlock it, and nothing does until the task that presents it runs.
		bundle, err := os.ReadFile(*pfxPath)
		if err != nil {
			return fmt.Errorf("failed to read bundle %s: %w", *pfxPath, err)
		}
		cred.PFXBase64 = base64.StdEncoding.EncodeToString(bundle)
		passphrase, err := readPassphrase("bundle passphrase: ")
		if err != nil {
			return err
		}
		cred.Passphrase = passphrase
	case *certPath != "":
		certBytes, err := os.ReadFile(*certPath)
		if err != nil {
			return fmt.Errorf("failed to read certificate %s: %w", *certPath, err)
		}
		keyBytes, err := os.ReadFile(*keyPath)
		if err != nil {
			return fmt.Errorf("failed to read private key %s: %w", *keyPath, err)
		}
		cred.CertificatePEM = certBytes
		cred.PrivateKeyPEM = keyBytes
	case *keyPath != "":
		keyBytes, err := os.ReadFile(*keyPath)
		if err != nil {
			return fmt.Errorf("failed to read private key %s: %w", *keyPath, err)
		}
		cred.PrivateKeyPEM = keyBytes
		passphrase, err := readPassphrase("private key passphrase: ")
		if err != nil {
			return err
		}
		cred.Passphrase = passphrase
	case *password != "":
		cred.Password = *password
	case *stdinPassword:
		read, err := prompt.SecretFromStdin()
		if err != nil {
			return err
		}
		cred.Password = read
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

// generateCredential stores a new random key and password for device in
// dir's vault and prints the key's public half, labelled user@device,
// which is what a machine is given so the key may log in to it.
func generateCredential(dir, device, username string, replace bool) error {
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		return fmt.Errorf("failed to resolve master key: %w", err)
	}
	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		return fmt.Errorf("failed to open the credential store: %w", err)
	}
	_, err = store.Lookup(context.Background(), device)
	switch {
	case err == nil && !replace:
		return fmt.Errorf("device %q already has a credential; --replace discards it, and whatever it logs in to is then out of reach", device)
	case err != nil && !errors.Is(err, credential.ErrNotFound):
		return fmt.Errorf("failed to read the credential store: %w", err)
	}
	comment := username + "@" + device
	cred, err := credential.Generate(username, comment)
	if err != nil {
		return err
	}
	public, err := credential.PublicKey(cred, comment)
	if err != nil {
		return err
	}
	if err := credential.SaveFileStore(dir, key, device, cred); err != nil {
		return fmt.Errorf("failed to save credential: %w", err)
	}
	fmt.Printf("stored a generated credential for device %q: an ed25519 key and a %d-character password, for %s\n",
		device, credential.GeneratedPasswordLength, username)
	fmt.Printf("public key: %s\n", public)
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
