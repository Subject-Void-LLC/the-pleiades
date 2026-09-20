// This file exposes Templates over the API: the saved, reusable
// definitions of what this platform runs, where it runs, and how.
//
// It is deliberately the administration half only. Creating, editing,
// copying and deleting a template live here under template:read and
// template:write; launching one lives in dispatcher.go under
// runbook:execute, beside every other dispatch path. That split is the
// scope vocabulary made structural: an operator who may run what somebody
// else saved does not thereby acquire the ability to change what it does,
// and an author who may change it does not thereby acquire the right to run
// it against production.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// TemplateStore is the slice of internal/launch's store the administration
// handlers need.
//
// It is the whole port today, and that is honest rather than lazy: this
// handler is the surface that administers templates, so every method is one
// it calls. dispatcher.go takes two narrower ports off the same concrete
// store precisely because launching must not be able to do these things.
type TemplateStore interface {
	Create(ctx context.Context, tmpl launch.Template) (launch.Template, error)
	Get(ctx context.Context, id int) (launch.Template, error)
	List(ctx context.Context, q launch.Query) ([]launch.Template, error)
	Update(ctx context.Context, tmpl launch.Template) error
	Delete(ctx context.Context, id int) error
	SavedConfigs(ctx context.Context, templateID int) ([]launch.SavedConfig, error)
	SaveConfig(ctx context.Context, cfg launch.SavedConfig) (launch.SavedConfig, error)
}

// TemplateHandler serves the Template resource.
type TemplateHandler struct {
	templates TemplateStore
	logger    *slog.Logger
}

// NewTemplateHandler builds the handlers over templates. A nil logger falls
// back to the process default.
func NewTemplateHandler(templates TemplateStore, logger *slog.Logger) *TemplateHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &TemplateHandler{templates: templates, logger: logger}
}

// questionDTO is the wire projection of one survey question.
type questionDTO struct {
	Variable string   `json:"variable"`
	Label    string   `json:"label,omitempty"`
	Help     string   `json:"help,omitempty"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Default  string   `json:"default,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Min      int      `json:"min,omitempty"`
	Max      int      `json:"max,omitempty"`

	// AllowProgramContent is the template author's half of the two-gate
	// rule on a file question. It is projected and accepted, so a client
	// can read and set it, and it grants nothing on its own: the
	// deployment's half is an environment variable the Controller reads at
	// startup and is deliberately not on the wire.
	AllowProgramContent bool `json:"allow_program_content,omitempty"`
}

// surveyDTO is a template's survey: whether it prompts, and what it asks.
type surveyDTO struct {
	Enabled   bool          `json:"enabled"`
	Questions []questionDTO `json:"questions"`
}

