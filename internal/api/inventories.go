// This file exposes Inventories over the API: the named, shareable sets of
// devices a runbook is dispatched against.
//
// It is the container layer above groups. A group already says which
// devices belong together; an inventory says whose set that is, which
// tenant owns it, and -- through a role binding at inventory scope -- which
// other teams may use it. Sharing is deliberately not a field here: it is a
// RoleBinding, resolved by the same hierarchical chain that governs
// organizations, groups and devices.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// InventoryRepository is the narrow slice of inventory.SetStore these
// handlers need.
//
// SetsForDevice is deliberately absent. It exists to fill a ScopeTarget
// before an access check, which is the admission chain's business and not
// an HTTP handler's; depending on it here would let a future handler answer
// "which inventories can reach this device" to a caller who should not be
// asking. Same Interface Segregation shape RunbookRepository already uses.
type InventoryRepository interface {
	Create(ctx context.Context, set inventory.Set) (inventory.Set, error)
	Get(ctx context.Context, id int) (inventory.Set, error)
	List(ctx context.Context, q inventory.SetQuery) ([]inventory.Set, error)
	UpdateWith(ctx context.Context, id int, fold func(stored inventory.Set) (inventory.Set, error)) (inventory.Set, error)
	Delete(ctx context.Context, id int) error
}

// InventoryHandler serves the inventory container resource.
type InventoryHandler struct {
	sets   InventoryRepository
	logger *slog.Logger
}

// NewInventoryHandler builds the handlers over sets. A nil logger falls
// back to the process default.
func NewInventoryHandler(sets InventoryRepository, logger *slog.Logger) *InventoryHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &InventoryHandler{sets: sets, logger: logger}
}

