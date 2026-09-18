// This file is the Tasks tab: what a job's runbook actually did, node by
// node, on each device it reached.
//
// It is the first reader the run journal has ever had. The table has been
// written since the journal shipped -- migrations in both dialects, a
// subscriber the Controller refuses to start without, an index declared for
// exactly this query -- and nothing anywhere queried it. The data was
// durable, correct and invisible.
//
// The honest caveat is structural and is stated on the page rather than
// buried here. Only the native execution adapter publishes journal entries,
// so a job of kind `playbook` has no entries and never will: its per-task
// detail exists instead as parsed stdout in the live log stream. That means
// this tab covers runbook jobs and the Live output tab covers playbook
// jobs, and neither covers both. Rendering an empty table under a playbook
// job would say "this job did nothing", which is false and is the exact
// ambiguity view.Status and Section.Empty exist to prevent.
package jobs

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// tasksTitle is the section heading and its page-index slug.
const tasksTitle = "Tasks"

// journalFields are the Tasks table's columns.
//
// Chosen against one question -- "what happened, in order, and where did it
// stop" -- rather than by projecting every column the row has. The journal
// records twenty-odd fields; the ones a person reads down a page are the
// order, the device, what ran, and how it came out.
var journalFields = []view.Field{
	{Name: "step", Label: "#", Kind: view.KindText, InList: true},
	// The device is a link for the reason the Device outcomes table gives:
	// this row is where an investigation stops being about a job and
	// starts being about a machine.
	{Name: "device", Label: "DEVICE", Kind: view.KindText, InList: true, MobilePrimary: true, References: "devices"},
	{Name: "task", Label: "TASK", Kind: view.KindText, InList: true},
	// The FQCN beside the task name, because the name is free text a
	// runbook author chose and the FQCN is what actually ran. A task named
	// "restart it" tells a reader nothing; svc.systemd.restart does.
	{Name: "action", Label: "ACTION", Kind: view.KindText, InList: true},
	{Name: "outcome", Label: "OUTCOME", Kind: view.KindBadge, InList: true, BadgeClass: journalBadge},
	{Name: "took", Label: "TOOK", Kind: view.KindText, InList: true},
	// Why it stopped, when it stopped. The journal has no error MESSAGE by
	// design -- it carries no field that could hold a value, which two
	// archtests enforce -- so this is the stage and the skip reason, which
	// is what it can honestly say. The message lives in the log stream.
	{Name: "detail", Label: "DETAIL", Kind: view.KindText, InList: true},
}

// journalBadge maps a node outcome onto the closed badge set.
//
// It is a second mapping beside taskBadge rather than a shared one, because
// the two vocabularies are different: a JobTask's outcome says whether a
// DEVICE was handed to a Runner, and a journal entry's says what one NODE
// of the runbook did. Merging them would mean one function answering two
// questions, and the pair that spells "failed" the same way would hide that
// they mean different things.
func journalBadge(value string) string {
	switch value {
	case string(engine.OutcomeChanged):
		return "badge-changed"
	case string(engine.OutcomeRan):
		return "badge-ok"
	case string(engine.OutcomeFailed):
		return "badge-failed"
	case string(engine.OutcomeSkipped), string(engine.OutcomeNotReached):
		return "badge-skipped"
	default:
		return "badge-neutral"
	}
}

// tasksSection is the Tasks tab.
//
// It takes the reader and the job store both, and the second is not
// redundant. A journal entry records a device ID and nothing else about the
// device, so without the job's own task rows this table would render raw
// identifiers in the column an operator is most likely to want to click --
// which is the "a list that prints ORGANIZATION: 1 has moved the join into
// the reader's head" failure this UI already has a rule about.
func tasksSection(entries JournalReader, jobs dispatch.JobStore) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   tasksTitle,
		Summary: "Every node this job's runbook ran, in order, on each device it reached.",
		Fields:  journalFields,
		Empty: "No per-task record for this job. A playbook job never has one: only the native runbook " +
			"executor writes the run journal, and a playbook's per-task detail is in Live output instead. " +
			"A runbook job shows nothing here until its first level of nodes has finished and reported.",
		Rows: journalRows(entries, jobs),
		Note: journalNote(entries),
	}
}

// journalRows loads one job's entries and projects them.
func journalRows(entries JournalReader, jobs dispatch.JobStore) func(context.Context, string) ([]view.Row, error) {
	return func(ctx context.Context, jobID string) ([]view.Row, error) {
		if jobID == "" {
			// A journal belongs to a job, not to the collection, so the
			// section on the list page shows its empty state rather than
			// every entry in the system.
			return nil, nil
		}

		found, _, err := entries.ForJob(ctx, jobID, journalLimit)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, nil
		}

		names := deviceNames(ctx, jobs, jobID)

		out := make([]view.Row, 0, len(found))
		for _, e := range found {
			name := names[e.DeviceID]
			if name == "" {
				// A device the job has no task row for, which a redelivered
				// dispatch can produce. The id is worse than a name and
				// better than a blank cell.
				name = e.DeviceID
			}
			out = append(out, view.Row{
				// The node id, which is unique per device per attempt and
				// is what a row is addressed by if this section ever gains
				// a control. Two devices share a node id, so it is not
				// unique within the section; nothing here addresses a row,
				// and a section with row actions would need the triple.
				ID: e.NodeID,
				Cells: view.Cells{
					"step":    strconv.Itoa(e.Sequence),
					"device":  name,
					"task":    taskLabel(e),
					"action":  actionLabel(e),
					"outcome": string(e.Outcome),
					"took":    took(e),
					"detail":  detail(e),
				},
				Refs: map[string]string{"device": e.DeviceID},
			})
		}
		return out, nil
	}
}