// templateDTO is the wire projection of one template.
//
// It carries the inventory's and organization's names beside their ids for
// the reason FAILURE_PATTERNS.md #107 records: a response that renders only
// a foreign key makes the reader do the join, and a client rendering a list
// of templates would have to fetch every inventory to label a column.
type templateDTO struct {
	LinkSet

	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Kind is the registry key, KindLabel what a reader sees. Both, because
	// a client filtering on kind needs the key and a client rendering it
	// needs the label, and deriving one from the other client-side would
	// mean every client carrying its own copy of the kind vocabulary.
	Kind      string `json:"kind"`
	KindLabel string `json:"kind_label,omitempty"`

	Definition string `json:"definition"`

	Inventory     int    `json:"inventory"`
	InventoryName string `json:"inventory_name,omitempty"`

	Organization     int    `json:"organization"`
	OrganizationName string `json:"organization_name,omitempty"`

	Defaults map[string]any `json:"defaults,omitempty"`

	// Prompts names the fields a launch may override. Everything else is
	// locked to what Defaults says, which is what a launch form has to know
	// before it renders a single control.
	Prompts []string `json:"prompts"`

	// Survey is populated on a single-template read and omitted from a
	// listing, matching inventoryDTO's treatment of its own membership: a
	// list of twenty templates should not carry two hundred questions no
	// column renders.
	Survey *surveyDTO `json:"survey,omitempty"`

	AllowSimultaneous    bool     `json:"allow_simultaneous"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
}

type templateListDTO struct {
	LinkSet
	Templates []templateDTO `json:"templates"`
}

// templateWriteDTO is the request body create, update and copy accept.
//
// Kind, Definition and Inventory are read on create and ignored on update,
// which the store enforces rather than trusts: re-pointing a template at
// different code or a different fleet, while it keeps its name, its grants
// and its job history, is how a reviewed thing quietly becomes an
// unreviewed one. Copy is what makes changing them cheap.
type templateWriteDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	Kind       string `json:"kind"`
	Definition string `json:"definition"`
	Inventory  int    `json:"inventory"`

	Defaults map[string]any `json:"defaults"`
	Prompts  []string       `json:"prompts"`

	Survey *surveyDTO `json:"survey"`

	AllowSimultaneous bool `json:"allow_simultaneous"`

	// RequiredCapabilities is a plan-time hint recorded with the template,
	// not something this API computes.
	//
	// Computing it means compiling the definition, and which source
	// resolves a definition is the kind's business rather than this
	// handler's: a handler that compiled would have to know a runbook id
	// from a playbook path, which is the type switch on kind the open
	// registry exists to prevent. A caller that wants the real set reads
	// GET /runbooks/{id}, which already reports it. The executor acquires
	// capabilities for real at run time regardless, so a stale or absent
	// hint costs a worse error message, never a wrong dispatch.
	RequiredCapabilities []string `json:"required_capabilities"`
}

// savedConfigDTO is one stored bundle of launch-time overrides.
//
// Answers arrive here already redacted: a password answer reads as the
// marker rather than its value. Encryption at rest and redaction on the
// wire are separate controls for separate exposures, and a caller who may
// administer a template is not thereby entitled to read the vault token an
// operator typed into it last week.
type savedConfigDTO struct {
	LinkSet

	ID       int            `json:"id"`
	Template int            `json:"template"`
	Name     string         `json:"name,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
	Answers  map[string]any `json:"answers,omitempty"`
}

type savedConfigListDTO struct {
	LinkSet
	Configs []savedConfigDTO `json:"configs"`
}

// savedConfigWriteDTO is the body that stores a named configuration.
type savedConfigWriteDTO struct {
	Name    string         `json:"name"`
	Fields  map[string]any `json:"fields"`
	Answers map[string]any `json:"answers"`
}

func toSurveyDTO(s launch.Survey) *surveyDTO {
	dto := &surveyDTO{Enabled: s.Enabled, Questions: make([]questionDTO, 0, len(s.Questions))}
	for _, q := range s.Questions {
		dto.Questions = append(dto.Questions, questionDTO{
			Variable: q.Variable,
			Label:    q.Label,
			Help:     q.Help,
			Type:     string(q.Type),
			Required: q.Required,
			Default:  q.Default,
			Choices:  q.Choices,
			Min:      q.Min,
			Max:      q.Max,

			AllowProgramContent: q.AllowProgramContent,
		})
	}
	return dto
}

func fromSurveyDTO(dto *surveyDTO) launch.Survey {
	if dto == nil {
		return launch.Survey{}
	}
	survey := launch.Survey{Enabled: dto.Enabled, Questions: make([]launch.Question, 0, len(dto.Questions))}
	for _, q := range dto.Questions {
		survey.Questions = append(survey.Questions, launch.Question{
			Variable: q.Variable,
			Label:    q.Label,
			Help:     q.Help,
			Type:     launch.QuestionType(q.Type),
			Required: q.Required,
			Default:  q.Default,
			Choices:  q.Choices,
			Min:      q.Min,
			Max:      q.Max,

			AllowProgramContent: q.AllowProgramContent,
		})
	}
	return survey
}

// toTemplateDTO projects a template onto the wire.
//
// withSurvey is false for a listing. The kind's label is looked up rather
// than stored, so a descriptor that renames itself renames every template
// at once; a kind no longer registered leaves the label empty rather than
// inventing one, which is the same posture Template.Descriptor takes.
func toTemplateDTO(tmpl launch.Template, withSurvey bool) templateDTO {
	dto := templateDTO{
		ID:                   tmpl.ID,
		Name:                 tmpl.Name,
		Description:          tmpl.Description,
		Kind:                 tmpl.KindName,
		Definition:           tmpl.Definition,
		Inventory:            tmpl.InventoryID,
		InventoryName:        tmpl.InventoryName,
		Organization:         tmpl.OrganizationID,
		OrganizationName:     tmpl.OrganizationName,
		Defaults:             tmpl.Defaults,
		AllowSimultaneous:    tmpl.AllowSimultaneous,
		RequiredCapabilities: tmpl.RequiredCaps,
	}
	if d, err := tmpl.Descriptor(); err == nil {
		dto.KindLabel = d.Label
	}

	// Initialized rather than left nil, so a template that opens no field
	// carries [] instead of null. A client reading null would have to guess
	// whether it meant "nothing is promptable" or "this response does not
	// say", and those are the two answers a launch form must not confuse.
	prompts := tmpl.Prompts
	if prompts == nil {
		prompts = []string{}
	}
	dto.Prompts = prompts

	if withSurvey {
		dto.Survey = toSurveyDTO(tmpl.Survey)
	}
	return dto
}

