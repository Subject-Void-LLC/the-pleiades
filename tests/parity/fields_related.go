package parity

// The classification tables for everything a job template points at.
//
// Three of these resources do not exist here in any form (Project,
// CredentialType, Schedule), so their tables are mostly gaps and their
// value is in the phase attribution rather than the verdict: they turn
// "we have no Projects" into a list of the twenty-five specific things a
// Project has to hold.
//
// Two do exist (surveys, the activity stream) and those tables are the
// interesting ones, because a resource we have is where a mismatch can
// hide behind a plausible name.

// ProjectFields classifies AWX's project, the content source a job
// template's playbook is chosen from. We have no Project entity at any
// layer, so every substantive field is a gap; the split between A1a and
// A1b is the staging the roadmap argues for, where a local-path project
// (AWX's own "manual" type) delivers the entity, the foreign key and a
// tenanted playbook catalog with no git machinery at all.
var ProjectFields = []Field{
	{Name: "id", Status: Metadata, Note: "the source instance's primary key"},
	{Name: "type", Status: Metadata, Note: "resource discriminator"},
	{Name: "url", Status: Metadata, Note: "the source instance's canonical path"},
	{Name: "related", Status: Metadata, Note: "hypermedia sub-resource URLs. See testdata/README.md for the ones not yet captured, several of which are A1 design input."},
	{Name: "summary_fields", Status: Metadata, Note: "denormalised previews, including last_update which is the sync status a list view renders"},
	{Name: "created", Status: Metadata},
	{Name: "modified", Status: Metadata},

	{Name: "name", Status: Gap, Phase: "A1a local-path projects", Note: "unique within an organization, as our other tenanted names are"},
	{Name: "description", Status: Gap, Phase: "A1a local-path projects"},
	{Name: "organization", Status: Gap, Phase: "A1a local-path projects", Note: "what makes the playbook catalog tenanted. The catalog as built is deployment-global, so every organization's template form offers every organization's playbooks."},
	{Name: "local_path", Status: Gap, Phase: "A1a local-path projects", Note: `AWX's "manual" project: a directory on disk rather than a repository. Empty here because this project is git-backed, and it is exactly the shape A1a delivers first.`},
	{Name: "status", Status: Gap, Phase: "A1a local-path projects", Note: "a local-path project is trivially successful; a git one carries a real sync outcome"},

	{Name: "scm_type", Status: Gap, Phase: "A1b git sync", Note: "git, svn, archive, or empty for a manual project"},
	{Name: "scm_url", Status: Gap, Phase: "A1b git sync"},
	{Name: "scm_branch", Status: Gap, Phase: "A1b git sync", Note: "the project end of the branch-override chain the job template's own scm_branch extends"},
	{Name: "scm_refspec", Status: Gap, Phase: "A1b git sync", Note: "AWX_PARITY.md already records the branch/refspec/override chain as a gap"},
	{Name: "scm_clean", Status: Gap, Phase: "A1b git sync"},
	{Name: "scm_track_submodules", Status: Gap, Phase: "A1b git sync"},
	{Name: "scm_delete_on_update", Status: Gap, Phase: "A1b git sync"},
	{Name: "scm_revision", Status: Gap, Phase: "A1b git sync", Note: "the commit a job actually ran, which is the audit fact worth most here: without it a job record cannot say which code it ran."},
	{Name: "allow_override", Status: Gap, Phase: "A1b git sync", Note: "whether a template may override the branch. The delegation AWX_PARITY.md describes: a project admin permits it, a template admin sets it, a launcher may be prompted for it."},
	{Name: "timeout", Status: Gap, Phase: "A1b git sync", Note: "bounds the sync, not a job"},
	{Name: "copy_from_dir", Status: Gap, Phase: "A1b git sync"},

	{Name: "credential", Status: Gap, Phase: "A2 Credential Types", Note: "the credential a private repository is cloned with, so a git-backed project depends on A2 as well as A1b"},
	{Name: "custom_virtualenv", Status: Gap, Phase: "A3 Execution Environments", Note: "deprecated by AWX in favour of execution environments"},
}

