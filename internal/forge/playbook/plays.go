// Package playbook: translating plays, and grouping them into runbooks.
package playbook

import (
	"fmt"
	"io/fs"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// playOut is one translated play.
type playOut struct {
	index           int
	name            string
	tags            []string
	pre, main, post []*outTask
	at              Position
}

// runbookOut is one output runbook: consecutive plays that share hosts.
type runbookOut struct {
	hosts string
	plays []*playOut
	// first and last bound the findings raised while translating its
	// plays; any blocked one among them makes the runbook incomplete.
	first, last int
}

// plainHost matches a hosts value a runbook's hosts can carry: one
// inventory name or tag, not a pattern.
var plainHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// playHosts reads a play's hosts: the runbook value, and a key that keeps
// a play whose hosts cannot be expressed from merging with its neighbors.
func (t *translator) playHosts(play *yaml.Node, index int) (hosts, key string, at Position) {
	v := deref(lookup(play, "hosts"))
	at = nodePos(v, t.file)
	if v == nil {
		t.raise("play.malformed", nodePos(play, t.file), "", "the play has no hosts")
		return "", fmt.Sprintf("\x00play%d", index), at
	}
	switch {
	case v.Kind != yaml.ScalarNode || !plainHost.MatchString(v.Value):
		t.raise("play.hosts_pattern", at, "", "hosts is a pattern, a list or a template")
		return "", fmt.Sprintf("\x00play%d", index), at
	case v.Value == "all":
		t.raise("play.hosts_all", at, "", "hosts: all")
	case v.Value == "localhost" || v.Value == "127.0.0.1":
		t.raise("play.hosts_local", at, "", "hosts: "+v.Value)
		return "", "\x00local", at
	}
	return v.Value, v.Value, at
}

// translatePlay translates one play's tasks under the play's own
// keywords.
func (t *translator) translatePlay(play *yaml.Node, index int, runbook string) *playOut {
	out := &playOut{index: index, at: nodePos(play, t.file)}
	ctx := taskCtx{runbook: runbook}
	entries, repeated := mapEntries(play)
	for _, r := range repeated {
		t.raise("yaml.duplicate_key", nodePos(r.keyAt, t.file), "", "key "+r.key+" is repeated in a play")
	}
	facts := true
	for _, e := range entries {
		at := nodePos(e.keyAt, t.file)
		v := deref(e.value)
		if rule, ok := playKeywords[e.key]; ok {
			switch {
			case e.key == "gather_facts":
				facts = !literalFalse(e.value)
			case rule.handling == kwDrop && !literalFalse(e.value):
				t.raise(rule.code, at, "", e.key+" dropped")
			case rule.handling == kwBlock && !literalFalse(e.value):
				t.raise(rule.code, at, "", e.key+" has no native equivalent")
			}
			continue
		}
		rule, ok := taskKeywords[e.key]
		switch {
		case e.key == "name":
			if v != nil {
				out.name = v.Value
			}
		case e.key == "collections":
			if v != nil && v.Kind == yaml.SequenceNode {
				for _, c := range v.Content {
					ctx.collections = append(ctx.collections, deref(c).Value)
				}
			}
		case e.key == "tags":
			if tags, ok := readTags(v); ok {
				out.tags = tags
			} else {
				ctx.blockers = append(ctx.blockers, t.raise("keyword.tags", at, "", "the play's tags are templated or name a filter-only tag"))
			}
		case e.key == "check_mode":
			if v != nil && readScalar(v).kind == "bool" && readScalar(v).b {
				ctx.checkMode = true
			} else if !literalFalse(e.value) {
				ctx.blockers = append(ctx.blockers, t.raise("keyword.check_mode", at, "", "check_mode on a play is not a literal true"))
			}
		case ok && rule.handling == kwScope:
		case ok && rule.handling == kwDrop:
			if !literalFalse(e.value) {
				t.raise(rule.code, at, "", e.key+" dropped from the play and every task in it")
			}
		case isDelegation(e.key):
			// As on a block: a play's delegation applies to tasks with
			// different methods, so it still blocks them.
			if !literalFalse(e.value) {
				ctx.blockers = append(ctx.blockers, t.raise(delegationCode(e.key), at, "", e.key+" on a play applies to every task in it"))
			}
		case ok && rule.handling == kwBlock:
			if !literalFalse(e.value) {
				ctx.blockers = append(ctx.blockers, t.raise(rule.code, at, "", e.key+" on a play applies to every task in it"))
			}
		default:
			t.raise("keyword.unknown", at, "", "the play carries "+e.key)
		}
	}
	if facts {
		t.raise("play.facts", out.at, "", "facts are not gathered")
	}
	out.pre = t.translateTasks(lookup(play, "pre_tasks"), ctx)
	out.main = t.translateTasks(lookup(play, "tasks"), ctx)
	out.post = t.translateTasks(lookup(play, "post_tasks"), ctx)
	if handlers := deref(lookup(play, "handlers")); handlers != nil {
		for _, h := range handlers.Content {
			t.raise("handler.dropped", nodePos(h, t.file), taskName(h), "handler not converted")
		}
	}
	return out
}

// translatePlays translates root's plays into runbooks.
func (t *translator) translatePlays(root *yaml.Node, stem string) []*runbookOut {
	var plays []*yaml.Node
	for _, item := range root.Content {
		item = deref(item)
		switch {
		case item != nil && item.Kind == yaml.MappingNode && lookup(item, "import_playbook") != nil:
			t.raise("import.playbook", nodePos(item, t.file), "", "import_playbook: convert the imported playbook on its own")
		case item == nil || item.Kind != yaml.MappingNode:
			t.raise("play.malformed", nodePos(item, t.file), "", "a play that is not a map")
		default:
			plays = append(plays, item)
		}
	}
	t.indexPlaybook(plays)
	for _, dir := range []string{"group_vars", "host_vars"} {
		if _, err := fs.Stat(t.fsys, dir); err == nil {
			t.raise("vars.inventory", Position{File: dir}, "", dir+" is not read")
		}
	}
	var runbooks []*runbookOut
	lastKey := ""
	for i, play := range plays {
		first := len(t.findings)
		hosts, key, hostsAt := t.playHosts(play, i)
		if len(runbooks) == 0 || key != lastKey {
			if len(runbooks) > 0 {
				t.raise("play.split", hostsAt, "", "this play's hosts start a new runbook")
			}
			runbooks = append(runbooks, &runbookOut{hosts: hosts, first: first})
		}
		lastKey = key
		rb := runbooks[len(runbooks)-1]
		rb.plays = append(rb.plays, t.translatePlay(play, i, runbookFile(stem, len(runbooks), false)))
		rb.last = len(t.findings)
	}
	return runbooks
}
