// Package sdk: what the SSH connections a task opens say about themselves,
// carried back to the task's result as warnings.
//
// A device whose record allows a legacy SSH algorithm is warned about on
// every connection to it (remoteexec.Conn.LegacyWarnings), whichever method
// opened it, so a method needs no code to take part: Connect and its kin
// note each connection here, and the engine adds what was noted to the
// task's warnings once the method returns.
package sdk

import (
	"context"
	"slices"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// connNotes collects the warnings of the connections one task opened to
// one device, each once, in the order first seen.
type connNotes struct {
	device   string
	mu       sync.Mutex
	warnings []string
}

// connNotesKey is the context key for a task's connNotes.
type connNotesKey struct{}

// WithConnectionNotes returns ctx collecting what the SSH connections opened
// under it to the named device report about themselves. The engine sets it
// around every method it runs, and a Runner's child around every call.
func WithConnectionNotes(ctx context.Context, device string) context.Context {
	return context.WithValue(ctx, connNotesKey{}, &connNotes{device: device})
}

// NoteConnection records conn's warnings in ctx's collector, and does
// nothing when ctx carries none or conn has none to say.
func NoteConnection(ctx context.Context, conn *remoteexec.Conn) {
	notes, ok := ctx.Value(connNotesKey{}).(*connNotes)
	if !ok || conn == nil {
		return
	}
	warnings := conn.LegacyWarnings(notes.device)
	notes.mu.Lock()
	defer notes.mu.Unlock()
	for _, w := range warnings {
		if !slices.Contains(notes.warnings, w) {
			notes.warnings = append(notes.warnings, w)
		}
	}
}

// ConnectionWarnings returns what NoteConnection recorded under ctx, each
// warning once, or nil when nothing was.
func ConnectionWarnings(ctx context.Context) []string {
	notes, ok := ctx.Value(connNotesKey{}).(*connNotes)
	if !ok {
		return nil
	}
	notes.mu.Lock()
	defer notes.mu.Unlock()
	return slices.Clone(notes.warnings)
}

// MergeWarnings adds each of warnings to stats' warnings (StatWarnings) that
// it does not hold yet, keeping the ones a method set itself, whether it set
// them as a []string or, after a trip through JSON, as a []any.
func MergeWarnings(stats map[string]any, warnings ...string) {
	if len(warnings) == 0 || stats == nil {
		return
	}
	var merged []string
	switch existing := stats[StatWarnings].(type) {
	case []string:
		merged = slices.Clone(existing)
	case []any:
		for _, w := range existing {
			if s, ok := w.(string); ok {
				merged = append(merged, s)
			}
		}
	}
	for _, w := range warnings {
		if !slices.Contains(merged, w) {
			merged = append(merged, w)
		}
	}
	stats[StatWarnings] = merged
}
