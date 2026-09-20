package launch

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entinventory "github.com/Subject-Void-LLC/the-pleiades/internal/ent/inventory"
	entlaunchable "github.com/Subject-Void-LLC/the-pleiades/internal/ent/launchable"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	entconfig "github.com/Subject-Void-LLC/the-pleiades/internal/ent/savedlaunchconfig"
	entquestion "github.com/Subject-Void-LLC/the-pleiades/internal/ent/surveyquestion"
	enttemplate "github.com/Subject-Void-LLC/the-pleiades/internal/ent/template"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// defaultPageSize bounds a List with no explicit limit.
const defaultPageSize = 50

// maxPageSize is the ceiling a caller cannot raise.
const maxPageSize = 200

type entStore struct {
	client  *ent.Client
	catalog Catalog
}

// NewEntStore builds the ent-backed Store over client, verifying every
// created template's definition against catalog.
//
// The catalog is a required collaborator, not an option, and it panics
// rather than degrades when absent. The optional-option shape is how the
// fan-out worker shipped without its inventory store and refused every
// launch at run time with nothing failing at build time
// (FAILURE_PATTERNS.md #110); a store that silently skipped verification
// would be worse, because nothing would refuse anything and the defect
// this check exists to close (a template saved against a definition that
// does not exist, failing later as a failed job) would be back.
func NewEntStore(client *ent.Client, catalog Catalog) Store {
	if catalog == nil {
		panic("launch.NewEntStore: a nil Catalog would silently skip definition verification; wire one (StaticCatalog in tests)")
	}
	return &entStore{client: client, catalog: catalog}
}

// Create persists a new template with its survey.
func (s *entStore) Create(ctx context.Context, tmpl Template) (Template, error) {
	organizationID, err := s.tenantFor(ctx, tmpl.InventoryID)
	if err != nil {
		return Template{}, err
	}

	// The organization is taken from the inventory rather than from the
	// submission, which is what makes ErrCrossTenant unreachable by
	// accident rather than merely refused. A caller who could set it
	// directly could tag their jobs with somebody else's tenant.
	if tmpl.OrganizationID != 0 && tmpl.OrganizationID != organizationID {
		return Template{}, fmt.Errorf("%w: template names organization %d and inventory %d belongs to %d",
			ErrCrossTenant, tmpl.OrganizationID, tmpl.InventoryID, organizationID)
	}
	tmpl.OrganizationID = organizationID

	if err := tmpl.Validate(); err != nil {
		return Template{}, err
	}

	// Existence, after shape. Validate proved the definition is a
	// reference this kind could resolve; this proves the deployment can
	// resolve it today, through the same source a dispatch will use. It
	// runs at create and never at update, because the definition is
	// immutable: re-pointing a saved definition at different code is a
	// copy, not an edit, and the update path carries the stored value
	// forward regardless of the submission.
	if err := s.catalog.Verify(ctx, tmpl.KindName, tmpl.Definition); err != nil {
		return Template{}, err
	}

	// The template, the launchable row standing for it and its survey are
	// written in one transaction. The launchable row is what a schedule
	// points at, so a template created without one is a template nothing can
	// schedule, and a crash between the two writes would leave exactly that
	// with nothing to say why.
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return Template{}, fmt.Errorf("launch: opening a transaction to create template %q: %w", tmpl.Name, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	created, err := tx.Template.Create().
		SetName(strings.TrimSpace(tmpl.Name)).
		SetDescription(tmpl.Description).
		SetKind(tmpl.KindName).
		SetDefinition(strings.TrimSpace(tmpl.Definition)).
		SetDefaults(map[string]any(tmpl.Defaults)).
		SetPrompts(tmpl.Prompts).
		SetRequiredCaps(tmpl.RequiredCaps).
		SetSurveyEnabled(tmpl.Survey.Enabled).
		SetAllowSimultaneous(tmpl.AllowSimultaneous).
		SetOrganizationID(organizationID).
		SetInventoryID(tmpl.InventoryID).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			// The composite unique index on (name, organization). Mapped to
			// a domain error so a caller answers 409 rather than 500 for
			// what is a user's mistake.
			return Template{}, fmt.Errorf("%w: %q", ErrExists, tmpl.Name)
		}
		return Template{}, fmt.Errorf("launch: creating template %q: %w", tmpl.Name, err)
	}

	if err := tx.Launchable.Create().
		SetType(launchable.TypeJobTemplate).
		SetName(created.Name).
		SetOrganizationID(organizationID).
		SetTemplateID(created.ID).
		Exec(ctx); err != nil {
		return Template{}, fmt.Errorf("launch: recording template %d as launchable: %w", created.ID, err)
	}

	if err := replaceQuestionsTx(ctx, tx, created.ID, tmpl.Survey.Questions); err != nil {
		return Template{}, err
	}

	if err := tx.Commit(); err != nil {
		return Template{}, fmt.Errorf("launch: committing template %q: %w", tmpl.Name, err)
	}
	committed = true

	return s.Get(ctx, created.ID)
}

