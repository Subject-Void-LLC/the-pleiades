// The Cloudflare account probe: read the account with its own token, which
// proves the token, the account and the TLS, and changes nothing.
package onboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cloudflare"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

type cloudflareProber struct{}

func init() { Register(cloudflare.TypeAccount, cloudflareProber{}) }

func (cloudflareProber) Protocol() string { return "https" }

// Probe reads the account (GET /accounts/{id}) through the client every
// edge method uses (cfapi.ClientFor). A refused token, an account the
// token cannot read, or an answer that is not Cloudflare's proves nothing.
func (cloudflareProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	dev, ok := device.(capability.CloudflareAPICapable)
	if !ok {
		return Probed{}, errors.New("the device is not a Cloudflare account")
	}
	c, warnings, err := cfapi.ClientFor(device, device.Name(), secrets)
	if err != nil {
		return Probed{}, err
	}
	acct, err := c.Account(ctx, dev.CloudflareAccountID())
	if err != nil {
		return Probed{}, err
	}
	// An answer for no account, or another one, proves nothing about this.
	if acct.ID != dev.CloudflareAccountID() {
		return Probed{}, fmt.Errorf("the API answered for account %s, not %s", factText(acct.ID), dev.CloudflareAccountID())
	}
	return Probed{
		Capabilities: []capability.Name{capability.NameCloudflareAPI},
		Facts:        map[string]any{cloudflare.FactAccountName: factText(acct.Name)},
		Warnings:     warnings,
	}, nil
}