// journalNote says when the table is showing less than the whole run.
//
// A second read rather than a value threaded out of Rows, because Section
// resolves the two separately and there is nowhere to hand one to the
// other. It costs one more query on a page that is already doing two, and
// the alternative is a table that silently shows the first five hundred
// rows of a larger run while looking complete.
func journalNote(entries JournalReader) func(context.Context, string) string {
	return func(ctx context.Context, jobID string) string {
		if jobID == "" {
			return ""
		}
		found, truncated, err := entries.ForJob(ctx, jobID, journalLimit)
		if err != nil || !truncated {
			// A failed read is reported by Rows, which runs first and
			// logs it. Saying it twice, in two voices, would be worse
			// than saying it once.
			return ""
		}
		// Names the control on this page rather than an API, which is
		// what this used to say: there is no journal endpoint in
		// internal/apispec and never has been, so an operator who
		// followed that sentence found nothing to follow. The CSV holds
		// four times what this table does and discloses its own bound in
		// the file, which is why it can be pointed at honestly.
		return "Showing the first " + strconv.Itoa(len(found)) +
			" of a longer run. This is not the whole record: the run journal (CSV) under Downloads " +
			"holds more, and says in the file if it stops short too."
	}
}

// deviceNames maps a job's device ids to the names it dispatched under.
//
// Best effort: a job whose task rows cannot be read still renders a table,
// with identifiers in the device column instead of names. The alternative
// is failing a page that has everything else it needs.
func deviceNames(ctx context.Context, jobs dispatch.JobStore, jobID string) map[string]string {
	if jobs == nil {
		return nil
	}
	_, tasks, err := jobs.Get(ctx, jobID)
	if err != nil {
		return nil
	}
	names := make(map[string]string, len(tasks))
	for _, t := range tasks {
		names[t.DeviceID] = t.DeviceName
	}
	return names
}

// taskLabel is what the runbook author called this node, falling back to
// the node's own id.
//
// The id is a position in the document (`tasks[3]`), which is a worse
// answer than a name and a much better one than a blank cell on the column
// a reader scans first.
func taskLabel(e engine.JournalEntry) string {
	if name := strings.TrimSpace(e.TaskName); name != "" {
		return name
	}
	return e.NodeID
}

// actionLabel is the FQCN that ran, marked when the platform could not
// resolve it.
//
// An unresolved FQCN is recorded rather than dropped, and it matters here:
// it is the difference between "this task ran svc.systemd.restart" and
// "this task named something this deployment does not have", which look
// identical in a bare column.
func actionLabel(e engine.JournalEntry) string {
	if e.FQCN == "" {
		return ""
	}
	if e.FQCNUnresolved {
		return e.FQCN + " (unresolved)"
	}
	return e.FQCN
}

// took renders how long a node ran.
//
// Blank rather than "0s" when either stamp is missing, because a node that
// never started and a node that took no measurable time are different facts
// and zero is the wrong way to say the first.
func took(e engine.JournalEntry) string {
	if e.StartedAt.IsZero() || e.FinishedAt.IsZero() {
		return ""
	}
	d := e.FinishedAt.Sub(e.StartedAt)
	if d < 0 {
		// Clock skew between a Runner and this Controller, which is not
		// this column's problem to adjudicate.
		return ""
	}
	if d < time.Second {
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	}
	return d.Round(time.Millisecond).String()
}

// detail says why a node came out the way it did, as far as the journal
// can.
//
// The journal deliberately holds no error message: it carries no field
// whose type could contain a value, which two archtests enforce, so the
// reason a task failed is in the log stream and not here. What it does hold
// is the STAGE a failure happened at and the kind of skip, and both are
// worth a column: "failed at connect" and "failed at execute" send an
// operator to different places.
func detail(e engine.JournalEntry) string {
	switch {
	case e.FailureStage != "":
		return "failed at " + string(e.FailureStage)
	case e.SkipKind != "" && e.SkipTotal > 0:
		// The ordinal is what makes a loop's skips readable: "skipped by
		// when (2 of 5)" says four others ran.
		return "skipped by " + string(e.SkipKind) +
			" (" + strconv.Itoa(e.SkipOrdinal) + " of " + strconv.Itoa(e.SkipTotal) + ")"
	case e.SkipKind != "":
		return "skipped by " + string(e.SkipKind)
	default:
		return ""
	}
}

// sections is the job detail page's tables, in the order they are indexed.
//
// Tasks is offered only when a journal reader is wired, and that is the
// difference between an absent section and an empty one. A deployment with
// no journal reader has no per-task record to show for ANY job, so drawing
// the tab would promise something the deployment cannot do; a deployment
// that has one draws it for every job and the section's own Empty sentence
// explains a playbook job's blank table. The first is a property of the
// installation, the second of the record, and only the second belongs on
// the page.
func sections(jobs dispatch.JobStore, entries JournalReader) []view.Section {
	out := []view.Section{deviceOutcomes(jobs)}
	if entries != nil {
		out = append(out, tasksSection(entries, jobs))
	}
	return out
}