// Get loads one template with its survey.
func (s *entStore) Get(ctx context.Context, id int) (Template, error) {
	row, err := s.client.Template.Query().
		Where(enttemplate.IDEQ(id)).
		WithOrganization().
		WithInventory().
		WithSurveyQuestions(func(q *ent.SurveyQuestionQuery) {
			q.Order(ent.Asc(entquestion.FieldDisplayOrder), ent.Asc(entquestion.FieldID))
		}).
		// The bound credentials, loaded HERE and deliberately not in List
		// below, for the reason the survey is not loaded there either: a
		// list renders a template's name, kind and inventory, and a launch
		// is what needs to know what it runs as. Loading them per row would
		// be an extra query per page for data no column shows.
		WithCredentials().
		// The launchable row standing for this template, so a caller that
		// holds a Template can name it to a schedule without a second query.
		WithLaunchable().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Template{}, fmt.Errorf("%w: template %d", ErrNotFound, id)
		}
		return Template{}, fmt.Errorf("launch: loading template %d: %w", id, err)
	}
	return hydrate(row), nil
}

// ByLaunchable returns the template a launchable row stands for.
func (s *entStore) ByLaunchable(ctx context.Context, launchableID int) (Template, error) {
	row, err := s.client.Template.Query().
		Where(enttemplate.HasLaunchableWith(entlaunchable.IDEQ(launchableID))).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Template{}, fmt.Errorf("%w: no template for launchable %d", ErrNotFound, launchableID)
		}
		return Template{}, fmt.Errorf("launch: loading the template for launchable %d: %w", launchableID, err)
	}
	// Re-read through Get rather than hydrating this row, so a caller gets
	// the same fully loaded Template every other read returns: the survey and
	// the bound credentials are exactly what a launch needs, and this query
	// deliberately loads neither.
	return s.Get(ctx, row.ID)
}

// List returns a page of templates.
func (s *entStore) List(ctx context.Context, q Query) ([]Template, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)

	query := s.client.Template.Query().
		WithOrganization().
		WithInventory().
		WithLaunchable().
		Order(ent.Asc(enttemplate.FieldID)).
		Limit(limit)

	if len(q.OrganizationIDs) > 0 {
		query = query.Where(enttemplate.HasOrganizationWith(entorg.IDIn(q.OrganizationIDs...)))
	}
	if q.After > 0 {
		query = query.Where(enttemplate.IDGT(q.After))
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		query = query.Where(enttemplate.NameContainsFold(search))
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("launch: listing templates: %w", err)
	}

	// The survey is deliberately not loaded for a listing. A list renders a
	// template's name, kind and inventory; loading every question for every
	// row would be a second query per page for data no column shows.
	out := make([]Template, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// Update saves the mutable half of a template.
func (s *entStore) Update(ctx context.Context, tmpl Template) error {
	current, err := s.Get(ctx, tmpl.ID)
	if err != nil {
		return err
	}

	// The kind, the definition and the inventory come from storage rather
	// than the submission. See Store.Update's own doc comment: re-pointing
	// a template at different code or a different fleet, while it keeps its
	// name and its grants, is a new template rather than an edit.
	tmpl.KindName = current.KindName
	tmpl.Definition = current.Definition
	tmpl.InventoryID = current.InventoryID
	tmpl.OrganizationID = current.OrganizationID

	if err := tmpl.Validate(); err != nil {
		return err
	}

	// One transaction again, for the reason Create gives plus one more: the
	// launchable row carries a copy of the name, and a rename that reached
	// only one of the two would leave a picker offering the old name for a
	// template that no longer has it.
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("launch: opening a transaction to update template %d: %w", tmpl.ID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	updated, err := tx.Template.UpdateOneID(tmpl.ID).
		SetName(strings.TrimSpace(tmpl.Name)).
		SetDescription(tmpl.Description).
		SetDefaults(map[string]any(tmpl.Defaults)).
		SetPrompts(tmpl.Prompts).
		SetRequiredCaps(tmpl.RequiredCaps).
		SetSurveyEnabled(tmpl.Survey.Enabled).
		SetAllowSimultaneous(tmpl.AllowSimultaneous).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return fmt.Errorf("%w: %q", ErrExists, tmpl.Name)
		}
		if ent.IsNotFound(err) {
			return fmt.Errorf("%w: template %d", ErrNotFound, tmpl.ID)
		}
		return fmt.Errorf("launch: updating template %d: %w", tmpl.ID, err)
	}

	if _, err := tx.Launchable.Update().
		Where(entlaunchable.HasTemplateWith(enttemplate.IDEQ(updated.ID))).
		SetName(updated.Name).
		Save(ctx); err != nil {
		return fmt.Errorf("launch: renaming the launchable row for template %d: %w", updated.ID, err)
	}

	if err := replaceQuestionsTx(ctx, tx, updated.ID, tmpl.Survey.Questions); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("launch: committing template %d: %w", tmpl.ID, err)
	}
	committed = true
	return nil
}

