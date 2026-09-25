// Package playbook: the conversion's entry point and its shared state.
package playbook

import (
	"fmt"
	"io/fs"
	"runtime/debug"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// Options configures a conversion. Its zero value is the default.
type Options struct{}

// findingKey identifies one construct, so each is reported once however
// many tasks it lands in (a loop unrolled into three copies is one loop).
type findingKey struct {
	code Code
	at   Position
}

// translator holds one conversion's state.
type translator struct {
	fsys fs.FS
	// file is the playbook's name inside fsys, as positions name it.
	file string
	ix   *varIndex
	res  *resolver
	// findings are raised in order; seen maps each construct to its ID.
	findings []Finding
	seen     map[findingKey]string
	// registers maps a register name to what its converted task produces.
	registers map[string]produced
	// emitted counts output tasks, against maxEmittedTasks.
	emitted int
	// indexed records the imported files the variable index has read, so
	// each is read once however often it is imported.
	indexed map[string]bool
}

// Translate converts the playbook named name inside src, which should be
// rooted at the playbook's own directory (os.Root's FS, so nothing the
// playbook names can escape it, symlinks included). It returns the
// runbooks and the report; an error means the file could not be read as a
// playbook at all, not that something in it failed to convert.
//
// The native methods come from the collection registry, so the calling
// binary must link the built-in catalog (a blank import of
// internal/catalog); without it every mapped task is blocked as naming a
// method that is not registered.
//
// A panic anywhere inside is turned into an error: the playbook is input
// from someone else, and a crash would take its caller (the CLI today, an
// editor's language server later) down with it.
func Translate(src fs.FS, name string, _ Options) (result Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = Result{}, fmt.Errorf("converting %s: internal error: %v\n%s", termsafe.EscapeLine(name), r, debug.Stack())
		}
	}()
	t := &translator{
		fsys:      src,
		file:      name,
		ix:        newVarIndex(),
		seen:      map[findingKey]string{},
		registers: map[string]produced{},
		indexed:   map[string]bool{},
	}
	t.res = newResolver(t.ix)
	data, err := readBounded(src, name, maxPlaybookBytes)
	if err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", termsafe.EscapeLine(name), err)
	}
	root, err := parseYAML(data, name)
	if err != nil {
		return Result{}, err
	}
	return t.run(root)
}

// raise records a finding for the construct at at, returning its ID. A
// construct already raised with the same code returns its first ID. The
// code must be in codes.go; its outcome comes from there.
func (t *translator) raise(code Code, at Position, task, message string) string {
	doc, ok := codes[code]
	if !ok {
		panic(fmt.Sprintf("finding code %q is not in codes.go", code))
	}
	key := findingKey{code, at}
	if id, seen := t.seen[key]; seen {
		return id
	}
	id := fmt.Sprintf("F%03d", len(t.findings)+1)
	t.seen[key] = id
	t.findings = append(t.findings, Finding{
		ID:      id,
		Code:    code,
		Outcome: doc.Outcome,
		At:      at,
		Task:    termsafe.EscapeLine(task),
		Message: termsafe.EscapeLine(message),
		Native:  doc.Native,
	})
	return id
}

// finding returns the finding with id, for updating its emitted position.
func (t *translator) finding(id string) *Finding {
	for i := range t.findings {
		if t.findings[i].ID == id {
			return &t.findings[i]
		}
	}
	return nil
}