// ProjectUpdateFields classifies a project sync run.
//
// The table worth reading for the roadmap's own argument: a project update
// is a JOB. It has a status, a start and a finish, an execution
// environment and a job_type, and it appears in AWX's Jobs list beside a
// playbook run. That is axis A (UnifiedJobTemplate) made concrete, and it
// is why C1 has to exist before schedules and notifications rather than
// after: a schedule attaches to a project sync exactly as it attaches to a
// job template.
var ProjectUpdateFields = []Field{
	{Name: "id", Status: Metadata},
	{Name: "type", Status: Metadata, Note: "the UnifiedJob subclass discriminator"},
	{Name: "url", Status: Metadata},

	{Name: "project", Status: Gap, Phase: "A1b git sync", Note: "which project synced"},
	{Name: "scm_revision", Status: Gap, Phase: "A1b git sync", Note: "the commit this sync landed on"},
	{Name: "job_type", Status: Gap, Phase: "A1b git sync", Note: `"check" here means an SCM update mode, unrelated to a job template's run/check`},
	{Name: "job_tags", Status: Gap, Phase: "A1b git sync", Note: "empty on a sync; present because AWX shares one job model across kinds"},

	{Name: "status", Status: Gap, Phase: "C1 Launchable", Note: "our dispatch.Job has a state, but only a job template can produce one. A sync has no launchable to be a job of until C1."},
	{Name: "failed", Status: Gap, Phase: "C1 Launchable", Note: "AWX carries a boolean beside the status string"},
	{Name: "started", Status: Gap, Phase: "C1 Launchable"},
	{Name: "finished", Status: Gap, Phase: "C1 Launchable"},

	{Name: "execution_environment", Status: Gap, Phase: "A3 Execution Environments", Note: "a sync runs in a container too"},
}

// CredentialTypeFields classifies AWX's credential_type: the schema and
// injection rules behind every credential a template binds.
//
// AWX_PARITY.md calls this the single biggest gap and the corpus shows
// why. This is a CUSTOM type (managed: false) with a secret input and two
// injector targets, and a customer's playbook reads the environment
// variables it injects. Nothing about a migration works without it.
var CredentialTypeFields = []Field{
	{Name: "id", Status: Metadata},
	{Name: "type", Status: Metadata},
	{Name: "url", Status: Metadata},
	{Name: "related", Status: Metadata},
	{Name: "summary_fields", Status: Metadata},
	{Name: "created", Status: Metadata},
	{Name: "modified", Status: Metadata},

	{Name: "name", Status: Represented, Ours: "credtype.CredentialType.Name"},
	{Name: "description", Status: Represented, Ours: "credtype.CredentialType.Description"},
	{
		Name: "kind", Status: Represented, Ours: "credtype.CredentialType.Kind",
		Note: `AWX's coarse grouping (cloud, net, ssh, vault). It is what the "one credential per type, vault exempted" bind rule keys on, which is why credtype.Kind is a closed twelve-value vocabulary rather than a free string: an open one would make that rule unenforceable.`,
	},
	{
		Name: "namespace", Status: Represented, Ours: "credtype.CredentialType.Namespace",
		Note: "stable identifier for a managed type; a custom type carries its own, and the corpus fixture proves it (custom_api_token). It is what an import keys on to decide whether a type already exists, so it is required here rather than managed-only.",
	},
	{
		Name: "managed", Status: Represented, Ours: "credtype.CredentialType.Managed",
		Note: "whether AWX ships it. A managed type cannot be edited, which an import must respect rather than recreating the built-ins as custom types.",
	},
	{
		Name: "inputs", Status: Represented, Ours: "credtype.CredentialType.Inputs",
		Note: `the schema: fields with id, type, label and secret, plus a required list. "secret": true is what decides encryption at rest and redaction on the way out; InputSchema.SecretFields is the single place that decision is made, mirroring launch.Survey.SecretVariables for survey answers. The two schemas stay parallel rather than merged because AWX has two vocabularies (seven survey question types encoding secrecy IN the type, two credential input types encoding it in an orthogonal boolean), and merging them would produce values an import has nowhere to put.`,
	},
	{
		Name: "injectors", Status: Represented, Ours: "credtype.CredentialType.Injectors",
		Note: `how a secret reaches Ansible: env, extra_vars, file and multi-key file, with values written as Jinja templates over the input ids ("{{ api_token }}"). This is what made the shared renderer (internal/render) a hard requirement rather than a convenience, and C3 Notifications reuses it. Injectors.Validate compiles every template at SAVE time and refuses one naming an input the type does not declare, so a launch can never fail on an undefined variable.`,
	},
}

