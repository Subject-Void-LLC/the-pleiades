// The seal-key and seal commands: sealing a secret to a project's public key
// or to a passphrase (internal/sealed), so that it can be handed over as a
// file without being readable in it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// sealKeyView is what seal-key reports, as text or JSON.
type sealKeyView struct {
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	Method      string `json:"method"`
	Created     bool   `json:"created"`
}

// runSealKey prints the project's public seal key, making the key pair on
// first use. The public key is not a secret: it is what someone seals to,
// on any machine, so that only this project opens the file.
func runSealKey(args []string) error {
	fs := flag.NewFlagSet("seal-key", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: pleiades seal-key [--dir DIR] [--json]")
	}
	key, err := credential.ResolveMasterKey(*dir)
	if err != nil {
		return fmt.Errorf("failed to resolve master key: %w", err)
	}
	priv, created, err := credential.SealKey(*dir, key)
	if err != nil {
		return err
	}
	pub := priv.Public()
	view := sealKeyView{PublicKey: pub.String(), Fingerprint: pub.Fingerprint(), Method: sealed.MethodKey, Created: created}
	if *asJSON {
		return writeJSON(os.Stdout, view)
	}
	// The key alone on standard output, so `--to "$(pleiades seal-key)"`
	// works; what it is goes to standard error.
	if created {
		fmt.Fprintf(os.Stderr, "made this project's seal key, %s\n", view.Fingerprint)
	} else {
		fmt.Fprintf(os.Stderr, "this project's seal key, %s\n", view.Fingerprint)
	}
	fmt.Println(view.PublicKey)
	return nil
}

// runSeal seals one secret into a new file, to a public key (--to) or to a
// passphrase, whichever the person sealing chooses. The secret is typed at a
// prompt, twice, or piped with --secret-stdin; never a flag value.
func runSeal(args []string) error {
	fs := flag.NewFlagSet("seal", flag.ContinueOnError)
	to := fs.String("to", "", "the public seal key to seal to, as `pleiades seal-key` prints it, or @file holding it")
	promptPassphrase := fs.Bool("passphrase", false, "seal to a passphrase, typed at a prompt, instead of a key")
	stdinPassphrase := fs.Bool("passphrase-stdin", false, "seal to a passphrase read as one line on standard input")
	stdinSecret := fs.Bool("secret-stdin", false, "read the secret as one line on standard input instead of a prompt")
	purpose := fs.String("purpose", sealed.PurposePassword, "what the secret is for")
	out := fs.String("out", "", "the file to write, which must not exist")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: pleiades seal (--to <key> | --passphrase | --passphrase-stdin) [--secret-stdin] --out <file>")
	}
	ways := 0
	for _, chosen := range []bool{*to != "", *promptPassphrase, *stdinPassphrase} {
		if chosen {
			ways++
		}
	}
	if ways != 1 {
		return fmt.Errorf("choose one way to seal: --to <public key>, --passphrase or --passphrase-stdin")
	}
	if *stdinSecret && *stdinPassphrase {
		return fmt.Errorf("--secret-stdin and --passphrase-stdin cannot both read standard input")
	}
	if *out == "" {
		return fmt.Errorf("--out is required")
	}
	// Refused before anything is typed, so a mistake costs no prompt.
	if _, err := os.Lstat(*out); err == nil {
		return fmt.Errorf("%s already exists; seal to a new file", *out)
	}
	var pub sealed.PublicKey
	if *to != "" {
		text := *to
		if path, ok := strings.CutPrefix(text, "@"); ok {
			data, err := readBounded(path, 64<<10)
			if err != nil {
				return err
			}
			text = string(data)
		}
		var err error
		if pub, err = sealed.ParsePublicKey(text); err != nil {
			return err
		}
	}
	secret, err := readConfirmed(*stdinSecret, "secret: ")
	if err != nil {
		return err
	}
	var data []byte
	if *to != "" {
		data, err = sealed.SealToKey(pub, *purpose, []byte(secret))
	} else {
		var passphrase string
		if passphrase, err = readConfirmed(*stdinPassphrase, "passphrase: "); err != nil {
			return err
		}
		data, err = sealed.SealWithPassphrase(passphrase, *purpose, []byte(secret))
	}
	if err != nil {
		return err
	}
	if err := writeNewPrivateFile(*out, data); err != nil {
		return err
	}
	if *to != "" {
		fmt.Printf("sealed a %s to %s in %s\n", *purpose, pub.Fingerprint(), *out)
	} else {
		fmt.Printf("sealed a %s with a passphrase in %s\n", *purpose, *out)
	}
	return nil
}

// readConfirmed reads one secret from standard input, or from a prompt typed
// twice, since a typo in a value nobody sees is otherwise found only when
// whatever it unlocks refuses it.
func readConfirmed(fromStdin bool, promptText string) (string, error) {
	if fromStdin {
		return prompt.SecretFromStdin()
	}
	first, err := promptSecret(promptText)
	if err != nil {
		return "", err
	}
	second, err := promptSecret("again: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", fmt.Errorf("the two entries differ")
	}
	return first, nil
}

// writeNewPrivateFile writes data to a file that must not exist yet, readable
// by its owner alone.
func writeNewPrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- the path the person names with --out
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists; seal to a new file", path)
	}
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return werr
	}
	return nil
}
