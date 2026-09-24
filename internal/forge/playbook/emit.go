// Package playbook: writing a translated runbook, and proving it builds.
package playbook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// guardName is the first task of an incomplete runbook: a placeholder
// like the others, first, so even a path that skipped validation would
// fail before any task ran.
const guardName = "forge: incomplete conversion; resolve every blocked finding in the report, then delete this task"

// idUnsafe matches what a runbook id may not hold.
var idUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// listKeys are a runbook's task lists, in the order they run.
var listKeys = []string{"pretasks", "tasks", "posttasks"}

// runbookFile names the n-th runbook converted from stem: its id, so the
// file name carries nothing the playbook's own name held that a file
// system or a terminal would act on.
func runbookFile(stem string, n int, incomplete bool) string {
	name := runbookID(stem, n)
	if incomplete {
		name += ".incomplete"
	}
	return name + ".yaml"
}

// runbookID is the n-th runbook's id: the stem with anything a NATS
// subject token cannot carry replaced.
func runbookID(stem string, n int) string {
	id := strings.Trim(idUnsafe.ReplaceAllString(stem, "-"), "-")
	if id == "" {
		id = "playbook"
	}
	if n > 1 {
		id = fmt.Sprintf("%s-%d", id, n)
	}
	return id
}

// emitted is one runbook's output and its record.
type emitted struct {
	runbook Runbook
	result  RunbookResult
	tasks   []*TaskResult
}

// emitRunbook writes rb, the n-th runbook, and proves it builds.
func (t *translator) emitRunbook(rb *runbookOut, n int, stem string, eval engine.Evaluator) (emitted, error) {
	incomplete := false
	for _, f := range t.findings[rb.first:rb.last] {
		incomplete = incomplete || f.Outcome == OutcomeBlocked
	}
	file := runbookFile(stem, n, incomplete)
	def, lists := t.buildDef(rb, runbookID(stem, n), incomplete)
	doc := &yaml.Node{}
	if err := doc.Encode(def); err != nil {
		return emitted{}, fmt.Errorf("encoding %s: %w", file, err)
	}
	doc.HeadComment = t.header(incomplete)
	for _, key := range listKeys {
		annotate(lookup(doc, key), lists[key], t)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return emitted{}, fmt.Errorf("encoding %s: %w", file, err)
	}
	_ = enc.Close()
	version, err := roundTrip(buf.Bytes(), def, eval)
	if err != nil {
		return emitted{}, fmt.Errorf("the converted runbook %s does not build: %w", file, err)
	}
	out := emitted{
		runbook: Runbook{File: file, ID: def.ID, YAML: buf.Bytes(), Incomplete: incomplete},
		result:  RunbookResult{File: file, ID: def.ID, Hosts: rb.hosts, Runnable: !incomplete, DAGVersion: version},
	}
	for _, p := range rb.plays {
		out.result.Plays = append(out.result.Plays, p.index+1)
	}
	var written yaml.Node
	if err := yaml.Unmarshal(buf.Bytes(), &written); err != nil {
		return emitted{}, err
	}
	for _, key := range listKeys {
		out.tasks = append(out.tasks, t.locate(lookup(written.Content[0], key), lists[key], key, file)...)
	}
	return out, nil
}

// header is the comment at the top of a converted runbook.
func (t *translator) header(incomplete bool) string {
	lines := []string{"# Converted by pleiades forge migrate-playbook from " + termsafe.EscapeLine(t.file) + "."}
	if incomplete {
		lines = append(lines,
			"# INCOMPLETE: some tasks did not convert. Each is a placeholder calling",
			"# ansible.unconverted.*, and the first task stops any run until every",
			"# blocked finding in the report is resolved.")
	}
	return strings.Join(lines, "\n")
}

