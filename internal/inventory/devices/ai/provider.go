// Package ai holds the ai_provider device type: a language model provider,
// reached through its API (pkg/aiprovider).
package ai

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TypeProvider is the ai_provider device type.
const TypeProvider = "ai_provider"

// The properties a provider's record holds.
const (
	// KindProperty is the provider's API: ollama, anthropic, google,
	// openai, or a kind an external Collection registers.
	KindProperty = "ai_kind"
	// APIBaseProperty is the API's base URL.
	APIBaseProperty = "api_base"
	// ResidencyProperty is local (hardware the operator runs) or hosted.
	// It is required: whether data leaves the operator's hardware is not a
	// default anyone should inherit.
	ResidencyProperty = "residency"
	// PricesProperty is the price table, "model=input/output" per model,
	// comma separated, each a price per million tokens.
	PricesProperty = "ai_prices"
	// CurrencyProperty is the price table's currency, required with it.
	CurrencyProperty = "ai_price_currency"
	// HostGuardProperty is an Ollama host's guard: single_resident lets
	// ai.model.load, unload and pull change the host, keeping one model
	// in memory at a time.
	HostGuardProperty = "ai_host_guard"
)

// The residencies.
const (
	ResidencyLocal  = "local"
	ResidencyHosted = "hosted"
)

// GuardSingleResident is the one host guard.
const GuardSingleResident = "single_resident"

// kindName is the form of a kind's name.
var kindName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Provider is ai_provider. AIProviderCapable is discovered: onboarding
// lists the provider's models with its credential, and only an answer
// grants it.
type Provider struct {
	*record.Base
	kind           string
	base           *url.URL
	residency      string
	prices         map[string]capability.AIPrice
	guard          string
	allowPlaintext bool
	tls            devicetls.Settings
	stale          string
}

// NewProvider builds an ai_provider from rec, refusing a record that does
// not say what the provider is, where it is and whether data sent to it
// stays local; a base URL that is not one (httpapi.ValidateBaseURL); a
// hosted provider over plain http://; TLS settings on a plain http:// API;
// a price table that does not parse; and a host guard on a provider that
// is not an Ollama host. Whether a key may go to an http:// base URL is
// decided where the key is known, by aiprovider.EndpointFor, since a local
// model server often takes none.
func NewProvider(rec record.Record) (inventory.InventoryItem, error) {
	fail := func(err error) (inventory.InventoryItem, error) {
		return nil, fmt.Errorf("%s %s: %w", TypeProvider, rec.Name, err)
	}
	props := inventory.NewProperties(rec.Properties)
	kind, _ := props.String(KindProperty)
	if !kindName.MatchString(kind) {
		return fail(fmt.Errorf("property %s must name the provider's API: ollama, anthropic, google or openai", KindProperty))
	}
	residency, _ := props.String(ResidencyProperty)
	if residency != ResidencyLocal && residency != ResidencyHosted {
		return fail(fmt.Errorf("property %s must be %s or %s: whether data sent to this provider leaves hardware you run", ResidencyProperty, ResidencyLocal, ResidencyHosted))
	}
	allowPlaintext, err := strictBool(rec.Properties, httpapi.AllowPlaintextCredentialsProperty)
	if err != nil {
		return fail(err)
	}
	raw, _ := props.String(APIBaseProperty)
	// Whether a key is stored is not the record's to say, so the record is
	// read as sending one exactly when it allows that over http://; the
	// flag on an https:// base, which allows nothing, is refused.
	auth := httpapi.AuthNone
	if allowPlaintext {
		auth = httpapi.AuthBearer
	}
	base, err := httpapi.ValidateBaseURL(raw, auth, allowPlaintext)
	if err != nil {
		return fail(fmt.Errorf("property %s: %w", APIBaseProperty, err))
	}
	if base.Scheme != "https" && residency == ResidencyHosted {
		return fail(fmt.Errorf("property %s: a hosted provider is reached over https only, since what is sent to it leaves hardware you run", APIBaseProperty))
	}
	if base.Scheme != "https" {
		for _, key := range devicetls.Properties() {
			if _, present := rec.Properties[key]; present {
				return fail(fmt.Errorf("property %s applies to a TLS connection, and this provider's has none", key))
			}
		}
	}
	settings, err := devicetls.Parse(props)
	if err != nil {
		return fail(err)
	}
	prices, err := parsePrices(props)
	if err != nil {
		return fail(err)
	}
	guard, present := rec.Properties[HostGuardProperty]
	guardName, _ := guard.(string)
	switch {
	case !present:
	case guardName != GuardSingleResident:
		return fail(fmt.Errorf("property %s must be %s", HostGuardProperty, GuardSingleResident))
	case kind != "ollama":
		return fail(fmt.Errorf("property %s applies to an Ollama host only", HostGuardProperty))
	}
	caps, stale, err := record.Declared(TypeProvider, rec, []capability.Name{capability.NameNetworkAddressable})
	if err != nil {
		return nil, err
	}
	return &Provider{
		Base: record.NewBase(rec, caps), kind: kind, base: base, residency: residency, prices: prices,
		guard: guardName, allowPlaintext: allowPlaintext, tls: settings, stale: stale,
	}, nil
}