// toSavedConfigDTO projects a stored configuration, redacting every secret
// answer the survey declares.
func toSavedConfigDTO(cfg launch.SavedConfig, survey launch.Survey) savedConfigDTO {
	redacted := cfg.Redact(survey)
	return savedConfigDTO{
		ID:       redacted.ID,
		Template: redacted.TemplateID,
		Name:     redacted.Name,
		Fields:   redacted.Fields,
		Answers:  redacted.Answers,
	}
}

// List serves a page of templates.
func (h *TemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	q := launch.Query{Search: r.URL.Query().Get("q")}

	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err := strconv.Atoi(raw)
		if err != nil || after < 0 {
			RespondError(w, r, http.StatusBadRequest, "after must be a non-negative integer")
			return
		}
		q.After = after
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			RespondError(w, r, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		q.Limit = limit
	}
	if raw := r.URL.Query().Get("organization"); raw != "" {
		organization, err := strconv.Atoi(raw)
		if err != nil || organization < 1 {
			RespondError(w, r, http.StatusBadRequest, "organization must be a positive integer")
			return
		}
		q.OrganizationIDs = []int{organization}
	}

	templates, err := h.templates.List(r.Context(), q)
	if err != nil {
		h.respondStoreError(w, r, "list", 0, err)
		return
	}

	dto := templateListDTO{Templates: make([]templateDTO, 0, len(templates))}
	for _, tmpl := range templates {
		dto.Templates = append(dto.Templates, toTemplateDTO(tmpl, false))
	}
	Respond(w, r, http.StatusOK, &dto)
}

// Get serves one template, with its survey.
func (h *TemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}

	tmpl, err := h.templates.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}

	dto := toTemplateDTO(tmpl, true)
	Respond(w, r, http.StatusOK, &dto)
}

// Create persists a new template.
func (h *TemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body templateWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	created, err := h.templates.Create(r.Context(), launch.Template{
		Name:              body.Name,
		Description:       body.Description,
		KindName:          body.Kind,
		Definition:        body.Definition,
		InventoryID:       body.Inventory,
		Defaults:          launch.Fields(body.Defaults),
		Prompts:           body.Prompts,
		Survey:            fromSurveyDTO(body.Survey),
		AllowSimultaneous: body.AllowSimultaneous,
		RequiredCaps:      body.RequiredCapabilities,
		// OrganizationID is deliberately not read off the body. The store
		// derives it from the named inventory's required organization edge,
		// which is what makes a template's tenancy something a submitter
		// cannot assert: one who could set it directly could tag their jobs
		// with somebody else's tenant.
	})
	if err != nil {
		h.respondStoreError(w, r, "create", 0, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/templates/"+strconv.Itoa(created.ID))
	dto := toTemplateDTO(created, true)
	Respond(w, r, http.StatusCreated, &dto)
}

// Update replaces a template's editable half.
func (h *TemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}

	var body templateWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	// Read first, so an update to a missing template is a 404 rather than a
	// store error. The kind, definition and inventory are carried forward
	// by the store from what is stored, whatever the body says.
	if _, err := h.templates.Get(r.Context(), id); err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}

	if err := h.templates.Update(r.Context(), launch.Template{
		ID:                id,
		Name:              body.Name,
		Description:       body.Description,
		Defaults:          launch.Fields(body.Defaults),
		Prompts:           body.Prompts,
		Survey:            fromSurveyDTO(body.Survey),
		AllowSimultaneous: body.AllowSimultaneous,
		RequiredCaps:      body.RequiredCapabilities,
	}); err != nil {
		h.respondStoreError(w, r, "update", id, err)
		return
	}

	updated, err := h.templates.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}
	dto := toTemplateDTO(updated, true)
	Respond(w, r, http.StatusOK, &dto)
}