// Delete removes a template, its survey and its saved configurations.
func (s *entStore) Delete(ctx context.Context, id int) error {
	if err := s.client.Template.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("%w: template %d", ErrNotFound, id)
		}
		if ent.IsConstraintError(err) {
			// Something still references this template. Surveys and saved
			// configurations cascade, so the only edge that can refuse is
			// Schedule's, which is deliberately uncascaded; see ErrInUse.
			return fmt.Errorf("%w: template %d is scheduled", ErrInUse, id)
		}
		return fmt.Errorf("launch: deleting template %d: %w", id, err)
	}
	return nil
}

// SavedConfigs returns a template's stored launch configurations.
func (s *entStore) SavedConfigs(ctx context.Context, templateID int) ([]SavedConfig, error) {
	rows, err := s.client.SavedLaunchConfig.Query().
		Where(entconfig.HasTemplateWith(enttemplate.IDEQ(templateID))).
		Order(ent.Asc(entconfig.FieldID)).
		Limit(maxPageSize).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("launch: listing saved configurations for template %d: %w", templateID, err)
	}

	out := make([]SavedConfig, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrateConfig(row, templateID))
	}
	return out, nil
}

// SaveConfig stores one launch configuration.
func (s *entStore) SaveConfig(ctx context.Context, cfg SavedConfig) (SavedConfig, error) {
	if cfg.TemplateID <= 0 {
		return SavedConfig{}, fmt.Errorf("%w: a saved configuration must belong to a template", ErrNotFound)
	}

	created, err := s.client.SavedLaunchConfig.Create().
		SetName(strings.TrimSpace(cfg.Name)).
		SetFields(map[string]any(cfg.Fields)).
		SetAnswers(cfg.Answers).
		SetTemplateID(cfg.TemplateID).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return SavedConfig{}, fmt.Errorf("%w: template %d", ErrNotFound, cfg.TemplateID)
		}
		return SavedConfig{}, fmt.Errorf("launch: saving a launch configuration: %w", err)
	}
	return s.GetConfig(ctx, created.ID)
}

// GetConfig loads one stored launch configuration.
func (s *entStore) GetConfig(ctx context.Context, id int) (SavedConfig, error) {
	row, err := s.client.SavedLaunchConfig.Query().
		Where(entconfig.IDEQ(id)).
		WithTemplate().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return SavedConfig{}, fmt.Errorf("%w: saved configuration %d", ErrNotFound, id)
		}
		return SavedConfig{}, fmt.Errorf("launch: loading saved configuration %d: %w", id, err)
	}

	templateID := 0
	if row.Edges.Template != nil {
		templateID = row.Edges.Template.ID
	}
	return hydrateConfig(row, templateID), nil
}

// tenantFor resolves which organization an inventory belongs to.
//
// One query, and it is what makes a template's tenancy derived rather than
// asserted. The inventory's organization edge is required by its own
// schema, so an inventory that exists always has one.
func (s *entStore) tenantFor(ctx context.Context, inventoryID int) (int, error) {
	if inventoryID <= 0 {
		return 0, fmt.Errorf("%w: a template needs an inventory to run against", ErrInvalidTemplate)
	}

	row, err := s.client.Inventory.Query().
		Where(entinventory.IDEQ(inventoryID)).
		WithOrganization().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, fmt.Errorf("%w: inventory %d", ErrNotFound, inventoryID)
		}
		return 0, fmt.Errorf("launch: resolving the tenant of inventory %d: %w", inventoryID, err)
	}
	if row.Edges.Organization == nil {
		return 0, fmt.Errorf("launch: inventory %d belongs to no organization", inventoryID)
	}
	return row.Edges.Organization.ID, nil
}

