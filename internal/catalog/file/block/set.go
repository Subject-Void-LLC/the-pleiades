package block

import (
	"context"
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file implements "file.block.set", the present half of
// ansible.builtin.blockinfile.
//
// It creates nothing. A file that is not there is a refusal rather than
// a create, for the reason file.permissions gives: "put this block in
// that file" and "make that file exist" are different intentions, and a
// method that quietly did both would write a one-block file wherever a
// runbook had a typo in a path and then report success.

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: blockFQCNSet,
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"ssh",
			},
			RequiredCapabilities: []capability.Name{
				capability.NamePOSIXFileSystem,
			},
			// Left false even though the files worth managing this way are
			// usually root's. Elevation is a property of the individual
			// task (a block in a file the account already owns needs
			// nothing), so declaring it unconditionally would mark every
			// use as privileged and teach a reader to ignore the field.
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
			},
			PlatformTargets: nil,
			EngineVersion:   ">=1.0.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes: "The strongest undo in the file namespace, and the markers are why: they delimit on the device the exact region this run " +
					"replaced, so a run that changed a block emits an instruction restoring the previous body verbatim, and a run that added one " +
					"emits a file.block.remove. file.line is weaker because it identifies its target by a pattern rather than by a boundary, so a " +
					"later edit can make that pattern match a different line and its undo cannot promise the text lands where it came from. What " +
					"this still does not restore: anything outside the markers, the file's mode and owner (which this method never changes, and " +
					"puts back after each rewrite), and a final newline added to a file that had none, which the emitted inverse says in its own " +
					"description. A converged run emits nothing, because undoing a run that changed nothing means doing nothing.",
			},
			Doc: setDoc(),
		},
		Invoke: Set,
	})
}

// setDoc is this method's reference documentation, kept out of the
// registration above so the manifest fields stay readable.
//
// It is duplicated into internal/forge/catalogdata, and
// internal/archtest's TestCatalogDataDocsMatchTheRegistry compares the
// two for equality so the copies cannot drift.
func setDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Ensures a marked, multi-line block of text is present in a file.",
		Description: "Keeps a region of a text file, delimited by a begin and an end marker line, exactly as the runbook declares it. The markers are what make this safe to run twice: the region carries its own boundaries on the device, so a later run replaces the text between them in place rather than appending a second copy below the first. A file with no such markers gets the block, with its markers, appended at the end. A file whose block already matches is not written at all. The file must already exist, since file.touch and file.copy are what create; a symbolic link is refused, because writing the file back would replace the link with a regular file. Its mode, owner and group are put back after every rewrite, so managing a block in a file that services read does not quietly make it private. Markers that do not pair up (two begin markers, or a begin with no end) are an error rather than a guess about where the block stops.",
		Params: []collection.Param{
			{Name: blockParamPath, Type: "string", Required: true, Description: "The file to edit. It must already exist: this method edits a file rather than creating one."},
			{Name: blockParamBlock, Type: "string", Required: true, Description: "The text to keep between the markers, usually written as a YAML block scalar. A trailing newline is not significant, so the same block written inline and as a block scalar produce the same region. It must not be empty and must not itself contain a marker line."},
			{Name: blockParamMarker, Type: "string", Default: "# {mark} ANSIBLE MANAGED BLOCK", Description: "The template for both marker lines. The {mark} placeholder is replaced by marker_begin on the line above the block and by marker_end on the line below it. Change it for a file whose comment character is not #, and keep it stable afterwards: a task that changes its marker stops finding the block it wrote last time and appends a second one."},
			{Name: blockParamMarkerBegin, Type: "string", Default: "BEGIN", Description: "The word {mark} becomes on the line above the block."},
			{Name: blockParamMarkerEnd, Type: "string", Default: "END", Description: "The word {mark} becomes on the line below the block."},
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
		},
		Returns: []collection.ReturnField{
			{Name: blockStatPath, Type: "string", Returned: "always", Description: "The file this task acted on."},
			{Name: blockStatPresent, Type: "bool", Returned: "always", Description: "Whether a marked block is in the file now, read back from the device."},
			{Name: blockStatBlock, Type: "string", Returned: "always", Description: "The text between the markers now, with no trailing newline."},
			{Name: sdk.StatDiff, Type: "dict", Returned: "always", Description: "The before and after state of the marked region, each holding whether the block was present and what was between the markers. Recorded even when nothing changed, in which case the two halves are identical."},
			{Name: sdk.StatInverse, Type: "dict", Returned: "when the run changed the file", Description: "The task that undoes this run: a file.block.set carrying the previous body, or a file.block.remove when the file carried no block before. A converged run records none, which is how the journal says undoing it means doing nothing."},
		},
		Examples: []collection.Example{
			{
				Name:        "Manage a hosts file entry",
				RunbookYAML: "- name: Keep the cluster's short names resolvable\n  file.block.set:\n    path: /etc/hosts\n    block: |\n      10.0.0.11 db1\n      10.0.0.12 db2\n",
			},
			{
				Name:        "Use a marker a file's own syntax allows",
				RunbookYAML: "- name: Manage the sshd hardening stanza\n  file.block.set:\n    path: /etc/ssh/sshd_config\n    marker: \"# {mark} PLEIADES HARDENING\"\n    block: |\n      PermitRootLogin no\n      PasswordAuthentication no\n",
			},
			{
				Name:        "Keep two independent blocks in one file",
				RunbookYAML: "- name: Manage the proxy stanza only\n  file.block.set:\n    path: /etc/environment\n    marker_begin: OPEN PROXY\n    marker_end: CLOSE PROXY\n    block: |\n      http_proxy=http://proxy.internal:3128\n",
			},
		},
		SeeAlso: []string{blockFQCNRemove, "file.line.set", "file.copy", "file.permissions"},
	}
}

