// The language model provider probe: list the provider's models with its
// own key, which costs nothing and proves the API, the key and the TLS.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

type aiProber struct{}

func init() { Register(ai.TypeProvider, aiProber{}) }

func (aiProber) Protocol() string { return "https" }

// Probe lists the provider's models through its kind (pkg/aiprovider),
// with the endpoint every call to it uses (aiprovider.EndpointFor: its own
// key, its own TLS, no redirect followed). A kind that needs a key is
// refused before contact when none is stored. A listing proves the API;
// a refused key, a server error or an answer the kind cannot read does
// not. No model is called, so onboarding is never billed.
func (aiProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	if _, ok := device.(capability.AIProviderCapable); !ok {
		return Probed{}, errors.New("the device is not a language model provider")
	}
	p, err := aiprovider.EndpointFor(device, device.Name(), secrets)
	if err != nil {
		return Probed{}, err
	}
	if p.Kind.NeedsKey() && p.Endpoint.Key == "" {
		return Probed{}, fmt.Errorf("a %s provider needs an API key, and %s has none stored (pleiades add-credential %s --token-stdin)", p.Kind.Name(), device.Name(), device.Name())
	}
	models, err := p.Kind.Models(ctx, p.Endpoint)
	if err != nil {
		return Probed{}, fmt.Errorf("listing models: %w", err)
	}
	slices.Sort(models)
	models = slices.Compact(models)
	return Probed{
		Capabilities: []capability.Name{capability.NameAIProvider},
		Facts: map[string]any{
			"kind":                 factText(p.Kind.Name()),
			"model_count":          strconv.Itoa(len(models)),
			ai.FactModels:          factList(models),
			ai.FactModelsTruncated: len(models) > maxFactList,
		},
		Warnings: p.Warnings,
	}, nil
}