// SetSurvey serves PUT /templates/{id}/survey.
//
// It replaces the template's survey and nothing else. The store's own
// Update writes the whole template, so this reads the stored one first and
// carries its name, description, defaults, prompts and flags forward, which
// is what keeps a survey edit from blanking the fields beside it. Every
// rule the store enforces on a full update still applies, because it is the
// same update: a question writing to no variable, two questions writing to
// one, a choice question offering nothing, a password carrying a default.
//
// The questions are stored in the order the body gives them. A survey's
// order is authored -- a question that only makes sense after another has
// been answered has to render after it -- so a client that reorders the
// array has reordered the form, and that is the only reorder operation
// there is.
func (h *TemplateHandler) SetSurvey(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}

	var body templateSurveyDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	stored, err := h.templates.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}

	stored.Survey = fromSurveyDTO(body.Survey)
	if err := h.templates.Update(r.Context(), stored); err != nil {
		h.respondStoreError(w, r, "update", id, err)
		return
	}

	updated, err := h.templates.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}
	dto := toTemplateDTO(updated, true)
	Respond(w, r, http.StatusOK, &dto)
}

// templateSurveyDTO is the body PUT /templates/{id}/survey accepts: the new
// survey, on its own.
//
// A nil survey is a survey with no questions and disabled, which
// fromSurveyDTO already returns, so an empty body clears the survey rather
// than being refused. That is the honest reading of "replace the survey
// with this" and it is how a template's last question gets removed.
type templateSurveyDTO struct {
	Survey *surveyDTO `json:"survey"`
}

// Delete removes a template.
func (h *TemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}
	if err := h.templates.Delete(r.Context(), id); err != nil {
		h.respondStoreError(w, r, "delete", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Copy duplicates a template under a new name.
//
// It exists because varying a template is how people actually work:
// duplicating one to test a change without touching the production copy,
// and because the kind, definition and inventory are not updatable, copying
// is the supported way to point a saved definition at something else.
//
// What it does NOT copy is the saved launch configurations. Those are one
// operator's answers, secrets among them, and duplicating them would move a
// stored password onto an object with its own separate access grants. The
// survey travels, because the questions are part of how the template runs;
// the answers do not.
func (h *TemplateHandler) Copy(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}

	// The body is optional: a copy with no name gets a derived one, so
	// duplicating a template is one request with nothing to compose.
	var body templateWriteDTO
	if r.ContentLength > 0 && !decodeJSON(w, r, &body) {
		return
	}

	source, err := h.templates.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = source.Name + " (copy)"
	}

	source.ID = 0
	source.Name = name
	// Cleared so the store derives it again from the inventory rather than
	// carrying a tenant across a write it did not check.
	source.OrganizationID = 0

	created, err := h.templates.Create(r.Context(), source)
	if err != nil {
		h.respondStoreError(w, r, "copy", id, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/templates/"+strconv.Itoa(created.ID))
	dto := toTemplateDTO(created, true)
	Respond(w, r, http.StatusCreated, &dto)
}

// ListConfigs serves a template's saved launch configurations.
func (h *TemplateHandler) ListConfigs(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}

	// The template is read first for its survey, which is what decides
	// which answers are secret. A projection that guessed from the value
	// would be a second place deciding what a password is.
	tmpl, err := h.templates.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}

	configs, err := h.templates.SavedConfigs(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "list configurations", id, err)
		return
	}

	dto := savedConfigListDTO{Configs: make([]savedConfigDTO, 0, len(configs))}
	for _, cfg := range configs {
		dto.Configs = append(dto.Configs, toSavedConfigDTO(cfg, tmpl.Survey))
	}
	Respond(w, r, http.StatusOK, &dto)
}