// ScheduleFields classifies AWX's schedule.
//
// Every field now exists. The shape differs in one deliberate way and it is
// worth stating rather than reading as a gap: AWX carries DTSTART and TZID
// folded inside the rrule string, and this platform stores rrule, timezone
// and dtstart as three columns. The information is identical; splitting it
// makes the zone and the anchor queryable and editable without parsing the
// rule, and makes rrule mean exactly one thing.
var ScheduleFields = []Field{
	{Name: "id", Status: Metadata},
	{Name: "type", Status: Metadata},
	{Name: "url", Status: Metadata},

	{Name: "name", Status: Represented, Ours: "schedule.Schedule.Name"},
	{Name: "enabled", Status: Represented, Ours: "schedule.Schedule.Enabled", Note: "a disabled schedule is kept rather than deleted, the same distinction Survey.Enabled draws. Disabling clears next_run: a schedule that will not run must not advertise a time."},
	{Name: "rrule", Status: Represented, Ours: "schedule.Schedule.RRule", Note: `RFC5545, parsed and expanded by internal/schedule/rrule against a deliberately bounded constraint set, with a preview endpoint so an author sees the next occurrences before saving. Parity with AWX is earned rather than claimed: the engine is tested against occurrence vectors generated from python-dateutil, the library AWX itself schedules on, across daylight saving transitions in both hemispheres, leap days, ordinal weekdays, BYSETPOS and exclusion rules straddling a transition. EXRULE and EXDATE live in their own exclusions field rather than inside the rule.`},
	{Name: "dtstart", Status: Represented, Ours: "schedule.Schedule.DTStart", Note: "its own column rather than folded into the rrule, since RFC5545 takes from it every field the rule leaves unspecified"},
	{Name: "dtend", Status: Represented, Ours: "schedule.Schedule.DTEnd", Note: "nil for an open-ended schedule. Separate from the rule's own UNTIL: that is part of what an author wrote, this is an operator saying stop after then."},
	{Name: "next_run", Status: Represented, Ours: "schedule.Schedule.NextRun", Note: "AWX computes it per response; here it is a materialised cache, recomputed from the rule on every write and after every fire. A keyset-paginated due scan cannot index a value that exists only in a response body, and the rrule remains the source of truth."},
	{
		Name: "timezone", Status: Represented, Ours: "schedule.Schedule.Timezone",
		Note: `carried beside the rrule rather than inside it. "America/New_York" with a daily rule is exactly the DST case the phase gate is written around: the wall-clock hour is preserved across the transition, so the interval between two runs is not always 24 hours. Validated at save time against a generated allowlist built from the same time zone archive the binary embeds, so a zone the picker offers is a zone the server can load.`,
	},
	{
		Name: "extra_data", Status: Convertible, Ours: "launch.SavedConfig.Fields",
		Conversion: "the same launch-override bundle we already store for relaunch, keyed the same way. An import writes it as a SavedLaunchConfig and the schedule points at it through its own saved_config edge.",
	},
}

