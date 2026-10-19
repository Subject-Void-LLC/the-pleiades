// The reference documentation of ai.provider.list and ai.provider.probe.
package provider

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func listDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Reports what a provider is and which models it serves: kind, endpoint, residency, prices and host guard.",
		Description: "Reads the ai_provider device's record (its kind, API base URL, residency, price table and host guard) and asks the provider for the models it serves, with the device's own key over its own TLS: a listing no provider counts as a call. A kind needing a key the device does not have is refused before contact. It only reads, so a check of it is the task itself.",
		Returns: []collection.ReturnField{
			{Name: "kind", Type: "string", Returned: "always", Description: "The provider's API: ollama, anthropic, google, openai, or a kind an external Collection registered."},
			{Name: "api_base", Type: "string", Returned: "always", Description: "The API's base URL."},
			{Name: "residency", Type: "string", Returned: "always", Description: "local or hosted, as the record says."},
			{Name: "models", Type: "list of string", Returned: "always", Description: "The models the provider serves, sorted."},
			{Name: "prices", Type: "dict", Returned: "always", Description: "The record's price table: model to input and output price per million tokens, and currency."},
			{Name: "host_guard", Type: "string", Returned: "always", Description: "The host guard the record declares (single_resident), or empty."},
			{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "What the record allows that a run must say: a key over plain http://, weakened TLS."},
		},
		Examples: []collection.Example{
			{Name: "List every provider", RunbookYAML: "- name: Providers\n  ai.provider.list: {}\n"},
		},
		SeeAlso: []string{"ai.provider.probe", "ai.model.list"},
	}
}

func probeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes one counted call of a few tokens to a provider's model and reports its latency and usage.",
		Description: "Asks the model for a one-word answer, at most 8 tokens at temperature 0 and with no retry, so a failure is the provider's answer as it stands: proof that the key, the endpoint, the TLS and the model all work, and what one call costs in time. The call is counted, its usage record returned and, with usage_sink, appended to the project. A check makes no call and reports the call it would make, with its estimate.",
		Params:      aitask.DocParams(aitask.ParamModel, aitask.ParamTimeout, aitask.ParamUsageSink),
		Returns: []collection.ReturnField{
			{Name: "latency_ms", Type: "int", Returned: "success", Description: "How long the call took, in milliseconds."},
			{Name: "answered_by", Type: "string", Returned: "success", Description: "The model the provider says answered."},
			{Name: "usage", Type: "dict", Returned: "success", Description: "The call's usage record, as ai.chat returns one."},
			{Name: "estimate", Type: "dict", Returned: "in a check", Description: "The call a run would make, and what it would cost."},
		},
		Examples: []collection.Example{
			{Name: "Prove a provider answers", RunbookYAML: "- name: Probe\n  ai.provider.probe:\n    model: claude-haiku-5-5\n"},
		},
		SeeAlso: []string{"ai.provider.list", "ai.chat"},
	}
}