// CreateConfig stores a named launch configuration against a template.
//
// Named, because the anonymous ones are written by the launch path itself,
// one per launch that supplied anything, so a relaunch can repeat it. This
// endpoint is the other half: a configuration somebody saved on purpose,
// picked by name, which is what a schedule or a workflow node will attach
// to once either exists.
func (h *TemplateHandler) CreateConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}

	var body savedConfigWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		RespondError(w, r, http.StatusBadRequest, "a saved configuration needs a name")
		return
	}

	tmpl, err := h.templates.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}

	// The supplied answers are validated against the survey before anything
	// is stored. A configuration holding an answer the survey would refuse
	// is one that fails at the moment somebody launches from it, which is
	// the worst moment to find out.
	//
	// CheckAnswers rather than Resolve, because a saved configuration is
	// legitimately partial: it may carry the overrides while a required
	// answer arrives at launch, and refusing it here would demand an answer
	// to a question nobody is asking yet.
	if err := tmpl.Survey.CheckAnswers(body.Answers); err != nil {
		RespondError(w, r, http.StatusUnprocessableEntity, "a survey answer is not valid for its question")
		return
	}
	// The run mode is refused here too, not left for the launch: a schedule
	// saved with a mode it can never run would otherwise fail every time
	// it fires.
	if err := tmpl.CheckSavedMode(launch.Fields(body.Fields)); err != nil {
		RespondError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	created, err := h.templates.SaveConfig(r.Context(), launch.SavedConfig{
		TemplateID: id,
		Name:       body.Name,
		Fields:     launch.Fields(body.Fields),
		Answers:    body.Answers,
	})
	if err != nil {
		h.respondStoreError(w, r, "save configuration", id, err)
		return
	}

	dto := toSavedConfigDTO(created, tmpl.Survey)
	Respond(w, r, http.StatusCreated, &dto)
}

// parseTemplateID reads and validates the {id} path parameter.
//
// A package-level function rather than a method, because dispatcher.go's
// launch handler reads the identical parameter off the identical pattern
// and a second implementation of the same check is how the two would
// eventually disagree about what a template id is.
func parseTemplateID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		RespondError(w, r, http.StatusBadRequest, "template id must be a positive integer")
		return 0, false
	}
	return id, true
}

// respondStoreError maps a store error onto the status it deserves, and
// logs anything it cannot classify.
//
// The store's own error text can name a constraint or a column, which a
// caller has no business reading, so only the category crosses the
// boundary. The two validation errors are the exception: their messages are
// written for whoever submitted the form ("a runbook template has no field
// \"forks\""), name nothing internal, and are the only thing that makes a
// 400 actionable.
func (h *TemplateHandler) respondStoreError(w http.ResponseWriter, r *http.Request, op string, id int, err error) {
	switch {
	case errors.Is(err, launch.ErrNotFound):
		RespondError(w, r, http.StatusNotFound, "template not found")
	case errors.Is(err, launch.ErrExists):
		RespondError(w, r, http.StatusConflict, "a template with that name already exists in this organization")
	case errors.Is(err, launch.ErrInUse):
		// 409 rather than 500: the request is well formed and the caller is
		// entitled to make it; the platform is refusing because something
		// else depends on the record. The message names what, because
		// "conflict" alone leaves an operator hunting.
		//
		// It used to say "delete or disable the schedule", which was wrong
		// advice: a disabled schedule still holds the reference, so disabling
		// one does not unblock the delete and following the instruction left
		// an operator with the same 409 and less trust in the message.
		RespondError(w, r, http.StatusConflict,
			"a schedule still launches this template; delete the schedule, or point it at something else, first")
	case errors.Is(err, launch.ErrCrossTenant):
		// 403 rather than 400, matching inventories.go: the submission is
		// well formed and the caller is authenticated, they are simply not
		// entitled to point a template in one tenant at another tenant's
		// fleet. The message says what was refused without naming whose.
		RespondError(w, r, http.StatusForbidden, "that inventory belongs to another organization")
	case errors.Is(err, launch.ErrInvalidTemplate), errors.Is(err, launch.ErrUnknownKind), errors.Is(err, launch.ErrInvalidSurvey):
		RespondError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, launch.ErrDefinitionNotFound):
		// 400, not 404: the URL resolved fine, it is the submitted
		// definition that names nothing this deployment can launch. The
		// message is the domain error's own, which says whether the
		// definition is unknown or the deployment has no source for the
		// kind at all; both are the author's to act on, at create time,
		// which is the entire point of checking here rather than letting
		// the launch fail later as a failed job.
		RespondError(w, r, http.StatusBadRequest, err.Error())
	default:
		h.logger.ErrorContext(r.Context(), "template store operation failed",
			slog.String("op", op),
			slog.Int("template_id", id),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
	}
}
