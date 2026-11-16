// Command pleiades's `confirm-tls-pin` subcommand: a person's confirmation
// that the pin onboarding recorded on first use (onboard --tls-first-use) is
// the device's own certificate, which moves it into tls_fingerprint. It
// parses flags and delegates; the move is record.Base.ConfirmFirstUsePin.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// confirmingPin is the one writer of a confirmation an inventory item has
// (record.Base.ConfirmFirstUsePin).
type confirmingPin interface {
	ConfirmFirstUsePin(pin, into string) error
}

// confirmTLSPinUsage is the command's usage line.
const confirmTLSPinUsage = "usage: pleiades confirm-tls-pin <device> <pin>"

// runConfirmTLSPin confirms the named device's first-use pin. The person
// gives the pin they checked with the device's owner, and it must be the one
// recorded: a confirmation is a comparison someone made, never a yes to
// whatever onboarding took. The device must be trusting that pin now, so a
// first-use pin the record's own tls_fingerprint or tls_ca_pem overrides is
// refused rather than added beside what a person set.
func runConfirmTLSPin(args []string) error {
	fs := flag.NewFlagSet("confirm-tls-pin", flag.ContinueOnError)
	addInventoryFlag(fs)
	dir := fs.String("dir", ".", "project directory")
	positional, err := parseCheckedArgs(fs, args, func(p []string) error {
		if len(p) != 2 {
			return errors.New(confirmTLSPinUsage)
		}
		return nil
	})
	if err != nil {
		return err
	}
	name := positional[0]
	pin, err := devicetls.NormalizePin(positional[1])
	if err != nil {
		return fmt.Errorf("%s: %w", confirmTLSPinUsage, err)
	}

	ctx := context.Background()
	repo := inv.NewFileRepository(inventoryFile(*dir), inv.NewItemFactory())
	item, err := repo.GetByName(ctx, name)
	if err != nil {
		return err
	}
	settings := devicetls.For(item)
	recorded := settings.FirstUsePin()
	switch {
	case recorded == "" && settings.UnusedFirstUsePin() != "":
		return fmt.Errorf("host %q holds a pin recorded on first use (%s) that its own TLS settings override, so it is not what the host trusts: onboard it again to remove it",
			name, settings.UnusedFirstUsePin())
	case recorded == "":
		return fmt.Errorf("host %q holds no pin recorded on first use: pleiades onboard %s --tls-first-use records one", name, name)
	case pin != recorded:
		return fmt.Errorf("host %q's pin recorded on first use is %s, not %s: compare the recorded one with the device's owner before confirming it", name, recorded, pin)
	}
	c, ok := item.(confirmingPin)
	if !ok {
		return fmt.Errorf("host %q cannot confirm a pin", name)
	}
	if err := c.ConfirmFirstUsePin(recorded, devicetls.FingerprintProperty); err != nil {
		return err
	}
	if err := repo.Save(ctx, item); err != nil {
		return fmt.Errorf("host %q not changed: %w", name, err)
	}
	fmt.Fprintf(os.Stdout, "confirmed %s's pin %s: it is now the host's %s, checked by a person\n", termsafe.EscapeLine(name), recorded, devicetls.FingerprintProperty)
	return nil
}
