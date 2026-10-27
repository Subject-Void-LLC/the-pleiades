// Template files (file.template): a task parameter declared
// collection.ParamFormatTemplateFile names a file in the project whose text is
// a template. The engine reads it, renders it through the one renderer every
// other parameter uses, and hands the method the RENDERED text as the
// parameter the method calls its content. A Collection never sees a template
// or a renderer, which it may not import (PLAN.md Section 25).
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// maxTemplateFileBytes bounds a template file, as file.copy bounds a src file.
const maxTemplateFileBytes = 64 << 20

// templateContentParam is the parameter a rendered template file lands in.
const templateContentParam = "content"

// TemplateFileReader reads one file of the run's project, the way
// sdk.ProjectFiles does: relative, inside the project, no symbolic link, never
// .pleiades, a regular file of at most max bytes.
type TemplateFileReader func(name string, max int64) ([]byte, error)

// WithTemplateFiles hands the Executor the reader a template file is read
// through. Only the command line holds a project, so a Runner passes none and
// such a task is refused there, naming content as the way.
func WithTemplateFiles(read TemplateFileReader) ExecutorOption {
	return func(x *Executor) { x.templateFiles = read }
}

// ProjectTemplateFiles returns the reader for the project in dir, built on the
// same guarded reader a method's sdk.ProjectFiles is.
func ProjectTemplateFiles(dir string, guarded ...string) TemplateFileReader {
	return func(name string, max int64) ([]byte, error) {
		rc, err := WithProjectFiles(NewDeviceRunbookContext, dir, guarded...)(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		files, ok := rc.(sdk.ProjectFiles)
		if !ok {
			return nil, sdk.ErrNoProjectFiles
		}
		return files.ReadProjectFile(name, max)
	}
}

// templateFileParam returns the name of the parameter fqcn declares as a
// template file, or "" when it declares none.
func templateFileParam(fqcn string) string {
	d, ok := collection.Lookup(fqcn)
	if !ok {
		return ""
	}
	for _, p := range d.Manifest.Doc.Params {
		if p.Format == collection.ParamFormatTemplateFile {
			return p.Name
		}
	}
	return ""
}

// renderTemplateFile removes task's template-file parameter and returns its
// rendered text, which the caller sets as the content parameter AFTER the other
// parameters render, so the text is never rendered a second time (a rendered
// value holding "{{" must stay literal). It returns task unchanged and false
// when the task declares or sets none. The copy shares everything but Params, so the DAG's own task
// stays as authored.
//
// It fails closed: a name that is itself templated (a path must be written, not
// computed), a task that also sets content, a run with no project or renderer,
// an expression reading a root a parameter may not, and an undefined variable
// all refuse the task before anything is sent. The rendered text is never
// logged here; it can hold an injected value, and it travels only to the
// method, which records a checksum of it and never the text.
func (r *run) renderTemplateFile(task *Task, ctx map[string]any) (*Task, string, bool, error) {
	name := templateFileParam(task.FQCN)
	if name == "" {
		return task, "", false, nil
	}
	raw, set := task.Params[name]
	if !set || raw == nil {
		return task, "", false, nil
	}
	path, ok := raw.(string)
	if !ok || path == "" {
		return nil, "", false, fmt.Errorf("parameter %s must name a file in the project", name)
	}
	if strings.Contains(path, "{{") {
		return nil, "", false, fmt.Errorf("parameter %s names a template file and must be written out, not computed: an expression in it could pick which project file is read", name)
	}
	if _, both := task.Params[templateContentParam]; both {
		return nil, "", false, fmt.Errorf("set %s or %s, not both: each says what the file holds", templateContentParam, name)
	}
	if r.x.templateFiles == nil {
		return nil, "", false, fmt.Errorf("%s names a file in the project, which only the command line (pleiades run, pleiades adhoc) can read; a Walk-tier Runner is a per-task container that cannot see the project, so pass the template text as %s there", name, templateContentParam)
	}
	if r.x.renderer == nil {
		return nil, "", false, errors.New("this run has no template renderer, so a template file cannot be rendered")
	}
	data, err := r.x.templateFiles(path, maxTemplateFileBytes)
	if err != nil {
		if errors.Is(err, sdk.ErrNoProjectFiles) {
			return nil, "", false, fmt.Errorf("%s names a file in the project, and this run holds no project", name)
		}
		return nil, "", false, fmt.Errorf("parameter %s: %w", name, err)
	}
	text, err := renderTemplateText(r.x.renderer, string(data), ctx, task)
	if err != nil {
		return nil, "", false, fmt.Errorf("parameter %s: template %s: %w", name, path, err)
	}
	params := make(map[string]any, len(task.Params))
	for k, v := range task.Params {
		if k != name {
			params[k] = v
		}
	}
	out := *task
	out.Params = params
	return &out, text, true, nil
}

// renderTemplateText compiles and renders one template body, holding it to the
// roots a parameter may read. An undefined variable is the renderer's own
// error, so a misspelled name never writes an empty value into a file.
func renderTemplateText(eng render.Engine, body string, ctx map[string]any, task *Task) (string, error) {
	tmpl, err := eng.Compile(body)
	if err != nil {
		return "", err
	}
	if problems := TaskTemplateProblems(tmpl, "", task); len(problems) > 0 {
		return "", errors.New(strings.Join(problems, "; "))
	}
	return tmpl.Render(ctx)
}

// withContent returns a copy of params with the content parameter set to text.
func withContent(params map[string]any, text string) map[string]any {
	out := make(map[string]any, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	out[templateContentParam] = text
	return out
}
