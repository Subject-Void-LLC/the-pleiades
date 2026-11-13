// Package main: list-credentials, every device credential in the project's
// vault by name, account and kind, and which are unbound, never a value.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// runListCredentials lists the device credentials in the project's vault:
// each name, the account it logs in as, the kinds of secret it holds, and
// "unbound" for one stored for a name no inventory device had
// (add-credential's warning or --no-device). It needs no master key and
// decrypts nothing.
func runListCredentials(args []string) error {
	fs := flag.NewFlagSet("list-credentials", flag.ContinueOnError)
	addVaultFlag(fs)
	dir := fs.String("dir", ".", "project directory")
	asJSON := fs.Bool("json", false, "print one JSON document instead of text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: pleiades list-credentials [--dir DIR] [--json]")
	}
	listed, err := credential.ListCredentials(vaultDir(*dir))
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(os.Stdout, listed)
	}
	if len(listed) == 0 {
		fmt.Println("no device credentials stored in this project")
		return nil
	}
	for _, c := range listed {
		line := termsafe.EscapeLine(c.Name)
		if c.Username != "" {
			line += "  " + termsafe.EscapeLine(c.Username)
		}
		line += "  " + strings.Join(c.Kinds, ", ")
		if c.Unbound {
			line += "  unbound"
		}
		fmt.Println(line)
	}
	return nil
}
