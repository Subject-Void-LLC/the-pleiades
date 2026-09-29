// Package playbook: what happens to each Ansible task and block keyword.
// Data only; tasks.go applies it.
//
// The drop rule decides every row: a keyword may be dropped only when its
// absence makes the task do less or fail sooner, and anything else blocks
// the task. A keyword missing from this table blocks the task too
// (keyword.unknown), so a new Ansible keyword can never pass as converted.
package playbook

// kwHandling is what happens to one keyword.
type kwHandling int

const (
	// kwSpecial is handled by tasks.go itself (name, when, register, tags,
	// loops, blocks, action forms, check_mode, become_user, failed_when,
	// connection, args).
	kwSpecial kwHandling = iota
	// kwScope is read by the variable index and needs nothing more (vars,
	// collections, listen).
	kwScope
	// kwDrop drops the keyword, raising its code, when its value is not a
	// literal false (a templated value counts as set).
	kwDrop
	// kwBlock blocks the task, raising its code, when its value is not a
	// literal false.
	kwBlock
)

// kwRule is one keyword's row.
type kwRule struct {
	handling kwHandling
	code     Code
}

// taskKeywords are Ansible's task and block keywords.
var taskKeywords = map[string]kwRule{
	"name":         {kwSpecial, ""},
	"when":         {kwSpecial, ""},
	"register":     {kwSpecial, ""},
	"tags":         {kwSpecial, ""},
	"check_mode":   {kwSpecial, ""},
	"loop":         {kwSpecial, ""},
	"loop_control": {kwSpecial, ""},
	"action":       {kwSpecial, ""},
	"args":         {kwSpecial, ""},
	"block":        {kwSpecial, ""},
	"rescue":       {kwSpecial, ""},
	"always":       {kwSpecial, ""},
	"become_user":  {kwSpecial, ""},
	"failed_when":  {kwSpecial, ""},
	"connection":   {kwSpecial, ""},

	"vars":        {kwScope, ""},
	"collections": {kwScope, ""},
	"listen":      {kwScope, ""},

	"notify":             {kwDrop, "keyword.notify"},
	"become":             {kwDrop, "keyword.become"},
	"become_method":      {kwDrop, "keyword.become"},
	"become_flags":       {kwDrop, "keyword.become"},
	"become_exe":         {kwDrop, "keyword.become"},
	"ignore_errors":      {kwDrop, "keyword.ignore_errors"},
	"ignore_unreachable": {kwDrop, "keyword.ignore_unreachable"},
	"changed_when":       {kwDrop, "keyword.changed_when"},
	"diff":               {kwDrop, "keyword.reporting"},
	"debugger":           {kwDrop, "keyword.reporting"},
	"any_errors_fatal":   {kwDrop, "keyword.reporting"},
	"remote_user":        {kwDrop, "play.identity"},
	"port":               {kwDrop, "play.identity"},

	"retries":         {kwBlock, "keyword.retries"},
	"until":           {kwBlock, "keyword.retries"},
	"delay":           {kwBlock, "keyword.retries"},
	"async":           {kwBlock, "keyword.async"},
	"poll":            {kwBlock, "keyword.async"},
	"delegate_to":     {kwSpecial, ""},
	"delegate_facts":  {kwBlock, "keyword.delegate_to"},
	"run_once":        {kwBlock, "keyword.run_once"},
	"throttle":        {kwBlock, "keyword.throttle"},
	"local_action":    {kwSpecial, ""},
	"no_log":          {kwBlock, "keyword.no_log"},
	"environment":     {kwBlock, "keyword.environment"},
	"module_defaults": {kwBlock, "keyword.module_defaults"},
	"timeout":         {kwBlock, "keyword.timeout"},
}

// playKeywords are Ansible's play keywords besides the task keywords a
// play may also carry.
var playKeywords = map[string]kwRule{
	"hosts":        {kwSpecial, ""},
	"tasks":        {kwSpecial, ""},
	"pre_tasks":    {kwSpecial, ""},
	"post_tasks":   {kwSpecial, ""},
	"handlers":     {kwSpecial, ""},
	"gather_facts": {kwSpecial, ""},
	"vars_files":   {kwSpecial, ""},

	"gather_subset":       {kwDrop, "play.facts"},
	"gather_timeout":      {kwDrop, "play.facts"},
	"fact_path":           {kwDrop, "play.facts"},
	"strategy":            {kwDrop, "play.order"},
	"order":               {kwDrop, "play.order"},
	"max_fail_percentage": {kwDrop, "play.order"},
	"force_handlers":      {kwDrop, "play.order"},

	"serial":      {kwBlock, "play.serial"},
	"roles":       {kwBlock, "play.roles"},
	"vars_prompt": {kwBlock, "play.vars_prompt"},
}

// isDelegation reports whether key asks for a task to run somewhere other
// than its target: delegate_to or local_action. A task decides these by its
// own native method (localRun); a block or a play still blocks on them.
func isDelegation(key string) bool {
	return key == "delegate_to" || key == "local_action"
}

// delegationCode is the finding a delegation keyword raises when it blocks.
func delegationCode(key string) Code {
	if key == "local_action" {
		return "keyword.local_action"
	}
	return "keyword.delegate_to"
}