// buildDef assembles rb's WorkflowDef, and the output tasks behind each of
// its task lists.
func (t *translator) buildDef(rb *runbookOut, id string, incomplete bool) (engine.WorkflowDef, map[string][]*outTask) {
	def := engine.WorkflowDef{ID: id, Hosts: rb.hosts}
	lists := map[string][]*outTask{}
	if len(rb.plays) == 1 {
		p := rb.plays[0]
		def.Name, def.Tags = p.name, engine.TagList(p.tags)
		lists["pretasks"], lists["tasks"], lists["posttasks"] = p.pre, p.main, p.post
	} else {
		var blocks []*outTask
		for _, p := range rb.plays {
			children := append(append(append([]*outTask{}, p.pre...), p.main...), p.post...)
			if len(children) == 0 {
				continue
			}
			name := p.name
			if name == "" {
				name = fmt.Sprintf("play %d", p.index+1)
			}
			blocks = append(blocks, &outTask{name: name, tags: p.tags, block: children, at: p.at})
		}
		lists["tasks"] = blocks
	}
	if incomplete {
		guard := &outTask{name: guardName, fqcn: engine.IncompleteGuard}
		key := "tasks"
		if len(rb.plays) == 1 && len(lists["pretasks"]) > 0 {
			key = "pretasks"
		}
		lists[key] = append([]*outTask{guard}, lists[key]...)
	}
	def.PreTasks, def.Tasks, def.PostTasks = toEngine(lists["pretasks"]), toEngine(lists["tasks"]), toEngine(lists["posttasks"])
	if def.Tasks == nil {
		def.Tasks = []engine.Task{}
	}
	return def, lists
}

// toEngine converts output tasks into engine tasks.
func toEngine(tasks []*outTask) []engine.Task {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]engine.Task, len(tasks))
	for i, o := range tasks {
		out[i] = engine.Task{
			Name: o.name, FQCN: o.fqcn, Params: o.params, Register: o.register,
			CheckMode: engine.CheckModeFlag(o.checkMode), Tags: engine.TagList(o.tags),
			Block: toEngine(o.block), Rescue: toEngine(o.rescue), Always: toEngine(o.always),
		}
		out[i].When = engine.StringList(o.when)
	}
	return out
}

// annotate puts each task's finding IDs and source line on its node as a
// comment, built only from IDs and an escaped position, so no text from
// the playbook can start a line of YAML.
func annotate(seq *yaml.Node, tasks []*outTask, t *translator) {
	seq = deref(seq)
	if seq == nil {
		return
	}
	for i, o := range tasks {
		if i >= len(seq.Content) {
			return
		}
		node := seq.Content[i]
		var parts []string
		if strings.HasPrefix(o.fqcn, placeholderPrefix) {
			parts = append(parts, "not converted")
		}
		if len(o.cites) > 0 {
			parts = append(parts, "see "+strings.Join(o.cites, ", "))
		}
		if o.at.Line > 0 {
			parts = append(parts, "from "+termsafe.EscapeLine(o.at.String()))
		}
		if len(parts) > 0 {
			node.HeadComment = "# forge: " + strings.Join(parts, "; ")
		}
		for _, sub := range []struct {
			key   string
			tasks []*outTask
		}{{"block", o.block}, {"rescue", o.rescue}, {"always", o.always}} {
			annotate(lookup(node, sub.key), sub.tasks, t)
		}
	}
}

// roundTrip builds the written YAML with the engine's own builder and
// checks it is the runbook def says, by building def's JSON too: the two
// versions hash the resolved definition, so equal versions mean the file
// says exactly what was converted.
func roundTrip(written []byte, def engine.WorkflowDef, eval engine.Evaluator) (string, error) {
	fromYAML, err := engine.NewBuilder(eval).BuildFromYAML(written)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(def)
	if err != nil {
		return "", err
	}
	fromJSON, err := engine.NewBuilder(eval).Build(payload)
	if err != nil {
		return "", fmt.Errorf("its JSON form: %w", err)
	}
	if fromYAML.Version != fromJSON.Version {
		return "", fmt.Errorf("the written YAML builds to %s but the conversion meant %s", fromYAML.Version, fromJSON.Version)
	}
	return fromYAML.Version, nil
}
