// build.digests.check: every reference pinned, or named as an offender.
package digests

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/imagepin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnCheck,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It reads files and, with resolve, asks the registry; it writes nothing.",
			},
			Doc: checkDoc(),
		},
		Invoke: CheckDigests,
		// It only reads, so a check of it is the task itself.
		Check: CheckDigests,
	})
}

// CheckDigests implements build.digests.check: every reference in the
// files, each pinned by tag and digest or named as an offender, and with
// resolve, each pinned digest compared with what its tag names now.
func CheckDigests(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	paths, err := pathList(params, paramFiles, true)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnCheck, err)
	}
	resolve, err := sdk.BoolParamOr(params, paramResolve, false)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnCheck, err)
	}
	conn, err := sdk.Connect(ctx, rc, device, params, fqcnCheck)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	files, err := scan(ctx, conn, paths)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnCheck, err)
	}
	var res *resolver
	if resolve {
		if res, err = openResolver(ctx, rc, device, params, fqcnCheck); err != nil {
			return collection.Result{}, err
		}
		defer res.session.Close()
	}

	var entries []any
	var offenders []string
	for _, f := range files {
		for _, ref := range f.refs {
			entry := map[string]any{"file": f.path, "line": ref.Line, "reference": remoteexec.Excerpt(ref.Text, maxShown), "where": ref.Where, "pinned": false, "problem": ""}
			problem := ""
			pin, perr := imagepin.Parse(ref.Text)
			switch {
			case ref.Unresolvable != "":
				problem = "it cannot be read as written: " + ref.Unresolvable
			case perr != nil:
				problem = perr.Error()
			case !pin.Pinned():
				problem = "it " + pin.Problem()
			default:
				entry["pinned"] = true
				if res != nil {
					current, err := res.digest(ctx, pin)
					switch {
					case err != nil:
						problem = "its tag could not be resolved: " + err.Error()
					case current != pin.Digest:
						problem = fmt.Sprintf("its tag %s now names %s, not the pinned digest", pin.Tag, current)
						entry["current"] = current
					}
				}
			}
			if problem != "" {
				entry["problem"] = remoteexec.Excerpt(problem, maxShown)
				offenders = append(offenders, fmt.Sprintf("%s:%d %s: %s", f.path, ref.Line, ref.Text, problem))
			}
			entries = append(entries, entry)
		}
	}
	for key, value := range map[string]any{statReferences: entries, statOffenders: len(offenders)} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnCheck, err)
		}
	}
	if len(offenders) > 0 {
		return collection.Result{}, fmt.Errorf("%s: %d of %d image reference(s) are not pinned by tag and digest%s", fqcnCheck, len(offenders), len(entries), listed(offenders))
	}
	return collection.Result{Changed: false}, nil
}
