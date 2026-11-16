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
// each name, the account it logs in as, its SNMPv3 user, the kinds of secret
// it holds, "unbound" for one stored for a name no inventory device had
// (add-credential's warning or --no-device), and "short community" for an
// SNMP community stored allowed short. It needs no master key and decrypts
// nothing.
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
		if c.SNMPUser != "" {
			line += "  snmp user " + termsafe.EscapeLine(c.SNMPUser)
		}
		line += "  " + strings.Join(c.Kinds, ", ")
		if c.Unbound {
			line += "  unbound"
		}
		if c.ShortCommunity {
			// Every run masks such a community by shape only, and says so.
			line += "  short community"
		}
		fmt.Println(line)
	}
	return nil
}