// ActivityStreamFields classifies AWX's activity stream against ours.
//
// We have this resource, which makes it the table most worth checking: a
// mismatch here hides behind a plausible name. One does. AWX records the
// BEFORE AND AFTER value of every changed field; we record only that an
// update happened. An auditor asking "what did that change actually do"
// gets an answer from AWX and none from us.
var ActivityStreamFields = []Field{
	{Name: "id", Status: Metadata},
	{Name: "type", Status: Metadata},

	{Name: "timestamp", Status: Represented, Ours: "activity.Entry.At"},
	{
		Name: "operation", Status: Convertible, Ours: "activity.Entry.Action",
		Conversion: `AWX's verb is the present tense ("update", "create", "delete") and ours is the past ("updated", "created", "deleted"), because ours is rendered into a sentence by Entry.Describe. A translation table, and it must be a table rather than a suffix: we also carry "attested", which AWX has no operation for.`,
	},
	{
		Name: "actor", Status: Convertible, Ours: "activity.Entry.Actor",
		Conversion: `AWX sends an object {id, username}; ours is the authenticated subject as a string. Take the username. Our writer refuses an entry with no actor at all (ErrUnattributed), where AWX permits a null actor for system-initiated changes, so a system change has no representation here.`,
	},
	{
		Name: "object1", Status: Convertible, Ours: "activity.Entry.ObjectKind",
		Conversion: `AWX's resource name ("project") onto our kind vocabulary (organization, team, user, role binding, contact). Half of AWX's resources have no kind of ours, so the mapping fails closed rather than inventing one.`,
	},
	{
		Name: "object1_search_fields", Status: Convertible, Ours: "activity.Entry.ObjectName",
		Conversion: "AWX's searchable label for the object onto the name we captured at write time. Both are the name as it was, which is the property that lets a deletion's record outlive the thing it describes.",
	},
	{
		Name: "changes", Status: Gap, Phase: "unowned",
		Note: `AWX records the before and after of every changed field ({"scm_branch": ["develop", "main"]}). We record that an update happened and nothing about what it did, so "who changed the branch and what was it before" is unanswerable here. Our Revision entity does field-level before/after but only for device properties. This is a real audit gap with no owning phase, and it is cheap next to the entities around it.`,
	},
}

// SurveySpecFields classifies the survey document itself. The questions
// inside it are classified separately (SurveyQuestionFields), because that
// is where the mapping actually lives.
var SurveySpecFields = []Field{
	{
		Name: "name", Status: Gap, Phase: "unowned",
		Note: "AWX names a survey; our launch.Survey carries only Enabled and Questions. Small, and it costs an import the survey's own title.",
	},
	{
		Name: "description", Status: Gap, Phase: "unowned",
		Note: "as name. The prompt an operator reads above the questions.",
	},
	{Name: "spec", Status: Convertible, Ours: "launch.Survey.Questions", Conversion: "the ordered question list, converted per question. See SurveyQuestionFields."},
}

