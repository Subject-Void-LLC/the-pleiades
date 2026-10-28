// The reference documentation of ai.bench.run.
package bench

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func runDoc() collection.Doc {
	params := aitask.DocParams(aitask.ParamModel)
	params = append(params,
		collection.Param{Name: paramPrompts, Type: "list of dict", Required: true, Description: "The prompt set, up to 200: each {id, text or messages, expect}, where expect is exact (the answer, trimmed, equals it), contains, regex (RE2) or json_field ({path, equals}: the answer read as JSON holds equals at path, a dotted list of keys)."},
		collection.Param{Name: paramSaveAs, Type: "string", Description: "A file name under the project's ai/bench (run1.json) the results are saved to, with each answer cut to 4 KiB; never written over. Crawl tier only."},
	)
	params = append(params, aitask.DocParams(aitask.ParamResidency)...)
	params = append(params, aitask.DocParams(aitask.OptionParams...)...)
	params = append(params, aitask.DocParams(aitask.ParamUsageSink)...)
	return collection.Doc{
		Summary:     "Asks a provider's model a prompt set, scores each answer, and reports the score with every call's tokens, time and cost.",
		Description: "Asks the target provider's model each prompt of the set in turn, as ai.chat asks one, and scores each answer with the prompt's scorer; a call that fails counts as failed and the set goes on. It returns each prompt's result (passed, tokens, time, cost, and the error of a failed call), how many passed, the score, and the total cost when every call is priced. Every call's usage record is appended to usage_sink, and with save_as the results, with the answers, are saved under the project's ai/bench. A check makes no call: it validates the set and its scorers and estimates its tokens.",
		Params:      params,
		Returns: []collection.ReturnField{
			{Name: "results", Type: "list of dict", Returned: "success", Description: "Each prompt's id, passed, input and output tokens, duration, cost when priced, and error when its call failed."},
			{Name: "passed", Type: "int", Returned: "success", Description: "How many prompts passed."},
			{Name: "count", Type: "int", Returned: "always", Description: "How many prompts the set holds."},
			{Name: "score", Type: "float", Returned: "success", Description: "passed divided by count."},
			{Name: "provider", Type: "string", Returned: "success", Description: "The provider device the set was asked of."},
			{Name: "model", Type: "string", Returned: "success", Description: "The model that answered, as the task named it."},
			{Name: "cost", Type: "float", Returned: "when every call is priced", Description: "What the set cost, in currency."},
			{Name: "currency", Type: "string", Returned: "when every call is priced", Description: "The price table's currency."},
			{Name: "saved", Type: "string", Returned: "with save_as", Description: "The results file, relative to the project."},
			{Name: "estimate", Type: "dict", Returned: "in a check", Description: "The set's estimated input tokens, and each prompt's estimate."},
		},
		Examples: []collection.Example{
			{Name: "Score a local model", RunbookYAML: "- name: Bench\n  ai.bench.run:\n    model: \"qwen3:14b\"\n    max_tokens: 50\n    prompts:\n      - {id: capital, text: \"Capital of France? One word.\", expect: {contains: Paris}}\n      - {id: label, text: \"Answer {\\\"label\\\": \\\"bug\\\"} as JSON.\", expect: {json_field: {path: label, equals: bug}}}\n    save_as: qwen3-14b.json\n"},
		},
		SeeAlso: []string{"ai.chat", "ai.provider.probe"},
	}
}
