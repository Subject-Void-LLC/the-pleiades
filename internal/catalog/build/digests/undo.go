// build.digests.refresh's undo record: the references a run replaced,
// kept on the device under an id short enough for the run's journal, which
// keeps only short scalar values and so could never hold the references
// themselves.
package digests

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// paramUndo names the undo record a refresh replays.
const paramUndo = "undo"

// undoRetentionDays is how long a record no rollback used is kept.
const undoRetentionDays = 90

// maxUndoBytes bounds a record, which holds at most maxFiles files'
// references.
const maxUndoBytes = 1 << 20

// undoIDPattern is a record's id: 128 random bits in hex.
var undoIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// undoEntry is one reference a record writes back: at file and line, the
// reference there now (from) and the one it held before (to).
type undoEntry struct {
	File string `json:"file"`
	Line int    `json:"line"`
	From string `json:"from"`
	To   string `json:"to"`
}

// undoDir is the directory holding records on the device: the login's own
// state directory, private to it.
func undoDir(ctx context.Context, conn *remoteexec.Conn) (string, error) {
	res, err := conn.Run(ctx, `printf '%s' "${XDG_STATE_HOME:-$HOME/.local/state}/pleiades/build-digests"`)
	if err != nil {
		return "", fmt.Errorf("finding the undo record directory: %w", err)
	}
	dir := res.Stdout
	if res.ExitCode != 0 || !path.IsAbs(dir) || path.Clean(dir) != dir || strings.ContainsAny(dir, "\x00\n\r") {
		return "", fmt.Errorf("the login on the device has no usable state directory for an undo record (it resolved to %q)", dir)
	}
	return dir, nil
}

// saveUndo writes the record that writes changes back, before any is made,
// returning its id and path. Records older than undoRetentionDays are
// removed on the way.
func saveUndo(ctx context.Context, conn *remoteexec.Conn, changes []change) (string, string, error) {
	dir, err := undoDir(ctx, conn)
	if err != nil {
		return "", "", err
	}
	quoted := remoteexec.QuoteArg(dir)
	res, err := conn.Run(ctx, "umask 077 && mkdir -p -- "+quoted+" && chmod -- 700 "+quoted+
		" && { find "+quoted+" -type f -name '*.json' -mtime +"+fmt.Sprint(undoRetentionDays)+" -exec rm -f -- {} + 2>/dev/null || true; }")
	if err != nil {
		return "", "", fmt.Errorf("preparing %s for the undo record: %w", dir, err)
	}
	if res.ExitCode != 0 {
		return "", "", fmt.Errorf("preparing %s for the undo record: exited %d: %s", dir, res.ExitCode, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256))
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(raw)
	entries := make([]undoEntry, len(changes))
	for i, c := range changes {
		entries[i] = undoEntry{File: c.file.path, Line: c.ref.Line, From: c.to, To: c.from}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return "", "", err
	}
	// loadUndo reads at most maxUndoBytes, so a larger record could be
	// written and never replayed.
	if len(data) > maxUndoBytes {
		return "", "", fmt.Errorf("its undo record would be %d bytes, over the %d one is read back at; refresh fewer files in one task", len(data), maxUndoBytes)
	}
	record := dir + "/" + id + ".json"
	if err := remotefile.Write(ctx, conn, record, data); err != nil {
		return "", "", fmt.Errorf("writing the undo record: %w", err)
	}
	return id, record, nil
}

// loadUndo reads record id as the set it writes, held to set's own rules,
// with the record's path.
func loadUndo(ctx context.Context, conn *remoteexec.Conn, id string) ([]setEdit, string, error) {
	if !undoIDPattern.MatchString(id) {
		return nil, "", fmt.Errorf("%s: %q is not an undo record's id", paramUndo, id)
	}
	dir, err := undoDir(ctx, conn)
	if err != nil {
		return nil, "", err
	}
	record := dir + "/" + id + ".json"
	data, _, err := remotefile.ReadText(ctx, conn, record, maxUndoBytes)
	if err != nil {
		return nil, "", fmt.Errorf("undo record %s (one is kept %d days): %w", id, undoRetentionDays, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var entries []undoEntry
	if err := dec.Decode(&entries); err != nil || len(entries) == 0 {
		return nil, "", fmt.Errorf("undo record %s is not one this wrote", id)
	}
	list := make([]any, len(entries))
	for i, e := range entries {
		list[i] = map[string]any{"file": e.File, "line": e.Line, "from": e.From, "to": e.To}
	}
	sets, err := setParam(map[string]any{paramSet: list})
	if err != nil {
		return nil, "", fmt.Errorf("undo record %s: %w", id, err)
	}
	return sets, record, nil
}

// dropUndo removes a record no longer needed: one a failed run will never
// be undone through, or one a run has just replayed.
func dropUndo(ctx context.Context, conn *remoteexec.Conn, record string) {
	_, _ = conn.Run(ctx, "rm -f -- "+remoteexec.QuoteArg(record))
}

// filesOf is each file a set names, once, in order.
func filesOf(sets []setEdit) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sets {
		if !seen[s.file] {
			seen[s.file] = true
			out = append(out, s.file)
		}
	}
	return out
}
