// The Builder that decides, field by field, what one run may change.
package setup

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// Action is what a plan does to one field.
type Action int

// The actions.
const (
	// Keep leaves the field as the file has it.
	Keep Action = iota

	// Generate writes a field the file does not have yet.
	Generate

	// Replace writes a new value over one the file already has.
	Replace
)

// Plan is the checked set of changes one run makes to a compose env file.
// Only PlanBuilder.Build makes one, and it makes one only when every change
// in it has passed the guard its field's class requires.
type Plan struct {
	// Key, JWT and Budget are the three fields a run can write.
	Key, JWT, Budget Action

	// BudgetValue is the budget to write when Budget is not Keep.
	BudgetValue topology.OutageBudget

	// HeldKey is the key the file already holds, and HeldPrevious the
	// previous key during a rotation. Either is nil when the file has none.
	HeldKey, HeldPrevious []byte

	// Lowering is set when the plan lowers an existing budget, which the
	// run states as what it discards.
	Lowering bool

	// PreviousBudget is the budget being lowered from.
	PreviousBudget topology.OutageBudget
}

// Changes reports whether the plan writes anything.
func (p *Plan) Changes() bool {
	return p.Key != Keep || p.JWT != Keep || p.Budget != Keep
}

// PlanBuilder decides, field by field, what one run of the compose target
// may change, checking each decision against its field's guard as it is
// made. It is this command's Builder: the decisions accumulate, the first
// refusal stops the build, and only Build hands back a Plan.
type PlanBuilder struct {
	file    *EnvFile
	display string
	opts    Options
	plan    Plan
	err     error
}

// NewPlanBuilder starts a plan for file, which a message names as display.
func NewPlanBuilder(file *EnvFile, display string, opts Options) *PlanBuilder {
	return &PlanBuilder{file: file, display: display, opts: opts}
}

// MasterKey decides the master key: generated when the file has none,
// replaced only when the operator named the destructive flag, kept
// otherwise. The census and the confirmation a replacement also needs come
// later, because they need a database and a person; this decides only
// whether the request is well formed.
func (b *PlanBuilder) MasterKey() *PlanBuilder {
	if b.err != nil {
		return b
	}
	if prev, ok := b.file.Get(VarPreviousKey); ok {
		b.plan.HeldPrevious, _ = crypto.DecodeKey(prev, VarPreviousKey)
	}
	raw, held := b.file.Get(VarMasterKey)
	if !held {
		b.plan.Key = Generate
		return b
	}
	b.plan.HeldKey, _ = crypto.DecodeKey(raw, VarMasterKey)
	if b.opts.DestroyKey {
		b.plan.Key = Replace
	}
	return b
}

// JWTSecret decides the JWT secret: generated when the file has none,
// replaced only on --new-jwt-secret with --force.
func (b *PlanBuilder) JWTSecret() *PlanBuilder {
	if b.err != nil {
		return b
	}
	if _, held := b.file.Get(VarJWTSecret); !held {
		b.plan.JWT = Generate
		return b
	}
	if !b.opts.NewJWTSecret {
		return b
	}
	if !b.opts.Force {
		b.err = needsForce(b.display, "a JWT_SECRET; replacing it makes every API token signed with the old one stop working")
		return b
	}
	b.plan.JWT = Replace
	return b
}

// Budget decides the outage budget. answer is the budget to use when the
// file has none: the operator's --max-outage, their answer to the question,
// or the default. An existing budget changes only on --max-outage with
// --force, and lowering one is marked so the run can say what it discards.
func (b *PlanBuilder) Budget(answer topology.OutageBudget) *PlanBuilder {
	if b.err != nil {
		return b
	}
	raw, held := b.file.Get(VarMaxOutage)
	if !held {
		b.plan.Budget, b.plan.BudgetValue = Generate, answer
		return b
	}
	current, err := topology.ParseOutageBudget(raw)
	if err != nil {
		// ParseEnvFile already refused an invalid budget, so this is
		// unreachable for a parsed file; refusing is still the safe answer.
		b.err = refuse(fmt.Sprintf("%s holds a PLEIADES_MAX_OUTAGE setup cannot read: %v", b.display, err))
		return b
	}
	if b.opts.MaxOutage == "" || answer == current {
		return b
	}
	if !b.opts.Force {
		b.err = needsForce(b.display, fmt.Sprintf("PLEIADES_MAX_OUTAGE=%s", shortDuration(current.Duration())))
		return b
	}
	b.plan.Budget, b.plan.BudgetValue = Replace, answer
	if answer < current {
		b.plan.Lowering, b.plan.PreviousBudget = true, current
	}
	return b
}

// Build returns the plan, the first refusal a decision produced, or the
// refusal for a run that asked for nothing to change. That last one names
// the file, because re-running setup over a working file is the moment an
// operator most needs to hear what replacing it would destroy.
func (b *PlanBuilder) Build() (*Plan, error) {
	if b.err != nil {
		return nil, b.err
	}
	if !b.plan.Changes() {
		return nil, alreadySetUp(b.display, crypto.Fingerprint(b.plan.HeldKey))
	}
	return &b.plan, nil
}
