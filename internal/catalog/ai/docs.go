// The reference documentation of ai.chat and ai.route, which `pleiades
// doc` and the generated catalog read.
package ai

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// callReturns are what a successful call returns, which both methods share.
func callReturns() []collection.ReturnField {
	return []collection.ReturnField{
		{Name: "answer", Type: "string", Returned: "success", Description: "The model's answer, whole: every kind streams, and the task sees one answer."},
		{Name: "json", Type: "any", Returned: "when schema is set", Description: "The answer read as JSON, once it met the schema."},
		{Name: "stop_reason", Type: "string", Returned: "success", Description: "Why the model stopped, in the provider's own words."},
		{Name: "usage", Type: "dict", Returned: "success", Description: "The usage record: time, method, provider, kind, residency, model asked for and the model that answered, input and output tokens, duration, retries, the parameters ignored and dropped, the cost and currency when the provider's price table prices the model, the prompt's verdict, and the outcome. It never holds what was sent or answered."},
		{Name: "prompt", Type: "dict", Returned: "always", Description: "The prompt's verdict: status (verified, unsigned, not checked) and, for a bundle, its name, version, SHA-256 and signing key."},
		{Name: "ignored", Type: "list of string", Returned: "success", Description: "Parameters the provider's kind does not accept, which were not sent."},
		{Name: "dropped", Type: "list of string", Returned: "success", Description: "Parameters the provider rejected, removed and never sent to it for that model again in the run."},
		{Name: "estimate", Type: "dict", Returned: "in a check", Description: "What a check reports instead of a call: the prompt's tokens (four characters a token), max_output_tokens or output_unbounded, the parameters that would be ignored and dropped, and max_cost with its currency when the model is priced."},
		{Name: "warnings", Type: "list of string", Returned: "when there is one", Description: "What the provider's record allows that a run must say: a key sent over plain http://, weakened TLS."},
		{Name: "review", Type: "dict", Returned: "with review rules", Description: "The answer's review: not held, or approved with the key, the rules it matched, who decided and when; in a check, the rules and any decision already on record. A held answer is never returned: the task fails as held."},
		{Name: "reused", Type: "bool", Returned: "when a decided answer is reused", Description: "True when the answer was approved earlier for this very request and returned from its review with no call; usage is the original call's."},
	}
}

func chatDoc() collection.Doc {
	params := aitask.DocParams(aitask.ParamModel)
	params = append(params, aitask.DocParams(aitask.PromptParams...)...)
	params = append(params, aitask.DocParams(aitask.ParamSigning, aitask.ParamResidency, aitask.ParamReview)...)
	params = append(params, aitask.DocParams(aitask.OptionParams...)...)
	params = append(params, aitask.DocParams(aitask.ParamUsageSink)...)
	return collection.Doc{
		Summary: "Asks the target provider's model and returns its answer with a usage record: tokens, time, retries and cost.",
		Description: "Sends messages, or a prompt bundle with optional untrusted input, to the model named on the ai_provider device the task targets, through its kind (ollama, anthropic, google, or any server speaking OpenAI's chat completions API), with the device's own key over its own TLS and no redirect followed. " +
			"Before any call it refuses a use tagged local against a hosted provider, a kind needing a key the device does not have, and a bundle its signing policy does not admit; a parameter the kind does not accept is reported ignored and not sent, and one the provider rejects (a 400 naming it) is reported dropped, the call sent once more without it, and never sent again to that provider for that model in the run. " +
			"Only rate limits, overload, server errors and a provider never reached are retried, with exponential backoff and jitter and honoring Retry-After, and nothing after the answer began streaming, which could bill twice. With schema the answer is asked for through the kind's structured output and checked against the schema before the task succeeds. " +
			"Every call ends in a usage record, returned and, with usage_sink, appended to a file in the project; the messages and the answer are never in it. With review rules, an answer crossing a threshold is held for a person before any later task can read it: asked at the terminal when someone is there, otherwise kept in the project's ai/review with the task failing as held until it is decided with pleiades ai review, after which running again reuses the decision with no second call; with nobody to ask at all it fails closed. A signature says who approved a bundle's text and that it is unchanged; it is provenance and integrity, not a promise about what the model does with it. " +
			"A check makes no call: it validates the key, the residency, the parameters against the kind, the schema and the bundle's signature, and estimates the cost from the prompt's size and max_tokens. It reports changed, since a real run makes a counted call.",
		Params:  params,
		Returns: callReturns(),
		Examples: []collection.Example{
			{Name: "Ask a local model for a structured answer", RunbookYAML: "- name: Classify\n  ai.chat:\n    model: qwen3:14b\n    residency: local\n    messages:\n      - {role: user, content: \"Is 'disk full on /var' a bug or an outage? Answer as JSON.\"}\n    schema:\n      type: object\n      properties: {label: {type: string, enum: [bug, outage]}}\n      required: [label]\n    max_tokens: 200\n    usage_sink: spend.jsonl\n  register: verdict\n"},
			{Name: "Send a signed prompt with untrusted input", RunbookYAML: "- name: Triage\n  ai.chat:\n    model: claude-haiku-5-5\n    prompt_file: triage\n    signing: required\n    input: \"{{ vars.ticket_text }}\"\n"},
		},
		SeeAlso: []string{"ai.route", "ai.prompt.verify", "ai.provider.probe"},
	}
}

