// The reference documentation of ai.prompt.verify.
package prompt

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/aitask"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func verifyDoc() collection.Doc {
	params := aitask.DocParams(aitask.ParamPrompt, aitask.ParamPromptFile, aitask.ParamSignature)
	signing := aitask.DocParams(aitask.ParamSigning)[0]
	signing.Default = "required"
	params = append(params, signing,
		collection.Param{Name: aitask.ParamUse, Type: "string", Description: "The use the document must be for; any when absent."},
	)
	return collection.Doc{
		Summary:     "Verifies a signed prompt bundle, parameter set or routing table against the project's trusted keys, as a call would.",
		Description: "Reads a document (inline as prompt with signature, or from the project as prompt_file with its .sig), refusing what two readers could read differently: a duplicate key at any depth, invalid UTF-8, an unpaired surrogate, a number with an exponent or beyond 2^53. It then checks, in order, that a trusted key (ai/trusted-keys.yaml) verifies the Ed25519 signature over its canonical form, that the key's role lists the document's use, that neither key nor document has expired, and that its version is not older than the newest of its name already used (.pleiades/prompt-versions.json). Any failure fails the task, naming which. It records nothing, so a check of it is the task itself. A signature is provenance and integrity: it says who approved the text and that it is unchanged, not that what a model does with it is safe.",
		Params:      params,
		Returns: []collection.ReturnField{
			{Name: "status", Type: "string", Returned: "success", Description: "verified; or, under a policy that admits them, unsigned or not checked."},
			{Name: "kind", Type: "string", Returned: "success", Description: "prompt, parameters or routes."},
			{Name: "name", Type: "string", Returned: "success", Description: "The document's name."},
			{Name: "version", Type: "int", Returned: "success", Description: "The document's version."},
			{Name: "use", Type: "string", Returned: "success", Description: "The use the document is for."},
			{Name: "hash", Type: "string", Returned: "success", Description: "The SHA-256 of its canonical form, in hex."},
			{Name: "key", Type: "string", Returned: "when signed", Description: "The signing key's id."},
		},
		Examples: []collection.Example{
			{Name: "Refuse a release whose prompt is not signed", RunbookYAML: "- name: Verify the triage prompt\n  ai.prompt.verify:\n    prompt_file: triage\n    use: triage\n"},
		},
		SeeAlso: []string{"ai.chat"},
	}
}