// SurveyQuestionFields classifies one survey question.
//
// The best-represented resource in the corpus, and not by accident: our
// seven question types are AWX's seven, character for character
// (text, textarea, password, integer, float, multiplechoice, multiselect),
// so the type vocabulary needs no translation at all.
//
// One divergence is deliberate and worth keeping: we refuse a password
// question that carries a default, because a default password is a
// credential stored in the template. AWX permits it. An import of such a
// question must drop the default and say so rather than storing it.
var SurveyQuestionFields = []Field{
	{Name: "variable", Status: Represented, Ours: "launch.Question.Variable"},
	{Name: "required", Status: Represented, Ours: "launch.Question.Required"},
	{Name: "type", Status: Represented, Ours: "launch.Question.Type", Note: "our seven QuestionType values are AWX's seven, character for character"},
	{Name: "min", Status: Represented, Ours: "launch.Question.Min", Note: "AWX's own semantics: a numeric bound or a text length"},
	{Name: "max", Status: Represented, Ours: "launch.Question.Max"},
	{
		Name: "question_name", Status: Convertible, Ours: "launch.Question.Label",
		Conversion: "a rename only.",
	},
	{
		Name: "question_description", Status: Convertible, Ours: "launch.Question.Help",
		Conversion: "a rename only.",
	},
	{
		Name: "default", Status: Convertible, Ours: "launch.Question.Default",
		Conversion: `stored as a string on both sides and converted per type, so the value copies directly, EXCEPT for a password question: we refuse a non-empty default because it is a credential in the template, and an import must drop it and report the drop.`,
	},
	{
		Name: "choices", Status: Convertible, Ours: "launch.Question.Choices",
		Conversion: `AWX sends the options as one newline-separated string ("dev\nqa\nprod"); ours is a list. Split on newlines and trim. An empty string means no choices rather than one empty choice, which a naive split would produce and our validation would then reject as an option nobody can pick.`,
	},
	{
		Name: "new_question", Status: Metadata,
		Note: "an AWX editor flag marking a question added but not yet saved, not part of the saved survey",
	},
}

// JobTemplateSummaryFields classifies a job template's summary_fields
// block, which is NOT purely AWX's REST envelope and was misclassified as
// such until a template carrying real relationships arrived.
//
// The reason this table has to exist: AWX gives a job template no
// root-level credentials, labels or instance_groups field. Those
// relationships live only behind sub-resource URLs and as previews here,
// so dismissing this block cost the measurement three entire
// relationships at once, including every bound credential. An import that
// read only root-level fields would silently drop an SSH key, a Vault
// credential and an AWS credential from a template that names all three.
//
// The entries that ARE envelope stay envelope, and say why: they preview
// a relationship a root-level field already carries.
var JobTemplateSummaryFields = []Field{
	{Name: "inventory", Status: Metadata, Note: "previews the root-level inventory field, which carries the relationship"},
	{Name: "project", Status: Metadata, Note: "previews the root-level project field, itself a gap owned by A1"},
	{Name: "webhook_credential", Status: Metadata, Note: "previews the root-level webhook_credential field, itself a gap owned by D4"},

	{
		Name: "credentials", Status: Represented, Ours: "launch.Template.CredentialIDs (the Template-to-Credential edge)", Phase: "A2 Credential Types",
		Note: "the only place a template's bound credentials appear in this payload, since AWX has no root-level credentials field. The corpus binds three at once (ssh, vault, aws), which is exactly what the Template-to-Credential binding now expresses: credtype.CheckBinding enforces at most one credential per kind with vault exempted by distinct identifier, and GET/PUT /templates/{id}/credentials read and replace the set.",
	},
	{
		Name: "labels", Status: Gap, Phase: "B3 Labels",
		Note: "the only place a template's labels appear. Paginated (count plus results) even when nested, so a reader must not assume the preview is complete.",
	},
	{
		Name: "instance_groups", Status: Gap, Phase: "D1 Capacity and Instance Groups",
		Note: "the only place a template's runner affinity appears. The corpus carries both a node group and a Kubernetes container group (is_container_group), which are different execution substrates rather than two names for one thing.",
	},
	{
		Name: "created_by", Status: Gap, Phase: "unowned",
		Note: "our Template has no creator edge. Authorship is not authority here, so this is provenance rather than access, but an audit surface that records who made every other object should record who made this one.",
	},
	{
		Name: "recent_jobs", Status: Gap, Phase: "B2 List metadata",
		Note: "what the Activity and Last Ran columns render. We can compute it from ListForTemplate; nothing projects it yet.",
	},
}

