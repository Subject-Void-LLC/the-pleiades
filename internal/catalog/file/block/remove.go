package block

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file implements "file.block.remove", the absent half of
// ansible.builtin.blockinfile.
//
// It takes away the region between the markers AND the markers
// themselves, which is the only sensible reading of removal: a pair of
// marker lines with nothing between them is not a removed block, it is
// an empty one that the next run would find and replace.

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: blockFQCNRemove,
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NamePOSIXFileSystem,
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			// It reads the file and computes the edit before it writes, so
			// a check can predict through the same code (CheckRemove).
			SupportsCheck: true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "Deleting text is usually the least reversible thing a method can do, and the markers are what make this the exception: they " +
					"say exactly which lines belonged to the block, so the run that removes them captures the body and emits a file.block.set that " +
					"writes it back. file.line is weaker because it identifies its target by a pattern rather than by a boundary, so it cannot be " +
					"sure which of several matching lines it took away. What this still does not restore: a block that was in the MIDDLE of a file " +
					"comes back at the END, because file.block.set appends, and the emitted inverse says so in its own description when that " +
					"applies. Nor does it restore a final newline added to a file that had none. A run that found no block emits nothing, because " +
					"undoing a run that changed nothing means doing nothing.",
			},
			Doc: removeDoc(),
		},
		Invoke: Remove,
		Check:  CheckRemove,
	})
}

// removeDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It is duplicated into internal/forge/catalogdata, and
// internal/archtest's TestCatalogDataDocsMatchTheRegistry compares the
// two for equality so the copies cannot drift.
func removeDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes a marked, multi-line block of text from a file.",
		Description: "Takes away the region a file.block.set task left in a file, the two marker lines included, and leaves every other line alone. A file with no such markers is already in the state this asks for, so the task reports no change and writes nothing. The file must already exist, since a path that is not there is far more often a typo than a file whose block is missing; a symbolic link is refused, because writing the file back would replace the link with a regular file. Its mode, owner and group are put back after the rewrite. Markers that do not pair up (two end markers, or an end with no begin) are an error rather than a guess about which lines to delete, which matters more here than anywhere else in this namespace: the guess would be a deletion.",
		Params: []collection.Param{
			{Name: blockParamPath, Type: "string", Required: true, Description: "The file to edit. It must already exist."},
			{Name: blockParamMarker, Type: "string", Default: "# {mark} ANSIBLE MANAGED BLOCK", Description: "The template for both marker lines, which must be the one the block was written with. The {mark} placeholder is replaced by marker_begin on the line above the block and by marker_end on the line below it."},
			{Name: blockParamMarkerBegin, Type: "string", Default: "BEGIN", Description: "The word {mark} becomes on the line above the block."},
			{Name: blockParamMarkerEnd, Type: "string", Default: "END", Description: "The word {mark} becomes on the line below the block."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: blockStatPath, Type: "string", Returned: "always", Description: "The file this task acted on."},
			{Name: blockStatPresent, Type: "bool", Returned: "always", Description: "Whether a marked block is in the file now, read back from the device. False after a successful run."},
			{Name: blockStatBlock, Type: "string", Returned: "always", Description: "The text between the markers now, which is empty once the block is gone."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "The before and after state of the marked region, each holding whether the block was present and what was between the markers. The before half is where the removed text is recorded. Recorded even when nothing changed, in which case the two halves are identical."},
			{Name: sdk.StatInverse, Type: "dict", Returned: "when the run removed a block", Description: "The task that undoes this run: a file.block.set carrying the body that was removed. A run that found no block records none, which is how the journal says undoing it means doing nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Stop managing a hosts file entry",
				RunbookYAML: "- name: Drop the cluster short names\n  file.block.remove:\n    path: /etc/hosts\n",
			},
			{
				Name:        "Remove a block written with its own marker",
				RunbookYAML: "- name: Retire the hardening stanza\n  file.block.remove:\n    path: /etc/ssh/sshd_config\n    marker: \"# {mark} PLEIADES HARDENING\"\n",
			},
		},
		SeeAlso: []string{blockFQCNSet, "file.line.remove", "file.remove"},
	}
}