// inventoryDTO is the wire projection of one inventory.
//
// It carries counts rather than the member ids themselves. A listing exists
// to answer "which inventories are there and how big are they", and
// inlining every device id would make opening a list of twenty inventories
// cost twenty fleet reads for data nothing on that page renders. The detail
// response carries the ids.
type inventoryDTO struct {
	LinkSet

	ID           int    `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Organization int    `json:"organization"`
	Owner        string `json:"owner,omitempty"`
	GroupCount   int    `json:"group_count"`
	DeviceCount  int    `json:"device_count"`

	// Groups and Devices are populated on a single-inventory read and
	// omitted from a listing.
	//
	// Pointers rather than plain slices, because omitempty drops an empty
	// slice as readily as a nil one. Without the indirection, an inventory
	// with no members and a listing that never carried membership would
	// both render as an absent key, which is the exact ambiguity the
	// initialization in toInventoryDTO exists to remove. A nil pointer means
	// "not part of this projection"; a pointer to an empty slice means
	// "included, and empty".
	Groups  *[]int `json:"groups,omitempty"`
	Devices *[]int `json:"devices,omitempty"`
}

type inventoryListDTO struct {
	LinkSet
	Inventories []inventoryDTO `json:"inventories"`
}

// inventoryWriteDTO is the request body create accepts; PATCH accepts
// inventoryPatchDTO (inventories_patch.go). Organization is required.
type inventoryWriteDTO struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Organization int    `json:"organization"`
	Groups       []int  `json:"groups"`
	Devices      []int  `json:"devices"`
}

func toInventoryDTO(set inventory.Set, withMembers bool) inventoryDTO {
	dto := inventoryDTO{
		ID:           set.ID,
		Name:         set.Name,
		Description:  set.Description,
		Organization: set.OrganizationID,
		Owner:        set.Owner,
		GroupCount:   len(set.GroupIDs),
		DeviceCount:  len(set.DeviceIDs),
	}
	if withMembers {
		// Initialized rather than left nil, so the JSON carries [] instead
		// of null for an empty inventory: a client should not have to guess
		// which one null meant.
		groups, devices := set.GroupIDs, set.DeviceIDs
		if groups == nil {
			groups = []int{}
		}
		if devices == nil {
			devices = []int{}
		}
		dto.Groups, dto.Devices = &groups, &devices
	}
	return dto
}

// List serves a page of inventories.
func (h *InventoryHandler) List(w http.ResponseWriter, r *http.Request) {
	q := inventory.SetQuery{Search: r.URL.Query().Get("q")}

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

	sets, err := h.sets.List(r.Context(), q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to list inventories",
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	dto := inventoryListDTO{Inventories: make([]inventoryDTO, 0, len(sets))}
	for _, set := range sets {
		dto.Inventories = append(dto.Inventories, toInventoryDTO(set, false))
	}
	Respond(w, r, http.StatusOK, &dto)
}

// Get serves one inventory, with its membership.
func (h *InventoryHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	set, err := h.sets.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}

	dto := toInventoryDTO(set, true)
	Respond(w, r, http.StatusOK, &dto)
}

// Create persists a new inventory.
func (h *InventoryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body inventoryWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		RespondError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	set, err := h.sets.Create(r.Context(), inventory.Set{
		Name:           body.Name,
		Description:    body.Description,
		OrganizationID: body.Organization,
		// Recorded from the caller's identity, never read off the body: an
		// owner a submitter could name is an audit trail a submitter could
		// forge.
		Owner:     identity.Subject,
		GroupIDs:  body.Groups,
		DeviceIDs: body.Devices,
	})
	if err != nil {
		h.respondStoreError(w, r, "create", 0, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/inventories/"+strconv.Itoa(set.ID))
	dto := toInventoryDTO(set, true)
	Respond(w, r, http.StatusCreated, &dto)
}

// Update serves PATCH /inventories/{id}: a partial update of an
// inventory's name, description and membership. See inventoryPatchDTO and
// inventories_patch.go's comment for the rules.
func (h *InventoryHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	var body inventoryPatchDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.nulls) > 0 {
		RespondError(w, r, http.StatusBadRequest, patchNullRefusal(inventoryNoun, body.nulls))
		return
	}

	// The store runs the fold on the inventory it reads under the row's
	// lock, so a missing inventory is a 404 and a concurrent PATCH's change
	// is folded over rather than written back over.
	updated, err := h.sets.UpdateWith(r.Context(), id, body.fold)
	if err != nil {
		h.respondStoreError(w, r, "update", id, err)
		return
	}
	dto := toInventoryDTO(updated, true)
	Respond(w, r, http.StatusOK, &dto)
}

// Delete removes an inventory.
//
// The devices and groups it referenced are untouched: an inventory is a
// view onto the fleet, not its owner, so deleting a shared collection never
// deletes somebody's servers.
func (h *InventoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}
	if err := h.sets.Delete(r.Context(), id); err != nil {
		h.respondStoreError(w, r, "delete", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseID reads and validates the {id} path parameter.
//
// Rejected unless it parses as a positive integer, for the same reason
// jobs.go requires a UUID: the value reaches a primary-key lookup, and
// constraining its shape at the boundary is cheaper than trusting every
// layer beneath it to.
func (h *InventoryHandler) parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := pathID(r, "id")
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "inventory id must be a positive integer")
		return 0, false
	}
	return id, true
}

// memberRefusal is the one answer for a group or device an inventory may not
// take in, whether it does not exist or belongs to another organization.
const memberRefusal = "one or more of the groups or devices do not exist or cannot be added to this inventory"

// respondStoreError maps a store error onto the status it deserves, and
// logs anything it cannot classify.
//
// The store's own error text can name a constraint or a column, which a
// caller has no business reading, so only the category crosses the
// boundary -- the same posture devices.go and runbooks.go already take.
func (h *InventoryHandler) respondStoreError(w http.ResponseWriter, r *http.Request, op string, id int, err error) {
	var refusal patchRefusal
	var nul *inventory.NULError
	switch {
	case errors.As(err, &refusal):
		// A PATCH's own refusal, made inside the store's transaction.
		RespondError(w, r, http.StatusBadRequest, string(refusal))
	case errors.As(err, &nul):
		// The store's own sentence, naming the field and nothing sent.
		RespondError(w, r, http.StatusBadRequest, nul.Error())
	case errors.Is(err, inventory.ErrSetNameRequired):
		RespondError(w, r, http.StatusBadRequest, "an inventory needs a name that is not blank")
	case errors.Is(err, inventory.ErrSetOrganizationRequired):
		RespondError(w, r, http.StatusBadRequest, "an inventory must belong to an organization")
	case errors.Is(err, inventory.ErrUnknownOrganization):
		RespondError(w, r, http.StatusBadRequest, "the organization does not exist")
	case errors.Is(err, inventory.ErrUnknownMember), errors.Is(err, inventory.ErrCrossTenantMember):
		// One answer for a member that does not exist and one that belongs
		// to another organization (owner decision 2026-11-10): two answers
		// let an inventory:write holder probe ids one at a time and map which
		// devices and groups exist and which are another tenant's. The store
		// runs both checks before returning. The text is true in both cases,
		// and names no id. This removes the difference between the two
		// answers only: until per-organization authorization exists (FP 98),
		// a caller can still name another organization on create, and the UI
		// chooser lists every tenant's devices.
		RespondError(w, r, http.StatusBadRequest, memberRefusal)
	case errors.Is(err, inventory.ErrSetNotFound):
		RespondError(w, r, http.StatusNotFound, "inventory not found")
	case errors.Is(err, inventory.ErrSetExists):
		// A name already taken in that organization is the submitter's
		// mistake, not the platform's, and 409 is what says so.
		RespondError(w, r, http.StatusConflict, "an inventory with that name already exists in this organization")
	default:
		h.logger.ErrorContext(r.Context(), "inventory store operation failed",
			slog.String("op", op),
			slog.Int("inventory_id", id),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
	}
}
