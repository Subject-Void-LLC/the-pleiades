package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
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
//     password on its command line, or --password-file, a file `pleiades
//     seal` wrote: sealed to this project's key, it opens with that key;
//     sealed to a passphrase, it opens with --passphrase or
//     --passphrase-stdin. It is the way for an agent whose shell refuses a
//     redirect, and the file never holds the password readably.
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
//     key (ed25519, or the --key-type asked for) and a new random password
//     unless --no-password, neither chosen, typed nor shown by anyone. Only
//     the key's type, fingerprint and public half are printed, as text or
//     with --json (addcredential_generate.go). It refuses to replace a
//     stored credential without --replace, since the machine that
//     credential reaches would then be out of reach.
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
//
// The SNMP flags (addcredential_snmp.go) store the device's SNMP family
// apart from its login: given alone they replace only the SNMP fields and
// keep a stored login, and given with login flags both are saved. A -stdin
// flag reads one line of standard input, and every such line is read by one
// reader in one order: the login's secret, the enable secret, then the SNMP
// community or the SNMPv3 passphrases.
func runAddCredential(args []string) error {
	fs := flag.NewFlagSet("add-credential", flag.ContinueOnError)
	addProjectFlags(fs)
	dir := fs.String("dir", ".", "project directory")
	username := fs.String("username", "", "account name to authenticate as")
	password := fs.String("password", "", "password to authenticate with (prompted interactively if --key is also absent and this is empty)")
	stdinPassword := fs.Bool("password-stdin", false, "read the password to authenticate with as one line on standard input")
	passwordFile := fs.String("password-file", "", "path to a file `pleiades seal` wrote holding the password, opened with this project's seal key or, sealed to a passphrase, with --passphrase or --passphrase-stdin")
	keyPath := fs.String("key", "", "path to a PEM private key file to authenticate with")
	certPath := fs.String("certificate", "", "path to a PEM client certificate to present, which requires --key; the file holds certificates only, its key going to --key")
	pfxPath := fs.String("pfx", "", "path to a PKCS#12 (.pfx/.p12) bundle holding a certificate and its key")
	promptPassphrase := fs.Bool("passphrase", false, "prompt for the passphrase of the private key, the bundle or a passphrase-sealed --password-file")
	stdinPassphrase := fs.Bool("passphrase-stdin", false,
		"read the passphrase of the private key, the bundle or a passphrase-sealed --password-file as one line on standard input")
	generate := fs.Bool("generate", false, "generate a new random key (ed25519 unless --key-type says otherwise) and password, for a machine Pleiades will create; prints the key's type, fingerprint and public key line, never a secret")
	replace := fs.Bool("replace", false, "with --generate, replace a credential already stored for the device")
	keyType := fs.String("key-type", string(credential.DefaultKeyType), "with --generate, the key to make: ed25519, rsa-3072, rsa-4096, ecdsa-p256 or ecdsa-p384")
	noPassword := fs.Bool("no-password", false, "with --generate, store the key alone with no password, so a device that refuses the key fails the login instead of taking a password")
	asJSON := fs.Bool("json", false, "with --generate, print one JSON document instead of text: the key's type, fingerprint and public key line, never a secret")
	stdinToken := fs.Bool("token-stdin", false, "read a bearer token as one line on standard input, for a device whose API takes one (a Kubernetes cluster, a language model provider's API key)")
	promptEnable := fs.Bool("enable-password", false, "prompt for the secret a network device asks for to enter privileged mode (Cisco's enable), when this account lands in user mode")
	stdinEnable := fs.Bool("enable-password-stdin", false, "read that enable secret as one line on standard input, after the login's own secret when that is piped too")
	noDevice := fs.Bool("no-device", false, "store the credential for a name no inventory device has (a sync source's account, a device added later), marked unbound; without it such a name is stored with a warning, and from 0.4.0 refused")
	snmp := addSNMPFlags(fs)

	name, err := parseOneArg(fs, args)
	if err != nil {
		return fmt.Errorf("usage: pleiades add-credential <device> [--username <user>] "+
			"[--password <password> | --password-stdin | --password-file <sealed file> [--passphrase] | --key <path> [--passphrase] | --certificate <path> --key <path> | --pfx <path> --passphrase | --token-stdin | "+
			"--generate [--key-type <type>] [--no-password] [--replace] [--json]] [--enable-password | --enable-password-stdin] "+
			"[--snmp-community | --snmp-community-stdin [--allow-short-community] | --snmp-user <user> [--snmp-auth <protocol> [--snmp-priv <protocol>] [--snmp-passphrase-stdin]]] [--no-device]; "+
			"standard input holds one line for each secret a -stdin flag asks for, in this order: the login's secret, the enable secret, then the SNMP community or the SNMPv3 authentication and privacy passphrases: %w", err)
	}

	// The key type is read before anything else, so a typo prints no warning
	// and consumes no input.
	generatedType, err := checkGenerateFlags(func(flagName string) bool { return flagGiven(fs, flagName) }, *generate, *keyType)
	if err != nil {
		return err
	}
	if (*promptEnable || *stdinEnable) && *stdinToken {
		return fmt.Errorf("an enable secret goes with a device login, so it cannot be combined with --token-stdin")
	}
	if *promptEnable && *stdinEnable {
		return fmt.Errorf("--enable-password and --enable-password-stdin are mutually exclusive: an enable secret is read one way")
	}
	if err := snmp.check(); err != nil {
		return err
	}
	// The SNMP family and the login are saved apart: given SNMP flags alone,
	// only SNMP is replaced and a stored login is kept, and the reverse.
	families := familiesGiven(fs)
	// Checked before any secret is read, so a refusal consumes no input and
	// a warning is printed before a prompt asks for the secret.
	unbound, err := credentialBinding(*dir, name, *noDevice, os.Stderr)
	if err != nil {
		return err
	}
	opts := credential.SaveOptions{Unbound: unbound, Families: families, AllowShortCommunity: *snmp.allowShort}
	// Every line standard input holds is read by one reader, in the order
	// the usage gives, whatever mix of -stdin flags asks for them.
	in := &stdinQueue{n: snmp.stdinLines()}
	for _, piped := range []bool{*stdinPassword, *stdinPassphrase, *stdinEnable, *stdinToken} {
		if piped {
			in.n++
		}
	}
	// withSNMP adds the SNMP family to a login when SNMP flags were given.
	withSNMP := func(cred credential.Credential) (credential.Credential, error) {
		if families&credential.SNMPFamily == 0 {
			return cred, nil
		}
		return snmp.read(in, cred)
	}
	if families == credential.SNMPFamily {
		cred, err := withSNMP(credential.Credential{})
		if err != nil {
			return err
		}
		return saveCredential(*dir, name, cred, opts)
	}
	if *stdinToken {
		// A token is the whole identity, so it combines with nothing: a
		// cluster credential carries one way of authenticating, and an
		// account name beside a token would be a second answer to who.
		if *username != "" || *password != "" || *stdinPassword || *passwordFile != "" || *keyPath != "" || *certPath != "" || *pfxPath != "" ||
			*promptPassphrase || *stdinPassphrase || *generate {
			return fmt.Errorf("--token-stdin is the whole credential, so it cannot be combined with --username, a password, a key, a certificate, a bundle or --generate")
		}
		token, err := in.next()
		if err != nil {
			return err
		}
		if strings.TrimSpace(token) == "" {
			return fmt.Errorf("--token-stdin read an empty token")
		}
		cred, err := withSNMP(credential.Credential{Token: strings.TrimSpace(token)})
		if err != nil {
			return err
		}
		return saveCredential(*dir, name, cred, opts)
	}
	if *generate {
		if *password != "" || *stdinPassword || *passwordFile != "" || *keyPath != "" || *certPath != "" || *pfxPath != "" || *promptPassphrase || *stdinPassphrase {
			return fmt.Errorf("--generate makes its own key and password, so it cannot be combined with --password, --key, --certificate, --pfx or a passphrase")
		}
		if *username == "" {
			return fmt.Errorf("--username is required")
		}
		// The secrets on standard input are read only once the stored login
		// is known to be replaceable (generateCredential), so a refused
		// --replace consumes none of them.
		return generateCredential(generateRequest{
			dir: *dir, device: name, username: *username,
			options: credential.GenerateOptions{KeyType: generatedType, NoPassword: *noPassword},
			replace: *replace, asJSON: *asJSON, save: opts,
			readEnable: func() (string, error) { return readEnable(in, *stdinEnable, *promptEnable) },
			readSNMP:   withSNMP,
		})
	}

	usesCertificate := *certPath != "" || *pfxPath != ""

	if *username == "" && !usesCertificate {
		return fmt.Errorf("--username is required")
	}
	if *username != "" && usesCertificate {
		return fmt.Errorf("--username does not apply to a certificate: the target maps the certificate to an account, so naming one here would be a second answer")
	}
	if (*password != "" || *stdinPassword || *passwordFile != "") && (*keyPath != "" || usesCertificate) {
		return fmt.Errorf("--password is mutually exclusive with --key, --certificate and --pfx: a device authenticates one way at a time")
	}
	passwordWays := 0
	for _, given := range []bool{*password != "", *stdinPassword, *passwordFile != ""} {
		if given {
			passwordWays++
		}
	}
	if passwordWays > 1 {
		return fmt.Errorf("--password, --password-stdin and --password-file are mutually exclusive: a password is read one way")
	}
	if *pfxPath != "" && (*certPath != "" || *keyPath != "") {
		return fmt.Errorf("--pfx already holds a certificate and its key, so it cannot be combined with --certificate or --key")
	}
	if *certPath != "" && *keyPath == "" {
		return fmt.Errorf("--certificate needs --key: a certificate alone cannot prove possession")
	}
	if (*promptPassphrase || *stdinPassphrase) && *keyPath == "" && *pfxPath == "" && *passwordFile == "" {
		return fmt.Errorf("--passphrase and --passphrase-stdin only apply to --key, --pfx or a passphrase-sealed --password-file")
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
	// echoes a secret on some machines and not others. A piped passphrase is
	// the login's secret, so it is the first line the queue gives.
	readPassphrase := func(promptText string) (string, error) {
		switch {
		case *stdinPassphrase:
			return in.next()
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
		// A certificate file holding its key too, the shape a "cert and key"
		// bundle takes, is refused rather than stored: the certificate field
		// is the one credential field printed and carried as public.
		if problem := wire.ClientCertificateProblem(certBytes); problem != "" {
			return fmt.Errorf("--certificate %s %s: give the private key to --key in a file of its own, or the pair as a bundle to --pfx", *certPath, problem)
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
		read, err := in.next()
		if err != nil {
			return err
		}
		cred.Password = read
	case *passwordFile != "":
		opened, err := openPasswordFile(*dir, *passwordFile, *promptPassphrase || *stdinPassphrase, readPassphrase)
		if err != nil {
			return err
		}
		cred.Password = opened
	default:
		prompted, err := promptSecret("password: ")
		if err != nil {
			return err
		}
		cred.Password = prompted
	}

	if cred.EnablePassword, err = readEnable(in, *stdinEnable, *promptEnable); err != nil {
		return err
	}

	if cred, err = withSNMP(cred); err != nil {
		return err
	}
	for _, w := range sameAsUsernameWarnings(cred) {
		fmt.Fprintln(os.Stderr, "WARNING: "+w)
	}
	return saveCredential(*dir, name, cred, opts)
}

// sameAsUsernameWarnings says when a password or enable secret is the
// account's own username. Every run masks a credential's secret by value
// wherever it appears, so such a username, and every word holding it, is
// masked in all output too: a lab whose login was cisco/cisco read cisco.com
// as ********.com in every show command (blind CVD lab, run 8, 2026-11-14).
// Nothing is refused; the masking is right, since that word is the password.
func sameAsUsernameWarnings(cred credential.Credential) []string {
	if cred.Username == "" {
		return nil
	}
	var out []string
	for _, s := range []struct{ what, value string }{{"password", cred.Password}, {"enable secret", cred.EnablePassword}} {
		// Exact, as masking by value is: a secret differing only in case
		// does not mask the username. The message names neither value,
		// since the username here is the secret.
		if s.value != "" && s.value == cred.Username {
			out = append(out, fmt.Sprintf("the %s is the username itself; every run masks a secret wherever it appears, so the username, and every word holding it, will read as ******** in all output", s.what))
		}
	}
	return out
}

// openPasswordFile opens the sealed file at path for its password. A
// passphrase is read, through readPassphrase, only when one was asked for
// (passphraseGiven), so the credential package can refuse a passphrase a
// key-sealed file does not take and ask for one a passphrase-sealed file
// does.
func openPasswordFile(dir, path string, passphraseGiven bool, readPassphrase func(string) (string, error)) (string, error) {
	data, err := readBounded(path, sealed.MaxFileSize)
	if err != nil {
		return "", err
	}
	file, err := sealed.Parse(data)
	if errors.Is(err, sealed.ErrNotSealed) {
		return "", fmt.Errorf("%s is not a sealed file: seal the password with `pleiades seal`, or pipe a plaintext one to --password-stdin", path)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	key, err := credential.ResolveMasterKey(vaultDir(dir))
	if err != nil {
		return "", fmt.Errorf("failed to resolve master key: %w", err)
	}
	var passphrase func() (string, error)
	if passphraseGiven {
		passphrase = func() (string, error) { return readPassphrase("sealed file passphrase: ") }
	}
	opened, err := credential.OpenSealedPassword(vaultDir(dir), key, file, passphrase)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return opened, nil
}

// saveCredential encrypts cred into dir's credential store for device name,
// marked as opts says.
func saveCredential(dir, name string, cred credential.Credential, opts credential.SaveOptions) error {
	key, err := credential.ResolveMasterKey(vaultDir(dir))
	if err != nil {
		return fmt.Errorf("failed to resolve master key: %w", err)
	}

	if err := credential.SaveFileStoreWith(vaultDir(dir), key, name, cred, opts); err != nil {
		return fmt.Errorf("failed to save credential: %w", err)
	}

	if note := shortCommunityNote(name, cred); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	fmt.Printf("stored credential for device %q%s%s\n", name, familyNote(opts), unboundNote(opts))
	return nil
}

// readEnable reads the enable secret the flags ask for: the next line of
// standard input, a prompt with no echo, or "" when neither was given.
func readEnable(in *stdinQueue, stdin, prompted bool) (string, error) {
	switch {
	case stdin:
		return in.next()
	case prompted:
		return promptSecret("enable secret: ")
	}
	return "", nil
}

// flagGiven reports whether name was set on fs's command line. A boolean
// flag given as false asks for nothing, so it counts as not given.
func flagGiven(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name != name {
			return
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			given = f.Value.String() == "true"
			return
		}
		given = true
	})
	return given
}

// unboundNote is what a stored message adds for an entry marked unbound.
func unboundNote(opts credential.SaveOptions) string {
	if opts.Unbound {
		return " (unbound: no inventory device has this name)"
	}
	return ""
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
