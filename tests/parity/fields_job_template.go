package parity

// JobTemplateFields classifies every field of AWX's job_template.
//
// Produced by reading each field against our model with file:line evidence,
// then adversarially refuting every REPRESENTED claim: a false "we hold
// this" is the dangerous direction, because it makes an import silently
// lossy while the report says it is fine. REPRESENTED is drawn strictly,
// same name and same type, so a rename or a reshape lands in CONVERTIBLE
// with the transformation written down.
//
// The verdicts that surprised the analysis are worth reading before
// trusting the summary:
//
//   - forks is CONVERTIBLE despite an exact name and type match, because
//     AWX's exported value is 0 meaning "use the deployment default" and
//     our FieldSpec declares Min: 1. An importer that copied the number
//     across would be refused by our own validation.
//   - job_tags needs a split even though the name now matches: B1 renamed
//     our field from tags to job_tags, but AWX sends "deploy, frontend" as
//     one comma-separated string where we declare a list.
//   - extra_vars matches by name and is a raw YAML document string in AWX
//     against our TypeMap, so it needs parsing rather than copying.
//   - Every ask_*_on_launch boolean maps onto membership of our Prompts
//     list, which is lossless only while our field names are AWX's. Before
//     B1 renamed job_tags, this one was not.
var JobTemplateFields = []Field{
	// AWX's REST envelope.
	{Name: "id", Status: Metadata, Note: "the source instance's primary key"},
	{Name: "type", Status: Metadata, Note: "the UnifiedJobTemplate subclass discriminator"},
	{Name: "url", Status: Metadata, Note: "the source instance's canonical path"},
	{Name: "related", Status: Metadata, Note: "hypermedia sub-resource URLs, regenerated per response"},
	{Name: "summary_fields", Status: Metadata, Note: "the block itself is envelope, but it is NOT dismissible: AWX puts credentials, labels and instance_groups here and nowhere else, so its keys are classified in their own right as job_template_summary_fields"},
	{Name: "created", Status: Metadata, Note: "server-assigned; ours comes from TimestampMixin"},
	{Name: "modified", Status: Metadata, Note: "server-assigned; ours comes from TimestampMixin"},

	// Held directly.
	{Name: "name", Status: Represented, Ours: "launch.Template.Name"},
	{Name: "description", Status: Represented, Ours: "launch.Template.Description"},
	{Name: "allow_simultaneous", Status: Represented, Ours: "launch.Template.AllowSimultaneous"},
	{Name: "survey_enabled", Status: Represented, Ours: "ent Template.survey_enabled / launch.Survey.Enabled"},
	{Name: "limit", Status: Represented, Ours: `launch.FieldSpec "limit" (TypeString)`},
	{Name: "verbosity", Status: Represented, Ours: `launch.FieldSpec "verbosity" (TypeInt, 0 to 4)`},
	{Name: "timeout", Status: Represented, Ours: `launch.FieldSpec "timeout" (TypeInt, 0 permitted)`},

	// Held, but the value has to be transformed.
	{
		Name: "organization", Status: Convertible, Ours: "launch.Template.OrganizationID",
		Conversion: "resolve the AWX organization id to our own Organization row, by name. Our value is derived from the inventory rather than submitted, so an import must create the inventory under the right tenant and let the store derive it.",
	},
	{
		Name: "inventory", Status: Convertible, Ours: "launch.Template.InventoryID",
		Conversion: "resolve the AWX inventory id to our own Inventory row, by name, creating it if absent.",
	},
	{
		Name: "playbook", Status: Convertible, Ours: "launch.Template.Definition",
		Conversion: `rename playbook to definition and set KindName to "playbook" in the same write. The value itself needs no reshaping now that our grammar is project-relative paths, which is what FAILURE_PATTERNS #113 restored.`,
	},
	{
		Name: "forks", Status: Convertible, Ours: `launch.FieldSpec "forks" (TypeInt, Min 1, Max 1000)`,
		Conversion: `the name and type match exactly, but AWX exports 0 meaning "use the deployment default" and our Min is 1, so 0 must be translated to omitting the field rather than copied. Copying it is refused by our own validation.`,
	},
	{
		Name: "extra_vars", Status: Convertible, Ours: `launch.FieldSpec "extra_vars" (TypeMap)`,
		Conversion: "AWX sends a raw YAML or JSON document as a string; ours is a map. Parse it, and fail the import loudly rather than storing an unparsed string, because a silently empty extra_vars changes what a play does.",
	},
	{
		Name: "job_tags", Status: Convertible, Ours: `launch.FieldSpec "job_tags" (TypeStringList)`,
		Conversion: `the name matches now that B1 renamed our field from tags to job_tags; still split AWX's comma-separated string "deploy, frontend" into a list, trimming each element.`,
	},
	{
		Name: "skip_tags", Status: Convertible, Ours: `launch.FieldSpec "skip_tags" (TypeStringList)`,
		Conversion: "the name matches; split AWX's comma-separated string into a list.",
	},
	{
		Name: "ask_variables_on_launch", Status: Convertible, Ours: "launch.Template.Prompts",
		Conversion: `true adds "extra_vars" to the Prompts list; false omits it. AWX calls the field variables and we call it extra_vars, so the mapping is by position in a translation table rather than by name.`,
	},
	{
		Name: "ask_limit_on_launch", Status: Convertible, Ours: "launch.Template.Prompts",
		Conversion: `true adds "limit" to the Prompts list; false omits it.`,
	},
	{
		Name: "ask_verbosity_on_launch", Status: Convertible, Ours: "launch.Template.Prompts",
		Conversion: `true adds "verbosity" to the Prompts list; false omits it.`,
	},
	{
		Name: "ask_tags_on_launch", Status: Convertible, Ours: "launch.Template.Prompts",
		Conversion: `true adds "job_tags" to the Prompts list; false omits it. Lossless now that B1 renamed our field to match AWX's own.`,
	},
	{
		Name: "ask_skip_tags_on_launch", Status: Convertible, Ours: "launch.Template.Prompts",
		Conversion: `true adds "skip_tags" to the Prompts list; false omits it.`,
	},

	// Nowhere to put it.
	{Name: "project", Status: Gap, Phase: "A1 Projects", Note: "the keystone. A template references a Project by foreign key and its playbook is chosen from that project's synced tree."},
	{Name: "scm_branch", Status: Gap, Phase: "A1b git sync", Note: "the per-template branch override, which also needs the project's allow_override."},
	{Name: "ask_scm_branch_on_launch", Status: Gap, Phase: "A1b git sync", Note: "promptable branch, the third link in AWX's branch-override chain."},
	{Name: "ask_credential_on_launch", Status: Gap, Phase: "A2 Credential Types", Note: "AWX also requires a prompted credential to be the same type as the one it replaces."},
	{Name: "custom_virtualenv", Status: Gap, Phase: "A3 Execution Environments", Note: "deprecated by AWX in favour of execution environments, which is the successor concept A3 builds."},
	{Name: "ask_inventory_on_launch", Status: Gap, Phase: "B1 typed fields and per-field prompts", Note: "our inventory is a template column rather than a launch field, so making it promptable means making it a field."},
	{Name: "webhook_service", Status: Gap, Phase: "D4 Webhook launch"},
	{Name: "webhook_credential", Status: Gap, Phase: "D4 Webhook launch", Note: "also blocked on A2, since it names a credential."},
	{Name: "job_type", Status: Gap, Phase: "unowned", Note: "run against check. Blocked on a dry-run pass in internal/engine that does not exist; the roadmap names job_type and diff_mode as one unscheduled item rather than two template fields that silently do nothing."},
	{Name: "diff_mode", Status: Gap, Phase: "unowned", Note: "AWX's Show Changes. Same unscheduled dry-run item as job_type."},
	{Name: "ask_job_type_on_launch", Status: Gap, Phase: "unowned", Note: "promptable job_type; follows job_type."},
	{Name: "ask_diff_mode_on_launch", Status: Gap, Phase: "unowned", Note: "promptable diff_mode; follows diff_mode."},
	{Name: "become_enabled", Status: Gap, Phase: "unowned", Note: "privilege escalation as a template flag. Cheap once B1 renders per-kind fields, since it is one more boolean FieldSpec on the playbook kind."},
	{Name: "force_handlers", Status: Gap, Phase: "unowned", Note: "another playbook-kind FieldSpec B1 could carry."},
	{Name: "start_at_task", Status: Gap, Phase: "unowned", Note: "another playbook-kind FieldSpec B1 could carry."},
	{Name: "use_fact_cache", Status: Gap, Phase: "unowned", Note: "per-template fact caching. We have a Fact entity but no cache bridge into an Ansible run."},
	{Name: "host_config_key", Status: Gap, Phase: "unowned", Note: "provisioning callbacks, which let a host request its own configuration. No owning phase."},

	{Name: "execution_environment", Status: Gap, Phase: "A3 Execution Environments", Note: "the container image this template's jobs run in. Our nearest thing is ANSIBLE_RUNNER_IMAGE, one fixed string in the runner's composition root, which is exactly what A3 replaces with a record."},
	{Name: "ask_execution_environment_on_launch", Status: Gap, Phase: "A3 Execution Environments", Note: "promptable execution environment; follows the field."},
	{Name: "ask_labels_on_launch", Status: Gap, Phase: "B3 Labels", Note: "promptable labels. AWX appends prompted labels to the template's own rather than replacing them, which is a merge rule an import has to preserve."},
	{Name: "ask_instance_groups_on_launch", Status: Gap, Phase: "D1 Capacity and Instance Groups", Note: "promptable runner affinity; follows instance_groups."},
	{Name: "prevent_instance_group_fallback", Status: Gap, Phase: "D1 Capacity and Instance Groups", Note: "strict affinity: refuse to run rather than fall back to another group. Meaningless until instance groups exist, and a safety property once they do."},

	{
		Name: "ask_forks_on_launch", Status: Convertible, Ours: "launch.Template.Prompts",
		Conversion: `true adds "forks" to the Prompts list; false omits it. Our forks FieldSpec already exists on both kinds, so this is the same boolean-to-membership mapping the other ask_* fields use.`,
	},
	{
		Name: "ask_timeout_on_launch", Status: Convertible, Ours: "launch.Template.Prompts",
		Conversion: `true adds "timeout" to the Prompts list; false omits it.`,
	},

	// Decided against.
	{
		Name: "job_slice_count", Status: Unsupported,
		Note: "AWX shards one Ansible inventory across parallel processes. Our fan-out is already per-device, which is a different execution model, so the field cannot be honoured meaningfully. An import must report it as unsupported rather than accept it and ignore it. The corpus carries a template that actually uses it (job_slice_count 4), so this is a live incompatibility rather than a theoretical one.",
	},
	{
		Name: "ask_job_slice_count_on_launch", Status: Unsupported,
		Note: "promptable slice count. Follows job_slice_count: a prompt for a value we cannot honour would be worse than the field, because the operator would answer it.",
	},
}