// replaceQuestionsTx rewrites a template's survey inside a transaction: the
// questions are deleted and rewritten rather than diffed, because a survey is
// an ordered whole and display order is positional.
//
// It takes the transaction rather than the store so that a survey cannot be
// half-written beside a committed template: the create and update paths both
// write the template, its launchable row and its survey together or not at
// all.
func replaceQuestionsTx(ctx context.Context, tx *ent.Tx, templateID int, questions []Question) error {
	if _, err := tx.SurveyQuestion.Delete().
		Where(entquestion.HasTemplateWith(enttemplate.IDEQ(templateID))).
		Exec(ctx); err != nil {
		return fmt.Errorf("launch: replacing the survey of template %d: %w", templateID, err)
	}

	for i, q := range questions {
		if _, err := tx.SurveyQuestion.Create().
			SetVariable(strings.TrimSpace(q.Variable)).
			SetLabel(q.Label).
			SetHelp(q.Help).
			SetQuestionType(string(q.Type)).
			SetAllowProgramContent(q.AllowProgramContent).
			SetRequired(q.Required).
			SetDefaultValue(q.Default).
			SetChoices(q.Choices).
			SetMinValue(q.Min).
			SetMaxValue(q.Max).
			SetDisplayOrder(i).
			SetTemplateID(templateID).
			Save(ctx); err != nil {
			return fmt.Errorf("launch: storing survey question %q: %w", q.Variable, err)
		}
	}
	return nil
}

// hydrate projects a stored row onto the domain type.
func hydrate(row *ent.Template) Template {
	tmpl := Template{
		ID:                row.ID,
		Name:              row.Name,
		Description:       row.Description,
		KindName:          row.Kind,
		Definition:        row.Definition,
		Defaults:          Fields(row.Defaults),
		Prompts:           row.Prompts,
		RequiredCaps:      row.RequiredCaps,
		AllowSimultaneous: row.AllowSimultaneous,
		Survey:            Survey{Enabled: row.SurveyEnabled},
	}

	// The names come off edges the query already loaded, rather than being
	// looked up per row. A list that rendered an inventory's primary key
	// would make the reader do the join, which is FAILURE_PATTERNS.md #107.
	if row.Edges.Organization != nil {
		tmpl.OrganizationID = row.Edges.Organization.ID
		tmpl.OrganizationName = row.Edges.Organization.Name
	}
	if row.Edges.Inventory != nil {
		tmpl.InventoryID = row.Edges.Inventory.ID
		tmpl.InventoryName = row.Edges.Inventory.Name
	}
	if row.Edges.Launchable != nil {
		tmpl.LaunchableID = row.Edges.Launchable.ID
	}

	// The bound credentials, as opaque ids. This package knows nothing else
	// about them and deliberately loads nothing else: see
	// Template.CredentialIDs for why they stay opaque all the way to
	// fan-out.
	//
	// Ordered by id rather than left in whatever order the join returned,
	// because the order is meaningful downstream (ordered --vault-id
	// arguments) and an unordered join would make two identical templates
	// produce two different command lines.
	if len(row.Edges.Credentials) > 0 {
		ids := make([]int, 0, len(row.Edges.Credentials))
		for _, c := range row.Edges.Credentials {
			ids = append(ids, c.ID)
		}
		sort.Ints(ids)
		tmpl.CredentialIDs = ids
	}

	questions := make([]Question, 0, len(row.Edges.SurveyQuestions))
	for _, q := range row.Edges.SurveyQuestions {
		questions = append(questions, Question{
			Variable: q.Variable,
			Label:    q.Label,
			Help:     q.Help,
			Type:     QuestionType(q.QuestionType),
			Required: q.Required,

			AllowProgramContent: q.AllowProgramContent,
			Default:             q.DefaultValue,
			Choices:             q.Choices,
			Min:                 q.MinValue,
			Max:                 q.MaxValue,
		})
	}
	tmpl.Survey.Questions = questions

	return tmpl
}

// hydrateConfig projects a stored launch configuration.
func hydrateConfig(row *ent.SavedLaunchConfig, templateID int) SavedConfig {
	return SavedConfig{
		ID:         row.ID,
		TemplateID: templateID,
		Name:       row.Name,
		Fields:     Fields(row.Fields),
		Answers:    row.Answers,
	}
}
