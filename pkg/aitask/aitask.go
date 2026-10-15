// Package aitask is what the ai methods share between reading a task and
// making a call (pkg/aiprovider): the provider a task targets, the
// residency rule, a task's call parameters, prompt bundles and the policy
// their signatures are held to, and the usage record every call ends in.
// It is public so an external Collection building a method on a kind of
// its own holds its calls to the same rules.
package aitask

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The residencies a provider and a use are tagged with.
const (
	ResidencyLocal  = "local"
	ResidencyHosted = "hosted"
)

// Target is a provider a task calls: its device's name, its endpoint,
// residency and prices.
type Target struct {
	Name      string
	Provider  aiprovider.Provider
	Residency string
	Prices    aiprovider.Prices
}

// TargetFor resolves device, with its own secrets, into a Target. A kind
// that needs a key, with none stored, is refused here, before contact,
// naming the command that stores one.
func TargetFor(device inventory.InventoryItem, secrets map[string]string) (Target, error) {
	if device == nil {
		return Target{}, fmt.Errorf("this task has no provider device to call")
	}
	dev, ok := device.(capability.AIProviderCapable)
	if !ok {
		return Target{}, fmt.Errorf("%s is not a language model provider (an ai_provider device)", device.Name())
	}
	p, err := aiprovider.EndpointFor(device, device.Name(), secrets)
	if err != nil {
		return Target{}, fmt.Errorf("provider %s: %w", device.Name(), err)
	}
	if p.Kind.NeedsKey() && p.Endpoint.Key == "" {
		return Target{}, fmt.Errorf("a %s provider needs an API key, and %s has none stored (pleiades add-credential %s --token-stdin)", p.Kind.Name(), device.Name(), device.Name())
	}
	return Target{Name: device.Name(), Provider: p, Residency: dev.AIResidency(), Prices: aiprovider.PricesFor(device)}, nil
}

// CheckResidency refuses sending a use tagged tag to a provider of
// residency: a local use may reach a local provider only; a hosted use, or
// one with no tag, may reach either.
func CheckResidency(tag, provider, residency string) error {
	switch tag {
	case "", ResidencyHosted:
		return nil
	case ResidencyLocal:
		if residency != ResidencyLocal {
			return fmt.Errorf("the use is tagged %s, and provider %s is %s: what it is sent would leave hardware you run", ResidencyLocal, provider, residency)
		}
		return nil
	}
	return fmt.Errorf("residency %q is not %s or %s", tag, ResidencyLocal, ResidencyHosted)
}
