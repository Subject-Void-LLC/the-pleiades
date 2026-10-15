// The capability of a language model provider: an API this platform calls
// models through (pkg/aiprovider).
package capability

// NameAIProvider names AIProviderCapable.
const NameAIProvider Name = "AIProviderCapable"

// AIPrice is a model's price per million tokens, in and out, in one
// currency.
type AIPrice struct {
	Input, Output float64
	Currency      string
}

// AIProviderCapable is satisfied by a device that is a language model
// provider: an API of a kind pkg/aiprovider knows, at one base URL, with
// a residency that says whether data sent to it stays on hardware the
// operator runs.
type AIProviderCapable interface {
	// AIKind is the provider's API: ollama, anthropic, google or openai.
	AIKind() string
	// AIBaseURL is the API's base URL.
	AIBaseURL() string
	// AIResidency is local (hardware the operator runs) or hosted.
	AIResidency() string
	// AIPrices is the operator's price table, by model; nil when the
	// record gives none.
	AIPrices() map[string]AIPrice
	// AIHostGuard is the guard a model host declares (single_resident),
	// or empty for none.
	AIHostGuard() string
	// AIAllowPlaintextCredentials reports whether the record accepts
	// sending its key to an http:// base URL.
	AIAllowPlaintextCredentials() bool
}

func init() {
	Register(Descriptor{
		Name:   NameAIProvider,
		Assert: func(item any) bool { _, ok := item.(AIProviderCapable); return ok },
	})
}
