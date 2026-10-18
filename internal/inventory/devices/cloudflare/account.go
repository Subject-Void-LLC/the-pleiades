// Package cloudflare holds the cloudflare_account device type: one
// Cloudflare account, reached through Cloudflare's API with its own token
// (pkg/cfapi), which the edge methods read.
package cloudflare

import (
	"fmt"
	"net/url"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cfapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TypeAccount is the cloudflare_account device type.
const TypeAccount = "cloudflare_account"

// The properties an account's record holds.
const (
	// AccountIDProperty is the account's id, 32 hex digits.
	AccountIDProperty = "account_id"
	// APIBaseProperty is the API's base URL, https; Cloudflare's own when
	// absent, so a proxy or a test server can stand in.
	APIBaseProperty = "api_base"
)

// The discovery fact onboarding writes.
const FactAccountName = "account_name"

// Account is cloudflare_account. CloudflareAPICapable is discovered:
// onboarding reads the account with its token, and only an answer grants
// it.
type Account struct {
	*record.Base
	id    string
	base  *url.URL
	tls   devicetls.Settings
	stale string
}

// NewAccount builds a cloudflare_account from rec, refusing an account id
// that is not one, an API base that is not https or carries anything a
// client could mistake for a credential, and TLS settings an API reached
// over current TLS does not take: the weak-TLS opt-ins, and the client
// certificate switch (the account authenticates with its token).
func NewAccount(rec record.Record) (inventory.InventoryItem, error) {
	fail := func(err error) (inventory.InventoryItem, error) {
		return nil, fmt.Errorf("%s %s: %w", TypeAccount, rec.Name, err)
	}
	props := inventory.NewProperties(rec.Properties)
	id, _ := props.String(AccountIDProperty)
	if !cfapi.ValidAccountID(id) {
		return fail(fmt.Errorf("property %s must be the account's id, 32 hex digits", AccountIDProperty))
	}
	raw, present := props.String(APIBaseProperty)
	if !present || raw == "" {
		raw = cfapi.DefaultBase
	}
	base, err := httpapi.ValidateBaseURL(raw, httpapi.AuthBearer, false)
	if err != nil {
		return fail(fmt.Errorf("property %s: %w", APIBaseProperty, err))
	}
	settings, err := devicetls.Parse(props)
	if err != nil {
		return fail(err)
	}
	if settings.Deprecated() || settings.AllowLegacyCiphers || settings.ClientCertificate {
		return fail(fmt.Errorf("the weak-TLS settings and %s do not apply: Cloudflare's API speaks current TLS, and the account authenticates with its token", devicetls.ClientCertificateProperty))
	}
	caps, stale, err := record.Declared(TypeAccount, rec, []capability.Name{capability.NameNetworkAddressable})
	if err != nil {
		return nil, err
	}
	return &Account{Base: record.NewBase(rec, caps), id: id, base: base, tls: settings, stale: stale}, nil
}

// HasCapability checks the declared set AND the structural assertion.
func (a *Account) HasCapability(name capability.Name) bool {
	return a.Declares(name) && capability.Implements(a, name)
}

// CloudflareAccountID implements capability.CloudflareAPICapable.
func (a *Account) CloudflareAccountID() string { return a.id }

// CloudflareAPIBase implements capability.CloudflareAPICapable.
func (a *Account) CloudflareAPIBase() string { return a.base.String() }

// TLSSettings returns the account's validated TLS settings.
func (a *Account) TLSSettings() devicetls.Settings { return a.tls }

// IPAddress returns the API's host.
func (a *Account) IPAddress() string { return a.base.Hostname() }

// SharedAcrossRuns implements inventory.Shared: the edge methods only
// read, so no Pleiades lease serializes runs reading one account.
func (a *Account) SharedAcrossRuns() bool { return true }

// DiscoveryBinding implements inventory.DiscoveryBinder.
func (a *Account) DiscoveryBinding() string {
	return record.DiscoveryBinding(TypeAccount, a.Properties())
}

// StaleDiscovery implements inventory.StaleDiscoverer.
func (a *Account) StaleDiscovery() string { return a.stale }

func init() {
	record.RegisterType(TypeAccount, NewAccount)
	record.RegisterOnboardedType(TypeAccount)
	// A template's machine credential is never an API token.
	record.RegisterOwnCredentialType(TypeAccount)
	record.RegisterDiscoveryRules(TypeAccount, record.DiscoveryRules{
		Grants: []capability.Name{capability.NameCloudflareAPI},
		// What onboarding proved holds for this account, this API and
		// this authority; changing any asks for onboarding again before
		// the token goes anywhere new.
		Bound: append([]string{AccountIDProperty, APIBaseProperty}, devicetls.Properties()...),
	})
	record.RegisterDispatchProperties(TypeAccount, append([]string{AccountIDProperty, APIBaseProperty}, devicetls.Properties()...)...)
}