// JobTemplateRelatedFields classifies the keys of a job template's
// related block.
//
// The block is a map of relationship or action name to URL, and its KEYS
// are the API telling you what a job template is connected to. That makes
// it a checklist, and it is the checklist that was available and unread
// when summary_fields was dismissed as envelope: related listed
// credentials, labels and instance_groups as endpoints while the table
// across the room called the block previewing them nothing worth
// representing (LESSONS_LEARNED.md #104).
//
// Classifying it mechanically found six more relationships that nothing
// mentioned: the template's schedules, its three notification trigger
// points, AWX's deprecated extra_credentials alias, and modified_by.
//
// Relationships already classified under another table are METADATA here
// rather than counted twice, and each says where it is actually scored.
// Double counting a gap would inflate the backlog and make a phase look
// larger than the work it owes.
var JobTemplateRelatedFields = []Field{
	// Scored elsewhere.
	{Name: "inventory", Status: Metadata, Note: "the relationship is scored on the root-level inventory field"},
	{Name: "project", Status: Metadata, Note: "scored on the root-level project field"},
	{Name: "labels", Status: Metadata, Note: "scored under job_template_summary_fields.labels"},
	{Name: "credentials", Status: Metadata, Note: "scored under job_template_summary_fields.credentials"},
	{Name: "instance_groups", Status: Metadata, Note: "scored under job_template_summary_fields.instance_groups"},
	{Name: "created_by", Status: Metadata, Note: "scored under job_template_summary_fields.created_by"},

	// Actions rather than relationships: there is nothing to store, and
	// what matters is whether the capability exists at all.
	{Name: "launch", Status: Metadata, Note: "an action. We have POST /templates/{id}/launch."},
	{Name: "copy", Status: Metadata, Note: "an action. We have template copy, under its own auth.RelCopy relation."},
	{Name: "jobs", Status: Metadata, Note: "a derived collection. We have JobStore.ListForTemplate and the Completed jobs section renders it."},

	// Real relationships nothing else scores.
	{
		Name: "schedules", Status: Gap, Phase: "C2 Schedules",
		Note: "the schedules attached to this template. Invisible to this measurement until the related block was classified, because a schedule points AT a template and so appears in no field of one.",
	},
	{
		Name: "notification_templates_started", Status: Gap, Phase: "C3 Notifications",
		Note: "which notification templates fire when a job from this template starts. AWX binds notifications per trigger point, per object.",
	},
	{Name: "notification_templates_success", Status: Gap, Phase: "C3 Notifications", Note: "as started, on success"},
	{Name: "notification_templates_error", Status: Gap, Phase: "C3 Notifications", Note: "as started, on failure. The trigger somebody actually configures first."},
	{
		Name: "extra_credentials", Status: Gap, Phase: "A2 Credential Types",
		Note: "AWX's deprecated pre-3.x alias for the cloud and network credentials on a template, kept for API compatibility. An import must read it as a synonym for credentials rather than as a second relationship, and must not write it. Still a gap after the binding landed, and deliberately so: the relationship it aliases is represented, and reproducing a deprecated alias of it would be adding a second answer to one question. It becomes an import concern rather than an API one.",
	},
	{
		Name: "modified_by", Status: Gap, Phase: "unowned",
		Note: "the twin of created_by, and absent from a job template's summary_fields, so this is the only place it appears. Provenance rather than access, on the object whose edits change what runs.",
	},
	{
		Name: "activity_stream", Status: Gap, Phase: "unowned",
		Note: "AWX offers the audit trail FILTERED to one object. Ours is deployment-wide only: internal/activity has no per-object query, so \"what has been done to this template\" cannot be answered from the template. Cheap, and adjacent to the missing changes field (activity_stream.changes).",
	},
	{
		Name: "webhook_key", Status: Gap, Phase: "D4 Webhook launch",
		Note: "the generated shared secret a sender signs with. Generated per template rather than stored by the operator, which is a key-management surface of its own.",
	},
	{
		Name: "webhook_receiver", Status: Gap, Phase: "D4 Webhook launch",
		Note: "the generated per-template endpoint a provider posts to. The path encodes the service (github here), so the receiver is per template and per provider.",
	},
}