// parsePrices reads the price table: "model=input/output" entries, comma
// separated, each price a non-negative decimal per million tokens, with
// the currency in its own property.
func parsePrices(props inventory.Properties) (map[string]capability.AIPrice, error) {
	raw, present := props.Raw()[PricesProperty]
	currency, _ := props.String(CurrencyProperty)
	if !present {
		if currency != "" {
			return nil, fmt.Errorf("property %s names the currency of %s, which the record does not set", CurrencyProperty, PricesProperty)
		}
		return nil, nil
	}
	table, ok := raw.(string)
	if !ok || strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("property %s must be \"model=input/output\" entries, comma separated", PricesProperty)
	}
	if currency == "" {
		return nil, fmt.Errorf("property %s needs %s: the currency its prices are in", PricesProperty, CurrencyProperty)
	}
	out := map[string]capability.AIPrice{}
	for _, entry := range strings.Split(table, ",") {
		model, pair, ok := strings.Cut(strings.TrimSpace(entry), "=")
		in, outPrice, ok2 := strings.Cut(pair, "/")
		model = strings.TrimSpace(model)
		if !ok || !ok2 || model == "" {
			return nil, fmt.Errorf("property %s: %q is not model=input/output", PricesProperty, strings.TrimSpace(entry))
		}
		if _, dup := out[model]; dup {
			return nil, fmt.Errorf("property %s prices %s twice", PricesProperty, model)
		}
		inPrice, err1 := price(in)
		outValue, err2 := price(outPrice)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("property %s: %s's prices must be non-negative decimals", PricesProperty, model)
		}
		out[model] = capability.AIPrice{Input: inPrice, Output: outValue, Currency: currency}
	}
	return out, nil
}

// price reads one non-negative decimal price.
func price(s string) (float64, error) {
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f != f || f > 1e9 || strings.ContainsAny(s, "eEnN") {
		return 0, fmt.Errorf("not a price")
	}
	return f, nil
}

// strictBool reads key as a boolean, false when absent; anything else is
// refused, so a setting that weakens nothing by accident reads as true.
func strictBool(props map[string]inventory.PropertyValue, key string) (bool, error) {
	v, present := props[key]
	if !present {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("property %s must be true or false", key)
	}
	return b, nil
}

// HasCapability checks the declared set AND the structural assertion.
func (p *Provider) HasCapability(name capability.Name) bool {
	return p.Declares(name) && capability.Implements(p, name)
}

// AIKind implements capability.AIProviderCapable.
func (p *Provider) AIKind() string { return p.kind }

// AIBaseURL implements capability.AIProviderCapable.
func (p *Provider) AIBaseURL() string { return p.base.String() }

// AIResidency implements capability.AIProviderCapable.
func (p *Provider) AIResidency() string { return p.residency }

// AIPrices implements capability.AIProviderCapable; a copy, so a caller
// cannot change the record's table.
func (p *Provider) AIPrices() map[string]capability.AIPrice {
	if p.prices == nil {
		return nil
	}
	out := make(map[string]capability.AIPrice, len(p.prices))
	for k, v := range p.prices {
		out[k] = v
	}
	return out
}

// PricedModels is the models the table prices, sorted.
func (p *Provider) PricedModels() []string {
	out := make([]string, 0, len(p.prices))
	for k := range p.prices {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AIHostGuard implements capability.AIProviderCapable.
func (p *Provider) AIHostGuard() string { return p.guard }

// AIAllowPlaintextCredentials implements capability.AIProviderCapable.
func (p *Provider) AIAllowPlaintextCredentials() bool { return p.allowPlaintext }

// TLSSettings returns the provider's validated TLS settings.
func (p *Provider) TLSSettings() devicetls.Settings { return p.tls }

// IPAddress returns the base URL's host.
func (p *Provider) IPAddress() string { return p.base.Hostname() }

// SharedAcrossRuns implements inventory.Shared: a provider serves many
// callers at once, so no Pleiades lease serializes runs that call it. A
// model host's changes (ai.model.load) are serialized by the host guard's
// own rule, one model resident, not by a lease.
func (p *Provider) SharedAcrossRuns() bool { return true }

// DiscoveryBinding implements inventory.DiscoveryBinder.
func (p *Provider) DiscoveryBinding() string {
	return record.DiscoveryBinding(TypeProvider, p.Properties())
}

// StaleDiscovery implements inventory.StaleDiscoverer.
func (p *Provider) StaleDiscovery() string { return p.stale }

// The discovery facts onboarding writes.
const (
	FactModels          = "models"
	FactModelsTruncated = "models_truncated"
)

// DiscoveredModels returns the models onboarding found the provider
// serves; complete is false before onboarding, when the discovery no
// longer holds for the record, or when the list was cut.
func (p *Provider) DiscoveredModels() (models []string, complete bool) {
	if p.stale != "" {
		return nil, false
	}
	d, ok, err := inventory.DiscoveryFrom(p.Properties())
	if err != nil || !ok {
		return nil, false
	}
	if truncated, _ := d.Facts[FactModelsTruncated].(bool); truncated {
		return nil, false
	}
	return record.DiscoveredList(p.Properties(), FactModels), true
}

// boundProperties are what a discovery holds for: the API's kind, address
// and authority. A changed price table or residency is the operator's to
// say and asks for no new onboarding.
var boundProperties = append([]string{KindProperty, APIBaseProperty, httpapi.AllowPlaintextCredentialsProperty}, devicetls.Properties()...)

func init() {
	record.RegisterType(TypeProvider, NewProvider)
	record.RegisterOnboardedType(TypeProvider)
	// A template's machine credential is never an API key.
	record.RegisterOwnCredentialType(TypeProvider)
	record.RegisterDiscoveryRules(TypeProvider, record.DiscoveryRules{
		Grants: []capability.Name{capability.NameAIProvider},
		Bound:  boundProperties,
	})
	record.RegisterDispatchProperties(TypeProvider, append([]string{
		KindProperty, APIBaseProperty, ResidencyProperty, PricesProperty, CurrencyProperty, HostGuardProperty,
		httpapi.AllowPlaintextCredentialsProperty,
	}, devicetls.Properties()...)...)
}