func routeDoc() collection.Doc {
	params := aitask.DocParams(aitask.ParamUse, aitask.ParamRoutes, aitask.ParamReview)
	params = append(params, aitask.DocParams(aitask.PromptParams...)...)
	params = append(params, aitask.DocParams(aitask.OptionParams...)...)
	params = append(params, aitask.DocParams(aitask.ParamUsageSink)...)
	returns := append(callReturns(),
		collection.ReturnField{Name: "provider", Type: "string", Returned: "success", Description: "The provider that answered."},
		collection.ReturnField{Name: "model", Type: "string", Returned: "success", Description: "The model it was asked for."},
		collection.ReturnField{Name: "use", Type: "string", Returned: "always", Description: "The use routed."},
		collection.ReturnField{Name: "attempts", Type: "list of dict", Returned: "success", Description: "Each provider tried, in order, with its model, outcome and, for a failure, why."},
	)
	return collection.Doc{
		Summary: "Asks a use's providers in fallback order and returns the first answer, refusing any provider the use's residency forbids.",
		Description: "Routes a call by use: the routing table names, for each use, its residency (local or hosted), its signing policy and its providers in fallback order, each an ai_provider device with the model to ask it for. " +
			"Before any call it resolves every provider in the use's order (each must be an onboarded ai_provider, with a key when its kind needs one) and refuses the route as a whole when one would take a local use's data to a hosted provider, which pleiades validate refuses already for a literal table. The use, not the task, decides the signing policy and the residency, so the task may set neither, nor model; a prompt bundle must be for the routed use. " +
			"It then calls the providers in order, as ai.chat calls one, and moves to the next only when a provider could not be reached or kept failing in a way only time mends (rate limits, overload, server errors) after its retries, before any answer began; a refused request, a refused key or an answer missing its schema fails the task instead, since the next provider would be asked the same wrong thing or hide a broken credential. Every attempt's usage record is appended to usage_sink. " +
			"It runs once, with no target device (no hosts:), and reaches providers through the project, so it runs on the Crawl tier; on the Walk tier target the provider and use ai.chat. A check makes no call: it resolves the route and estimates each provider's cost.",
		Params:  params,
		Returns: returns,
		Examples: []collection.Example{
			{Name: "Summarize locally, falling back to a second local server", RunbookYAML: "- name: Summarize\n  ai.route:\n    use: summarize\n    routes:\n      summarize:\n        residency: local\n        signing: optional\n        providers:\n          - {provider: workstation, model: \"qwen3:14b\"}\n          - {provider: lab-vllm, model: qwen3-14b}\n    messages:\n      - {role: user, content: \"Summarize the release notes in three lines.\"}\n    max_tokens: 300\n"},
		},
		SeeAlso: []string{"ai.chat", "ai.provider.list"},
	}
}
