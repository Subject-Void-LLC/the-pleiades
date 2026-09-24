// Package playbook: the closed set of finding codes. Data only.
//
// A code names one reason a construct did not convert cleanly. It is
// stable, so an editor extension can offer one action per code, and the
// set is closed: a finding can only be raised with a code listed here,
// which is also where its outcome and its native alternative are decided,
// once, rather than at each place it is raised.
package playbook

// Code is a finding's stable machine code, "construct.reason".
type Code string

// CodeDoc is one code's fixed meaning.
type CodeDoc struct {
	// Outcome is what happens to a construct raised with this code.
	Outcome Outcome
	// Summary says, in a sentence, what the code means.
	Summary string
	// Native names what a runbook uses instead, or what will add it.
	Native string
}

// codes is every code a finding may carry.
var codes = map[Code]CodeDoc{
	// Modules and their arguments.
	"module.unmapped":    {OutcomeBlocked, "No native method does what this module does.", "Write the task by hand with a native method, or run the playbook unchanged as a playbook job."},
	"module.manual":      {OutcomeBlocked, "This module needs a person: it has no faithful mechanical conversion.", ""},
	"module.semantics":   {OutcomeReview, "The native method does what the module does, except in the way the finding says.", ""},
	"module.ambiguous":   {OutcomeBlocked, "The task names more than one module, or none.", "One module per task."},
	"args.unmapped":      {OutcomeBlocked, "An argument the native method has no equivalent for.", "Drop it if it changes nothing, or write the task by hand."},
	"args.unparsable":    {OutcomeBlocked, "Free-form arguments this converter cannot read unambiguously.", "Write the arguments as a map."},
	"args.value":         {OutcomeBlocked, "A value whose meaning differs between Ansible's YAML 1.1 and the runbook's YAML 1.2, or that the native parameter cannot take.", "Quote the value in the playbook so its meaning is plain."},
	"args.ignored":       {OutcomeInfo, "An argument with no effect on the native call was dropped: Ansible ignores it for this state, or the native method has nothing for it to choose.", ""},
	"args.list_unrolled": {OutcomeReview, "A list of names became one task per name, so the items no longer succeed or fail together.", ""},
	"netconf.target":     {OutcomeBlocked, "net.netconf.config's datastore parameter is named target, which the engine reads as the device to run against.", "Leave target out (running is the default) until the device selector moves out of params."},
	// Templates and variables.
	"template.resolved":    {OutcomeInfo, "A template read a variable with exactly one literal value in the playbook, and was replaced by that value.", ""},
	"template.unresolved":  {OutcomeBlocked, "A template reads a variable with no single literal value here: set at run time, defined more than once, a fact, or undefined.", "Write the value, or wait for templated params."},
	"template.unsupported": {OutcomeBlocked, "A template uses Jinja this converter cannot evaluate (a filter, a test or a statement).", "Write the resulting value."},
	"template.fact":        {OutcomeBlocked, "A template reads a fact or a magic variable (inventory_hostname, ansible_*), known only on a host at run time.", "Put per-device values in inventory properties."},
	"template.secret":      {OutcomeBlocked, "The variable's name or value looks secret, so it is not copied into the runbook.", "Bind a credential to the template instead of writing the value."},
	"state.computed":       {OutcomeBlocked, "The task's state comes from a variable with no single value, so which native method it maps to is decided only at run time.", "Choose the method that matches the state you mean."},
	"vault.value":          {OutcomeBlocked, "A vault-encrypted value. It is never read or copied.", "Bind a credential to the template instead."},
	"vars.files":           {OutcomeReview, "A vars_files entry could not be read inside the playbook's directory, so its variables are unknown here.", ""},
	"vars.inventory":       {OutcomeReview, "group_vars or host_vars exist beside the playbook. They are not read, and they differ per host.", "Put per-device values in inventory properties."},
	"set_fact.resolved":    {OutcomeInfo, "A set_fact with only literal values was folded into the variables it defines.", ""},
	"set_fact.runtime":     {OutcomeBlocked, "A set_fact that is conditional, looped or templated sets its value only at run time.", ""},
	// Loops.
	"loop.unrolled": {OutcomeInfo, "A loop over a list known here became one task per item.", ""},
	"loop.runtime":  {OutcomeBlocked, "A loop over a list known only at run time.", "Write one task per item."},
	"loop.register": {OutcomeBlocked, "A looped task registers its result, whose shape differs from one task's.", ""},
	"loop.control":  {OutcomeBlocked, "A loop_control option this converter does not reproduce.", ""},
	"loop.too_long": {OutcomeBlocked, "A loop longer than this converter unrolls.", ""},
	// Conditions.
	"when.register":    {OutcomeReview, "A condition reads a registered result. A native condition is evaluated once for the task, across every device it targets, not once per host.", ""},
	"when.unsupported": {OutcomeBlocked, "A condition uses Jinja this converter cannot translate to CEL.", "Write the condition as when_cel."},
	"when.fact":        {OutcomeBlocked, "A condition reads a fact or a magic variable (inventory_hostname, ansible_*).", ""},
	// Blocks.
	"block.rescue": {OutcomeReview, "rescue: converted, but the engine does not run rescue tasks yet: a failure in the block ends the run.", ""},
	"block.always": {OutcomeReview, "always: converted, but the engine does not run always tasks yet.", ""},
	// Task keywords.
	"keyword.notify":             {OutcomeReview, "notify dropped: the handler will not run.", "Run the handler's task explicitly after the change, guarded by when."},
	"keyword.become":             {OutcomeReview, "become dropped: the task runs as the connecting user and fails if it needs root.", "A method that needs root says so in its manifest; use a credential with the privilege."},
	"keyword.become_user":        {OutcomeBlocked, "become_user names a user other than root.", ""},
	"keyword.ignore_errors":      {OutcomeReview, "ignore_errors dropped: a failure ends the run instead of continuing.", ""},
	"keyword.changed_when":       {OutcomeInfo, "changed_when dropped: only how the task reports change differs.", ""},
	"keyword.failed_when":        {OutcomeBlocked, "failed_when would make the task fail where the native one passes.", "Check the result in a later task's when, or wait for failed_when."},
	"keyword.retries":            {OutcomeBlocked, "retries/until/delay: the native task runs once and passes without checking until.", ""},
	"keyword.async":              {OutcomeBlocked, "async/poll has no native equivalent.", ""},
	"keyword.delegate_to":        {OutcomeBlocked, "delegate_to would run the task on another host.", ""},
	"keyword.run_once":           {OutcomeBlocked, "run_once: the native task would run on every device.", ""},
	"keyword.throttle":           {OutcomeBlocked, "throttle has no native equivalent.", ""},
	"keyword.local_action":       {OutcomeBlocked, "local_action runs on the controller, not the device.", ""},
	"keyword.connection":         {OutcomeReview, "connection dropped: the transport comes from the device's capabilities.", ""},
	"keyword.no_log":             {OutcomeBlocked, "no_log hides output; dropping it could show a secret.", "Use register_mask or secret_mask on the fields that hold the secret."},
	"keyword.environment":        {OutcomeBlocked, "environment would change what the command sees.", ""},
	"keyword.module_defaults":    {OutcomeBlocked, "module_defaults would change the call's arguments.", ""},
	"keyword.check_mode_false":   {OutcomeReview, "check_mode: false dropped: the task now respects a check instead of running for real inside one.", ""},
	"keyword.check_mode":         {OutcomeBlocked, "check_mode: true on a method that cannot be checked.", ""},
	"keyword.timeout":            {OutcomeBlocked, "A task timeout has no per-task native equivalent.", ""},
	"keyword.ignore_unreachable": {OutcomeReview, "ignore_unreachable dropped: an unreachable device fails the run.", ""},
	"keyword.tags":               {OutcomeBlocked, "tags that are templated, or that name all, tagged or untagged, which a runbook task cannot carry.", "Write the tag names."},
	"keyword.reporting":          {OutcomeInfo, "A reporting-only keyword (diff, debugger) was dropped.", ""},
	"keyword.unknown":            {OutcomeBlocked, "A key this converter does not know.", ""},
	// Engine-keyword tasks.
	"debug.dropped":        {OutcomeInfo, "A debug task only prints, so it was dropped.", ""},
	"meta.dropped":         {OutcomeInfo, "A meta task with nothing to run (flush_handlers, noop) was dropped.", ""},
	"meta.unsupported":     {OutcomeBlocked, "A meta task that changes how the run proceeds.", ""},
	"include.tasks":        {OutcomeBlocked, "include_tasks decides at run time what it includes.", "import_tasks, which is static."},
	"include.role":         {OutcomeBlocked, "Roles convert through a Galaxy collection migration, not this one.", ""},
	"import.playbook":      {OutcomeBlocked, "import_playbook: convert the imported playbook on its own.", ""},
	"import.tasks_missing": {OutcomeBlocked, "import_tasks names a file that cannot be read inside the playbook's directory.", ""},
	"handler.dropped":      {OutcomeReview, "A handler was not converted, since nothing native notifies it.", "Run its task explicitly where it was notified."},
	// Plays.
	"play.hosts_pattern": {OutcomeBlocked, "hosts: is a pattern or a list, which a runbook's single host or tag cannot express.", "Tag the devices and name the tag."},
	"play.hosts_all":     {OutcomeReview, "hosts: all needs an inventory tag named all.", ""},
	"play.hosts_local":   {OutcomeReview, "hosts: localhost has no native equivalent; the tasks run with no target.", ""},
	"play.split":         {OutcomeReview, "This play's hosts differ from the play before it, so it starts a new runbook; run them in order.", ""},
	"play.serial":        {OutcomeBlocked, "serial: the runbook would change every device at once.", ""},
	"play.order":         {OutcomeReview, "A play-level control (strategy, order, max_fail_percentage, force_handlers) was dropped: the run stops at the first failure.", ""},
	"play.facts":         {OutcomeInfo, "gather_facts dropped: facts are not gathered.", "facts.gather, where the device supports it."},
	"play.identity":      {OutcomeReview, "remote_user, port or connection dropped: identity and transport come from the inventory and credential store.", ""},
	"play.roles":         {OutcomeBlocked, "roles: convert through a Galaxy collection migration.", ""},
	"play.vars_prompt":   {OutcomeBlocked, "vars_prompt asks at run time.", "A survey on the job template."},
	"play.malformed":     {OutcomeBlocked, "A play that is not a map, or has no tasks.", ""},
	// Documents.
	"yaml.duplicate_key": {OutcomeBlocked, "A map repeats a key; Ansible keeps the last and warns.", ""},
	"yaml.limit":         {OutcomeBlocked, "The playbook exceeds a size, depth or alias limit.", ""},
}
