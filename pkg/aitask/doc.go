// Package aitask: the documentation of the parameters a call takes, which
// every method making one shares.
package aitask

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// paramDocs documents each call parameter once.
var paramDocs = map[string]collection.Param{
	ParamModel:       {Name: ParamModel, Type: "string", Required: true, Description: "The model to ask for, as the provider names it."},
	ParamMessages:    {Name: ParamMessages, Type: "list of dict", Description: "The conversation: a list of {role, content}, role system, user or assistant, with at least one user message. Give exactly one: messages, prompt or prompt_file."},
	ParamPrompt:      {Name: ParamPrompt, Type: "dict", Description: "A prompt bundle given inline: {kind: prompt, name, version, use, text, variants, parameters, expires}, exactly the document its signature covers. Give exactly one: messages, prompt or prompt_file."},
	ParamPromptFile:  {Name: ParamPromptFile, Type: "string", Description: "A prompt bundle in the project by name: ai/prompts/<name>.json, with its signature, if any, in <name>.json.sig beside it. Crawl tier only."},
	ParamSignature:   {Name: ParamSignature, Type: "dict", Description: "An inline prompt's detached signature, {key, sig}, as pleiades ai prompt sign writes it."},
	ParamInput:       {Name: ParamInput, Type: "string", Description: "Untrusted text for a prompt bundle (a ticket, a file, an earlier answer), placed after the bundle's text between fixed markers as one JSON string, so nothing in it can close the bundle's instructions. The model still reads it: a signature vouches for the bundle, not for what the model does with its input."},
	ParamSigning:     {Name: ParamSigning, Type: "string", Default: "optional", Choices: []string{"off", "optional", "required"}, Description: "The use's signing policy. off checks nothing; optional checks a signature when there is one and records an unsigned bundle as unsigned, never as verified; required refuses an unsigned, invalid, expired, unknown-key, wrong-role or rolled-back bundle before any call."},
	ParamResidency:   {Name: ParamResidency, Type: "string", Choices: []string{ResidencyLocal, ResidencyHosted}, Description: "The use's residency. local refuses a provider whose record says hosted, before contact and at validate; hosted admits either."},
	ParamTemperature: {Name: ParamTemperature, Type: "float", Description: "Sampling temperature, 0 to 2. A kind that does not accept a parameter reports it ignored and does not send it."},
	ParamTopP:        {Name: ParamTopP, Type: "float", Description: "Nucleus sampling, 0 to 1."},
	ParamMaxTokens:   {Name: ParamMaxTokens, Type: "int", Description: "The most tokens the answer may take, 1 to 1000000. A check prices the answer at this many; without it the answer's cost is unbounded."},
	ParamStop:        {Name: ParamStop, Type: "list of string", Description: "A sequence, or a list of up to 8, that ends the answer."},
	ParamThinking:    {Name: ParamThinking, Type: "int", Description: "A token budget for a model that thinks before it answers (Ollama turns thinking on); a kind without thinking ignores it."},
	ParamSchema:      {Name: ParamSchema, Type: "dict", Description: "A JSON schema the answer must meet, asked for through the kind's own structured output and checked before the task succeeds. It keeps to type, properties, required, additionalProperties, items, enum, const, minimum, maximum, minLength, maxLength, minItems and maxItems; any other keyword is refused rather than half-checked."},
	ParamTimeout:     {Name: ParamTimeout, Type: "string", Default: "5m", Description: "How long one call may take, retries included, from 1s to 1h."},
	ParamRetries:     {Name: ParamRetries, Type: "int", Default: "3", Description: "How many times a retryable failure (a rate limit, an overloaded or failing server, a provider never reached) is tried again, with exponential backoff and jitter and honoring Retry-After; 0 to 10. Nothing else is retried, and nothing after the answer began."},
	ParamUsageSink:   {Name: ParamUsageSink, Type: "string", Description: "A file name under the project's ai/usage directory (spend.jsonl) each call's usage record is appended to as one JSON line, proved writable before any call. Crawl tier only."},
	ParamReview:      {Name: ParamReview, Type: "list of dict", Description: "Review rules (human in the loop): thresholds on the answer, any of which holds it for a person's decision before a later task can read it. Each names a single threshold: cost_over (a number in the price table's currency; an unpriced call holds), output_tokens_over, answer_matches (RE2), field (a dotted path in a JSON answer, with below, above, equals, in or not_in; a missing field holds), stop_reason (a list), fallback (ai.route), unsigned and always. A person at the terminal is asked; with nobody there the answer is written to the project's ai/review and the task fails as held until `pleiades ai review approve` or `deny`, and running again reuses the decision with no second call. With nobody to ask at all (the Walk tier), a held answer fails closed."},
	ParamUse:         {Name: ParamUse, Type: "string", Required: true, Description: "The use to route: a key of routes."},
	ParamRoutes:      {Name: ParamRoutes, Type: "dict", Required: true, Description: "The routing table: each use's residency (local or hosted, required), signing policy, review rules (as review takes them; a task's add to the use's), and providers in fallback order, as {provider: <ai_provider device>, model}. A literal table is checked against the inventory at validate."},
}

// DocParams documents the named call parameters, in the order named.
func DocParams(names ...string) []collection.Param {
	out := make([]collection.Param, 0, len(names))
	for _, n := range names {
		p, ok := paramDocs[n]
		if !ok {
			panic(fmt.Sprintf("aitask: %s is not a documented call parameter", n))
		}
		if len(p.Choices) > 0 {
			p.Choices = append([]string(nil), p.Choices...)
		}
		out = append(out, p)
	}
	return out
}

// PromptParams are the parameters that say what a call sends.
var PromptParams = []string{ParamMessages, ParamPrompt, ParamPromptFile, ParamSignature, ParamInput}

// OptionParams are a call's generation settings and how hard it tries.
var OptionParams = []string{ParamTemperature, ParamTopP, ParamMaxTokens, ParamStop, ParamThinking, ParamSchema, ParamTimeout, ParamRetries}
