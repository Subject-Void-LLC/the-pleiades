// The named secrets commands: add-secret, list-secrets and remove-secret,
// over the project's credential store (internal/credential's secret_store.go).
// A runbook refers to a stored field as {{ secret.<name>.<field> }} in a
// parameter whose method resolves it (pkg/secretref).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// addSecretUsage is add-secret's usage line.
const addSecretUsage = "usage: pleiades add-secret <name> --field <field> (--value-stdin | --value-file <sealed file> [--passphrase | --passphrase-stdin] | --generate <length>) [--replace] [--dir DIR]"

// runAddSecret stores one field of a named secret, encrypted at rest. The
// value is read from standard input, from a file `pleiades seal` wrote, or
// generated, and never taken as a flag's value, which shell history and the
// process list would show. A field already stored is replaced only with
// --replace, since every device configured with the old value would then
// disagree with the runbook.
func runAddSecret(args []string) error {
	name, rest, err := splitPositional(args, map[string]bool{"value-stdin": true, "passphrase": true, "passphrase-stdin": true, "replace": true})
	if err != nil {
		return fmt.Errorf("%s: %w", addSecretUsage, err)
	}
	fs := flag.NewFlagSet("add-secret", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	field := fs.String("field", "", "the field of the secret to store")
	fromStdin := fs.Bool("value-stdin", false, "read the value as one line on standard input")
	valueFile := fs.String("value-file", "", "a file `pleiades seal` wrote holding the value")
	promptPassphrase := fs.Bool("passphrase", false, "prompt for the passphrase of a passphrase-sealed --value-file")
	stdinPassphrase := fs.Bool("passphrase-stdin", false, "read the passphrase of a passphrase-sealed --value-file as one line on standard input")
	generate := fs.Int("generate", 0, "generate a random value of this many letters and digits instead of reading one")
	replace := fs.Bool("replace", false, "replace a value already stored for the field")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(addSecretUsage)
	}
	if !credential.ValidSecretName(name) {
		return fmt.Errorf("secret name %q must be a lowercase identifier (a letter, then letters, digits or _), since a runbook names it as {{ secret.<name>.<field> }}", name)
	}
	if !credential.ValidSecretName(*field) {
		return fmt.Errorf("--field %q must be a lowercase identifier (a letter, then letters, digits or _)", *field)
	}
	ways := 0
	for _, given := range []bool{*fromStdin, *valueFile != "", *generate != 0} {
		if given {
			ways++
		}
	}
	if ways != 1 {
		return fmt.Errorf("give exactly one of --value-stdin, --value-file and --generate: %s", addSecretUsage)
	}
	if (*promptPassphrase || *stdinPassphrase) && *valueFile == "" {
		return errors.New("--passphrase and --passphrase-stdin only apply to a passphrase-sealed --value-file")
	}
	if *promptPassphrase && *stdinPassphrase {
		return errors.New("--passphrase and --passphrase-stdin are mutually exclusive: a passphrase is read one way")
	}

	if !*replace {
		existing, err := credential.ListSecrets(*dir)
		if err != nil {
			return err
		}
		for _, s := range existing {
			for _, f := range s.Fields {
				if s.Name == name && f == *field {
					return fmt.Errorf("secret %s already has field %s; --replace overwrites it, and every device set with the old value then disagrees with the runbook", name, *field)
				}
			}
		}
	}

	var value string
	switch {
	case *generate != 0:
		if value, err = credential.GenerateSecret(*generate); err != nil {
			return err
		}
	case *fromStdin:
		if value, err = prompt.SecretFromStdin(); err != nil {
			return err
		}
	default:
		readPassphrase := func(text string) (string, error) {
			if *stdinPassphrase {
				return prompt.SecretFromStdin()
			}
			return promptSecret(text)
		}
		if value, err = openPasswordFile(*dir, *valueFile, *promptPassphrase || *stdinPassphrase, readPassphrase); err != nil {
			return err
		}
	}
	// The value lands on a device's command line, so it is held to the rule
	// a reference's resolution holds it to now, rather than at the run.
	if err := secretref.CheckValue(value); err != nil {
		return fmt.Errorf("secret %s.%s: %w", name, *field, err)
	}
	key, err := credential.ResolveMasterKey(*dir)
	if err != nil {
		return fmt.Errorf("failed to resolve master key: %w", err)
	}
	if err := credential.SaveSecretField(*dir, key, name, *field, value); err != nil {
		return err
	}
	how := "stored"
	if *generate != 0 {
		how = fmt.Sprintf("generated (%d characters) and stored", *generate)
	}
	fmt.Printf("%s secret %s field %s; a runbook names it as {{ secret.%s.%s }}\n", how, name, *field, name, *field)
	return nil
}

// runListSecrets lists every stored secret's name and field names, never a
// value.
func runListSecrets(args []string) error {
	fs := flag.NewFlagSet("list-secrets", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: pleiades list-secrets [--dir DIR] [--json]")
	}
	secrets, err := credential.ListSecrets(*dir)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(os.Stdout, secrets)
	}
	if len(secrets) == 0 {
		fmt.Println("no named secrets stored in this project")
		return nil
	}
	for _, s := range secrets {
		fmt.Printf("%s  %s\n", termsafe.EscapeLine(s.Name), termsafe.EscapeLine(strings.Join(s.Fields, ", ")))
	}
	return nil
}

// runRemoveSecret removes one field of a named secret, or the whole secret.
func runRemoveSecret(args []string) error {
	name, rest, err := splitPositional(args, nil)
	if err != nil || name == "" {
		return errors.New("usage: pleiades remove-secret <name> [--field <field>] [--dir DIR]")
	}
	fs := flag.NewFlagSet("remove-secret", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	field := fs.String("field", "", "remove only this field; the whole secret when empty")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	removed, err := credential.RemoveSecret(*dir, name, *field)
	if err != nil {
		return err
	}
	switch {
	case !removed && *field != "":
		return fmt.Errorf("no field %s in a secret named %s", termsafe.EscapeLine(*field), termsafe.EscapeLine(name))
	case !removed:
		return fmt.Errorf("no secret named %s", termsafe.EscapeLine(name))
	case *field != "":
		fmt.Printf("removed field %s of secret %s\n", termsafe.EscapeLine(*field), termsafe.EscapeLine(name))
	default:
		fmt.Printf("removed secret %s\n", termsafe.EscapeLine(name))
	}
	return nil
}
