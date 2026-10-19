// The reference documentation of the ai.model methods.
package model

import "github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

// guardText is what every changing method says about the guard.
const guardText = " It runs only against an ai_provider whose kind is ollama and whose record declares ai_host_guard: single_resident, and keeps to it: at most one model in memory. It speaks only Ollama's HTTP API, so it never restarts, stops or signals the host, or the machine or distribution it runs in."

var (
	modelParamDoc   = collection.Param{Name: paramModel, Type: "string", Required: true, Description: "The model, as Ollama names it; a name with no tag means :latest."}
	timeoutParamDoc = collection.Param{Name: paramTimeout, Type: "string", Default: "10m", Description: "How long the change may take, from 1s to 6h."}
	modelReturn     = collection.ReturnField{Name: "model", Type: "string", Returned: "always", Description: "The model, with its tag."}
)

func listDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Lists the models an Ollama host has on disk and the ones in memory.",
		Description: "Asks the Ollama host for its models (GET /api/tags) and the ones loaded in memory (GET /api/ps). It only reads, and needs no host guard, so a check of it is the task itself.",
		Returns: []collection.ReturnField{
			{Name: "models", Type: "list of dict", Returned: "always", Description: "Each model on disk: name (with its tag), size in bytes and digest."},
			{Name: "resident", Type: "list of string", Returned: "always", Description: "The models in memory."},
		},
		Examples: []collection.Example{{Name: "What the workstation has", RunbookYAML: "- name: Models\n  ai.model.list: {}\n"}},
		SeeAlso:  []string{"ai.model.load", "ai.provider.list"},
	}
}

func loadDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Loads a model into an Ollama host's memory, unloading every other model first so one is resident at a time.",
		Description: "Loads the model to stay in memory (keep_alive -1), after unloading every other resident model, so the host never holds two; a model already alone in memory is no change. The model must be on the host already (ai.model.pull). The undo loads the model that was resident before, or unloads this one when none was. A check reports what is in memory and what would change." + guardText,
		Params:      []collection.Param{modelParamDoc, timeoutParamDoc},
		Returns: []collection.ReturnField{
			modelReturn,
			{Name: "resident_before", Type: "list of string", Returned: "always", Description: "The models in memory before."},
			{Name: "unloaded", Type: "list of string", Returned: "always", Description: "The models unloaded (or, in a check, that would be)."},
			{Name: "loaded", Type: "bool", Returned: "always", Description: "Whether the model was loaded (or would be)."},
		},
		Examples: []collection.Example{{Name: "Make one model resident", RunbookYAML: "- name: Load\n  ai.model.load:\n    model: \"qwen3:14b\"\n"}},
		SeeAlso:  []string{"ai.model.unload", "ai.model.pull"},
	}
}

func unloadDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Unloads a model from an Ollama host's memory.",
		Description: "Unloads the model (keep_alive 0); a model not in memory is no change. The undo loads it again. A check reports whether it is in memory." + guardText,
		Params:      []collection.Param{modelParamDoc, timeoutParamDoc},
		Returns: []collection.ReturnField{
			modelReturn,
			{Name: "resident_before", Type: "list of string", Returned: "always", Description: "The models in memory before."},
		},
		Examples: []collection.Example{{Name: "Free the memory", RunbookYAML: "- name: Unload\n  ai.model.unload:\n    model: \"qwen3:14b\"\n"}},
		SeeAlso:  []string{"ai.model.load"},
	}
}

func pullDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Pulls a model onto an Ollama host when it is not there.",
		Description: "Pulls the model through Ollama (POST /api/pull, its progress streamed until it says success) and confirms the host then lists it; a model already on the host is no change. The undo removes the model, recorded only when this pull added it, so undoing a pull never removes a model that was there before. A check reports whether the model is present." + guardText,
		Params:      []collection.Param{modelParamDoc, timeoutParamDoc},
		Returns: []collection.ReturnField{
			modelReturn,
			{Name: "present_before", Type: "bool", Returned: "always", Description: "Whether the model was on the host before."},
		},
		Examples: []collection.Example{{Name: "Have the model", RunbookYAML: "- name: Pull\n  ai.model.pull:\n    model: \"qwen3:14b\"\n    timeout: 1h\n"}},
		SeeAlso:  []string{"ai.model.remove", "ai.model.load"},
	}
}

func removeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes a model from an Ollama host, unloading it first when it is in memory.",
		Description: "Removes the model from the host's disk (DELETE /api/delete), unloading it first when it is resident; a model not on the host is no change. The undo pulls the name again, which brings back what its tag names at that moment. A check reports whether the model is present." + guardText,
		Params:      []collection.Param{modelParamDoc, timeoutParamDoc},
		Returns: []collection.ReturnField{
			modelReturn,
			{Name: "present_before", Type: "bool", Returned: "always", Description: "Whether the model was on the host before."},
		},
		Examples: []collection.Example{{Name: "Reclaim the disk", RunbookYAML: "- name: Remove\n  ai.model.remove:\n    model: \"llama3.1:8b\"\n"}},
		SeeAlso:  []string{"ai.model.pull"},
	}
}