// removeInput is everything file.block.remove needs, already checked.
//
// It carries no block: which lines to take away is decided by the
// markers on the device, not by text the runbook repeats. That is the
// difference between removing a block and removing text that looks like
// one.
type removeInput struct {
	path    string
	markers blockMarkers
}

// Remove implements the "file.block.remove" collection method: it takes
// a marked region, and its markers, out of a file.
//
// The read that comes first is doing more work here than in the other
// direction. It decides whether anything has to be written at all, it is
// what makes a second run report no change, and it is the ONLY chance to
// capture the text about to be deleted. Once the file is written back
// the block is gone from the device, so an undo that was not built from
// this read could not be built at all.
func Remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRemove is the check of this method: the same reads, refusals and
// change decision as Remove, through the one body both share, then a
// prediction instead of the write. The predicted region is what reading
// the file back would find (blockPredict), and the stats are the ones a
// real run records from it.
func CheckRemove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return remove(ctx, rc, device, params, collection.ModeCheck)
}

// remove is Remove's and CheckRemove's one body; mode says which.
func remove(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	const fqcn = blockFQCNRemove

	req, err := removeRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()

	was, err := blockRequirePlainFile(ctx, conn, req.path, fqcn)
	if err != nil {
		return collection.Result{}, err
	}

	before, err := blockObserve(ctx, conn, req.path, req.markers)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}

	// No markers means the file is already in the state this task asks
	// for. Writing it back anyway would touch its modification time and
	// report a change nobody made.
	after, addedNewline := before, false
	changed := before.region.found

	if changed {
		lines, trailing, added := blockWithout(before)
		addedNewline = added
		if mode == collection.ModeCheck {
			if err := blockCheckAnswer(rc, req.path, before, lines, trailing, req.markers); err != nil {
				return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
			}
			return collection.Result{Changed: true}, nil
		}
		if err := remotefile.Write(ctx, conn, req.path, []byte(blockJoin(lines, trailing))); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := blockRestoreAttributes(ctx, conn, req.path, was); err != nil {
			// The block is gone and its file is wearing the permissions of
			// a temporary. Saying so here is the only place it can be said:
			// the engine discards a Result that arrives with an error.
			return collection.Result{}, fmt.Errorf("%s: the block was removed but %s could not be given back its previous mode and owner: %w", fqcn, req.path, err)
		}
		if after, err = blockObserve(ctx, conn, req.path, req.markers); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	if err := sdk.RecordDiff(rc, sdk.Diff{Before: before.region.state(), After: after.region.state()}); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := blockRecordStats(rc, req.path, after.region); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if changed {
		if err := sdk.RecordInverse(rc, removeInverse(req, before, addedNewline)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}

// removeRequest reads and checks everything this method needs from the
// task's params, before any connection is opened.
func removeRequest(params map[string]any) (removeInput, error) {
	var none removeInput

	path, err := blockPathRequest(params)
	if err != nil {
		return none, err
	}
	markers, err := blockMarkerRequest(params)
	if err != nil {
		return none, err
	}
	return removeInput{path: path, markers: markers}, nil
}

// removeInverse builds the concrete, already-parameterized task that
// undoes this run: a file.block.set carrying the body that was deleted.
//
// It is honest about the one thing it cannot promise. file.block.set
// appends when it finds no markers, so a block taken out of the middle
// of a file comes back at the end of it. That is a real difference, the
// inverse is still worth emitting (the text is what matters and it is
// recoverable), and the operator reading a rollback plan is told which
// lines it will not be landing on.
func removeInverse(req removeInput, before blockObservation, addedNewline bool) sdk.Inverse {
	params := req.markers.params()
	params[blockParamPath] = req.path
	params[blockParamBlock] = before.region.text()

	description := fmt.Sprintf("Writes the block this run removed from %s back between its markers.", req.path)
	if before.region.end != len(before.lines)-1 {
		description += fmt.Sprintf(" It was on lines %d to %d, and %s appends at the end of a file, so it comes back at the end rather than where it was.",
			before.region.begin+1, before.region.end+1, blockFQCNSet)
	}
	description += blockNewlineCaveat(addedNewline)

	return sdk.Inverse{FQCN: blockFQCNSet, Params: params, Description: description}
}
