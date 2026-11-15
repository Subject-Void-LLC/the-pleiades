// Package engine: the failure stages and skip kinds a journal entry may
// record, listed once. The engine writes from these lists, the journal reader
// holds a recorded entry to them, internal/journal's frozen fixtures hold
// every one, and docs/10-running-in-production.md lists every one (both held
// by internal/journal's tests), so none of the four can drift from the rest.
package engine

import "slices"

// recordedFailureStages is every stage a failed node may record.
//
// Every one is listed rather than range-checked, because the point is that
// adding a constant is not enough: a stage this list has never heard of is
// refused, and the entry that would have carried it is not written. See
// projectLevel on what that costs whoever adds one. The list only grows: a
// stage the engine no longer writes stays, or every journal that recorded it
// stops reading.
var recordedFailureStages = []FailureStage{
	FailureStageWorkflowRead,
	FailureStageConditionEval,
	FailureStageSecretMask,
	FailureStageRender,
	FailureStageResolveTarget,
	FailureStageLockAll,
	FailureStageLockDevice,
	FailureStageAction,
	FailureStageFailedWhen,
	FailureStageChangedWhen,
	FailureStageRegisterMask,
	FailureStageRecord,
}

// recordedSkipKinds is every reason a node may be recorded as skipped. It
// only grows, as recordedFailureStages does. A kind added here is refused by
// every older build's strict journal reader, so its journal stops reading
// there (and journal list and rollback, which read every run, stop at it).
var recordedSkipKinds = []SkipKind{
	SkipKindWhen,
	SkipKindWhenOr,
	SkipKindWhenCEL,
	SkipKindLifecycle,
	SkipKindBlockFailed,
	SkipKindNotFailed,
	SkipKindNotNotified,
	SkipKindLimit,
}

// RecordedFailureStages returns every failure stage a journal entry may
// record, in a new slice.
func RecordedFailureStages() []FailureStage { return slices.Clone(recordedFailureStages) }

// RecordedSkipKinds returns every skip kind a journal entry may record, in a
// new slice.
func RecordedSkipKinds() []SkipKind { return slices.Clone(recordedSkipKinds) }

// validFailureStage reports whether stage is one a failed node may record.
// The zero value is not: a failed result carrying it is a failure site that
// never tagged itself, which is exactly the silent hole this check refuses.
func validFailureStage(stage FailureStage) bool { return slices.Contains(recordedFailureStages, stage) }

// validSkipKind reports whether kind is one a skipped node may record; the
// zero value is not.
func validSkipKind(kind SkipKind) bool { return slices.Contains(recordedSkipKinds, kind) }

// IsRecordedFailureStage reports whether stage is one a failed entry may
// carry: the one list the engine writes from (validFailureStage) and the
// journal reader holds a recorded entry to, so the two cannot drift apart.
//
// Because the reader holds old journals to it too, the list only grows: a
// stage the engine no longer writes stays listed, or every journal that
// recorded it stops reading (internal/journal's TestJournalFormatOneReadsEveryKind).
func IsRecordedFailureStage(stage FailureStage) bool { return validFailureStage(stage) }

// IsRecordedSkipKind reports whether kind is one a skipped entry may carry,
// the one list the engine writes from (validSkipKind) and the journal reader
// holds a recorded entry to. Like IsRecordedFailureStage's list, it only
// grows.
func IsRecordedSkipKind(kind SkipKind) bool { return validSkipKind(kind) }
