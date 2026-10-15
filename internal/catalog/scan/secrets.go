// scan.secrets: Gitleaks over a directory, every finding named by file,
// line and rule, never by its value.
package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnSecrets,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: requiredCapabilities,
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It reads the tree, mounted read-only into the scanner's container, and writes nothing.",
			},
			Doc: secretsDoc(),
		},
		Invoke: Secrets,
		// It only reads, so a check of it is the task itself.
		Check: Secrets,
	})
}

// leak is one Gitleaks finding as this keeps it: where and which rule,
// never the value. Gitleaks' Secret and Match fields have no field here
// to decode into, so they are never held, redacted or not.
type leak struct {
	RuleID    string `json:"RuleID"`
	File      string `json:"File"`
	StartLine int    `json:"StartLine"`
}

// maxLeaks bounds how many findings one report yields.
const maxLeaks = 100000

// parseLeaks reads Gitleaks' JSON report, refusing one it cannot read
// whole.
func parseLeaks(data []byte) ([]leak, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var leaks []leak
	if err := dec.Decode(&leaks); err != nil {
		return nil, fmt.Errorf("the report is not Gitleaks' JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the report holds more than one JSON document")
	}
	if len(leaks) > maxLeaks {
		return nil, fmt.Errorf("the report holds over %d findings", maxLeaks)
	}
	for _, l := range leaks {
		if l.RuleID == "" || l.File == "" || l.StartLine < 1 {
			return nil, errors.New("the report holds a finding with no rule, file or line")
		}
	}
	return leaks, nil
}

// Secrets implements scan.secrets.
func Secrets(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnSecrets, err)
	}
	t, err := targetParam(params, false)
	if err != nil {
		return fail(err)
	}
	timeout, err := durationParam(params, paramTimeout, defaultTimeout, maxTimeout)
	if err != nil {
		return fail(err)
	}
	pull, err := sdk.BoolParamOr(params, paramPull, true)
	if err != nil {
		return fail(err)
	}
	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnSecrets)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()
	if err := t.check(ctx, ssh); err != nil {
		return fail(err)
	}
	ignore, err := loadIgnore(ctx, ssh, params)
	if err != nil {
		return fail(err)
	}
	engine, err := openEngine(ctx, rc, device, params, fqcnSecrets)
	if err != nil {
		return collection.Result{}, err
	}
	ran, err := run(ctx, engine, runOptions{
		image: gitleaksImage, mounts: []containerengine.ReadOnlyMount{t.mount()}, timeout: timeout, pull: pull,
		// --redact keeps a value out of the report in the first place;
		// --exit-code 0 leaves a non-zero exit to mean an error.
		cmd: []string{"dir", t.inContainer(), "--report-format", "json", "--report-path", "-", "--redact", "--no-banner", "--no-color", "--exit-code", "0", "--log-level", "warn"},
	})
	if err != nil {
		return fail(err)
	}
	if ran.exitCode != 0 {
		return fail(ran.failure("Gitleaks"))
	}
	leaks, err := parseLeaks(ran.report)
	if err != nil {
		return fail(fmt.Errorf("Gitleaks' report: %w", err))
	}

	judge := ignore.Judge(today())
	entries := []any{}
	var failing []string
	for _, l := range leaks {
		file := t.relative(l.File)
		fingerprint := file + ":" + l.RuleID + ":" + strconv.Itoa(l.StartLine)
		entry := map[string]any{"file": file, "line": l.StartLine, "rule": l.RuleID, "fingerprint": fingerprint}
		if e, ok := judge.Ignored(fingerprint); ok {
			entry["ignored"] = e.Reason
		} else {
			failing = append(failing, fmt.Sprintf("%s:%d: %s", file, l.StartLine, l.RuleID))
		}
		if len(entries) < maxRecordedFindings {
			entries = append(entries, entry)
		}
	}
	for key, value := range map[string]any{statFindings: entries, statFailing: len(failing), statScanner: gitleaksImage} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, err
		}
	}
	if err := recordWarnings(rc, ignoreWarnings(judge)); err != nil {
		return collection.Result{}, err
	}
	if len(failing) > 0 {
		return fail(fmt.Errorf("%d possible credential(s) in %s; rotate each, remove it, and ignore a false one by its fingerprint%s", len(failing), t.path, listed(failing)))
	}
	return collection.Result{Changed: false}, nil
}