// setInput is everything file.block.set needs, already checked.
type setInput struct {
	path    string
	body    []string
	markers blockMarkers
}

// Set implements the "file.block.set" collection method: it keeps a
// marked region of a file exactly as the runbook declares it.
//
// # The order of operations is the contract
//
// Everything the runbook got wrong is refused before the first packet,
// so a mistake in a task costs no round trip and names the runbook
// rather than the device. Then the file is read, then it is written only
// if the region really differs, then it is read AGAIN so that what gets
// recorded is what the device holds rather than what the task asked for.
//
// Reading first is what makes Changed mean something, and here it also
// makes the undo possible: once the region has been overwritten the text
// that was in it is gone, and the forward run is the only thing that was
// ever in a position to keep it.
func Set(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = blockFQCNSet

	req, err := setRequest(params)
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

	// Compared line by line rather than as one string. A runbook writing
	// the block as a YAML block scalar ends it with a newline and one
	// writing it inline does not, and those two mean the same region: a
	// string comparison would report changed forever after somebody
	// reformatted the runbook.
	after, addedNewline := before, false
	changed := !before.region.found || !slices.Equal(before.region.body, req.body)

	if changed {
		lines, trailing, added := blockWith(before, req.markers, req.body)
		addedNewline = added
		if err := remotefile.Write(ctx, conn, req.path, []byte(blockJoin(lines, trailing))); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		if err := blockRestoreAttributes(ctx, conn, req.path, was); err != nil {
			// The block is on the device and its file is wearing the
			// permissions of a temporary. Saying so here is the only place
			// it can be said: the engine discards a Result that arrives
			// with an error, so nothing else will report it.
			return collection.Result{}, fmt.Errorf("%s: the block was written but %s could not be given back its previous mode and owner: %w", fqcn, req.path, err)
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
		// Only a run that really changed something emits an inverse. A
		// converged run emitting one would tell a rollback engine to
		// perform work the forward run never did.
		if err := sdk.RecordInverse(rc, setInverse(req, before.region, addedNewline)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}

	return collection.Result{Changed: changed}, nil
}

// setRequest reads and checks everything this method needs from the
// task's params, before any connection is opened.
func setRequest(params map[string]any) (setInput, error) {
	var none setInput

	path, err := blockPathRequest(params)
	if err != nil {
		return none, err
	}

	body, _, err := blockTextParam(params, blockParamBlock)
	if err != nil {
		return none, err
	}
	// ansible.builtin.blockinfile treats an empty block as a removal,
	// because state is one parameter there and an empty region is the
	// only way to say "take it away" while state stays present. Removal
	// has its own method here, so an empty block is nearly always a
	// variable that rendered empty, and deleting a region of a
	// configuration file because a lookup returned nothing is the worst
	// failure available. This is a deliberate divergence, and it is a
	// refusal rather than a silent difference in behavior.
	if body == "" {
		return none, fmt.Errorf("%s is required and must not be empty: use %s to take a block away, since an empty block here is nearly always a variable that rendered empty",
			blockParamBlock, blockFQCNRemove)
	}

	markers, err := blockMarkerRequest(params)
	if err != nil {
		return none, err
	}

	lines := blockBodyLines(body)
	for _, line := range lines {
		// A marker line inside the body would be written into the file and
		// then found by the next run, which would read the block as ending
		// early or as being duplicated. Refused here, where the runbook can
		// still be fixed, rather than on the run after next.
		if line == markers.beginLine || line == markers.endLine {
			return none, fmt.Errorf("the %s contains the marker line %q: the markers delimit the block, so a copy of one inside it would move where the next run thinks the block ends",
				blockParamBlock, line)
		}
	}

	return setInput{path: path, body: lines, markers: markers}, nil
}

// setInverse builds the concrete, already-parameterized task that undoes
// this run.
//
// The two shapes are the reason an inverse cannot be declared once on a
// manifest. Against a file that already carried a block, undoing means
// writing the OLD body back, and the same task against a file that
// carried none means removing the block entirely. Same method, same
// parameters, two devices, two different undos.
func setInverse(req setInput, before blockRegion, addedNewline bool) sdk.Inverse {
	// The marker parameters are always spelled out, even when the task
	// used the defaults, so a rollback finds the same region without
	// depending on a default that a later release could change.
	params := req.markers.params()
	params[blockParamPath] = req.path

	if before.found {
		params[blockParamBlock] = before.text()
		return sdk.Inverse{
			FQCN:        blockFQCNSet,
			Params:      params,
			Description: fmt.Sprintf("Writes the previous block back between the markers in %s, exactly as this run found it.", req.path),
		}
	}

	return sdk.Inverse{
		FQCN:   blockFQCNRemove,
		Params: params,
		Description: fmt.Sprintf("Removes the block this run added to %s, which carried no such block before.", req.path) +
			blockNewlineCaveat(addedNewline),
	}
}
