// Tests for win.archive.extract's parameters, branches and records,
// against a model host that reads the script's values and plays it out over
// a modeled filesystem. The script itself runs on a real host in the lab.
package archive

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// winHost is a Windows device reached over WinRM.
type winHost struct{ *inventorytest.Stub }

func (winHost) WinRMHost() string        { return "win1.example.test" }
func (winHost) WinRMPort() int           { return 5986 }
func (winHost) WorkingDirectory() string { return "" }
func (winHost) CmdPath() string          { return winrmexec.DefaultCmdPath }
func (winHost) PowerShellPath() string   { return winrmexec.DefaultPowerShellPath }

func device() inventory.InventoryItem {
	return winHost{&inventorytest.Stub{StubName: "win1", StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameWinRM, capability.NameWindowsShell}}}
}

type recorder struct{ stats map[string]any }

func (r *recorder) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "pw"}
}
func (r *recorder) SetStat(k string, v any) error  { r.stats[k] = v; return nil }
func (r *recorder) EmitFact(k string, v any) error { return r.SetStat(k, v) }

type failingStats struct{ recorder }

func (failingStats) SetStat(string, any) error { return errors.New("the journal is full") }

// model is a host's files and folders, what each call sent, and how its
// tar.exe ends.
type model struct {
	files, folders map[string]bool
	tarExit        int
	scripts        []string
	answer         *winrmexec.Result
}

// value is the single-quoted value a script assigns to name.
func value(script, name string) string {
	m := regexp.MustCompile(`(?m)^\$` + name + ` = '([^']*)'$`).FindStringSubmatch(script)
	if m == nil {
		return ""
	}
	return m[1]
}

func (h *model) install(t *testing.T) {
	t.Helper()
	old := run
	t.Cleanup(func() { run = old })
	run = func(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, _ winrmexec.Shell, script string, _ winrmexec.Options) (winrmexec.Result, error) {
		h.scripts = append(h.scripts, script)
		if h.answer != nil {
			return *h.answer, nil
		}
		src, dest, creates := value(script, "src"), value(script, "dest"), value(script, "creates")
		check := strings.Contains(script, "$check = $true")
		switch {
		case creates != "" && (h.files[creates] || h.folders[creates]):
			return winrmexec.Result{Stdout: "skip=creates\r\n"}, nil
		case !h.files[src] && check:
			return winrmexec.Result{Stdout: "missing=src\r\n"}, nil
		case !h.files[src]:
			return winrmexec.Result{ExitCode: exitNoSrc}, nil
		case h.files[dest] && check:
			return winrmexec.Result{Stdout: "notdir=dest\r\n"}, nil
		case h.files[dest]:
			return winrmexec.Result{ExitCode: exitDestIsNot}, nil
		}
		out := "dest_existed=False\r\n"
		if h.folders[dest] {
			out = "dest_existed=True\r\n"
		}
		if check {
			return winrmexec.Result{Stdout: out}, nil
		}
		if h.tarExit != 0 {
			return winrmexec.Result{Stdout: out + "tar=2\r\n", Stderr: "tar.exe: Error opening archive", ExitCode: exitTar}, nil
		}
		h.folders[dest] = true
		if strings.Contains(script, "$remove = $true") {
			delete(h.files, src)
		}
		return winrmexec.Result{Stdout: out + "extracted=true\r\n"}, nil
	}
}

func extractRun(t *testing.T, mode collection.Mode, params map[string]any) (*recorder, collection.Result, error) {
	t.Helper()
	rc := &recorder{stats: map[string]any{}}
	res, err := extract(context.Background(), rc, device(), params, mode)
	return rc, res, err
}

const goZip = `C:\Windows\Temp\go1.26.9.windows-amd64.zip`

// TestExtract_ExtractsOnceWithCreates: a check predicts the extraction and
// sends no change; the run makes dest and extracts; with creates there, a
// second run is no change; remove deletes the archive.
func TestExtract_ExtractsOnceWithCreates(t *testing.T) {
	h := &model{files: map[string]bool{goZip: true}, folders: map[string]bool{}}
	h.install(t)
	p := map[string]any{"src": goZip, "dest": `C:\`, "creates": `C:\go\bin\go.exe`}
	if _, res, err := extractRun(t, collection.ModeCheck, p); err != nil || !res.Changed || h.folders[`C:\`] {
		t.Fatalf("check: %+v %v", res, err)
	}
	rc, res, err := extractRun(t, collection.ModeExecute, p)
	if err != nil || !res.Changed || rc.stats[statExtracted] != true || rc.stats[statDest] != `C:\` {
		t.Fatalf("run: %+v %v %v", res, err, rc.stats)
	}
	if d := rc.stats[sdk.StatDiff].(map[string]any); d["before"].(map[string]any)["dest_exists"] != false {
		t.Errorf("diff %v", d)
	}
	h.files[`C:\go\bin\go.exe`] = true
	if rc, res, err := extractRun(t, collection.ModeExecute, p); err != nil || res.Changed || rc.stats[statExtracted] != false {
		t.Errorf("again, with creates there: %+v %v", res, err)
	}
	h.files[`C:\Windows\Temp\b.tar.gz`] = true
	if _, res, err := extractRun(t, collection.ModeExecute, map[string]any{"src": `C:\Windows\Temp\b.tar.gz`, "dest": `C:\b`, "remove": true}); err != nil || !res.Changed || h.files[`C:\Windows\Temp\b.tar.gz`] {
		t.Errorf("remove: %+v %v, the archive left: %v", res, err, h.files[`C:\Windows\Temp\b.tar.gz`])
	}
	if !strings.Contains(h.scripts[len(h.scripts)-1], tarPath) {
		t.Error("the script does not run Windows' own tar.exe by its path")
	}
}

// TestExtract_CheckAndFailures: a check names a missing src or a dest that
// is a file as unchecked; a run fails on each, on tar.exe's failure, and on
// an answer it does not recognize.
func TestExtract_CheckAndFailures(t *testing.T) {
	h := &model{files: map[string]bool{`C:\a.zip`: true, `C:\file`: true}, folders: map[string]bool{}}
	h.install(t)
	var cannot *collection.CannotCheckError
	for name, p := range map[string]map[string]any{"missing src": {"src": `C:\none.zip`, "dest": `C:\x`}, "dest a file": {"src": `C:\a.zip`, "dest": `C:\file`}} {
		if _, _, err := extractRun(t, collection.ModeCheck, p); !errors.As(err, &cannot) {
			t.Errorf("check, %s: %v", name, err)
		}
		if _, _, err := extractRun(t, collection.ModeExecute, p); err == nil {
			t.Errorf("run, %s: passed", name)
		}
	}
	h.tarExit = 2
	if _, _, err := extractRun(t, collection.ModeExecute, map[string]any{"src": `C:\a.zip`, "dest": `C:\x`}); err == nil || !strings.Contains(err.Error(), "tar.exe could not extract") {
		t.Errorf("tar failing: %v", err)
	}
	for name, res := range map[string]winrmexec.Result{"another exit": {ExitCode: 1, Stderr: "Access is denied."}, "no report": {Stdout: ""}, "no extraction": {Stdout: "dest_existed=True\r\n"}} {
		r := res
		h.answer = &r
		if _, _, err := extractRun(t, collection.ModeExecute, map[string]any{"src": `C:\a.zip`, "dest": `C:\x`}); err == nil {
			t.Errorf("%s: passed", name)
		}
	}
}

// TestExtract_Refuses: values outside their forms are refused before WinRM
// is reached, as are a device that is not a Windows host and a stat that
// cannot be kept.
func TestExtract_Refuses(t *testing.T) {
	h := &model{files: map[string]bool{`C:\a.zip`: true}, folders: map[string]bool{}}
	h.install(t)
	for name, p := range map[string]map[string]any{
		"no src":           {"dest": `C:\x`},
		"relative src":     {"src": `a.zip`, "dest": `C:\x`},
		"not an archive":   {"src": `C:\a.exe`, "dest": `C:\x`},
		"a quote":          {"src": `C:\a'b.zip`, "dest": `C:\x`},
		"a climb":          {"src": `C:\x\..\a.zip`, "dest": `C:\x`},
		"no dest":          {"src": `C:\a.zip`},
		"relative dest":    {"src": `C:\a.zip`, "dest": `x`},
		"a wildcard":       {"src": `C:\a.zip`, "dest": `C:\x*`},
		"relative creates": {"src": `C:\a.zip`, "dest": `C:\x`, "creates": `go.exe`},
		"a remove word":    {"src": `C:\a.zip`, "dest": `C:\x`, "remove": "please"},
		"a timeout":        {"src": `C:\a.zip`, "dest": `C:\x`, "timeout": 0},
		"not a number":     {"src": `C:\a.zip`, "dest": `C:\x`, "timeout": "soon"},
		"not a string":     {"src": `C:\a.zip`, "dest": `C:\x`, "creates": 5},
	} {
		if _, _, err := extractRun(t, collection.ModeExecute, p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(h.scripts) != 0 {
		t.Errorf("WinRM was reached %d times", len(h.scripts))
	}
	p := map[string]any{"src": `C:\a.zip`, "dest": `C:\x`}
	if _, err := Extract(context.Background(), &recorder{stats: map[string]any{}}, device().(winHost).Stub, p); err == nil {
		t.Error("a device that is not a Windows host passed")
	}
	if _, err := Extract(context.Background(), &failingStats{recorder{stats: map[string]any{}}}, device(), p); err == nil {
		t.Error("a stat that could not be kept passed")
	}
	if _, err := CheckExtract(context.Background(), &recorder{stats: map[string]any{}}, device(), p); err != nil {
		t.Errorf("the exported check: %v", err)
	}
}

// TestExtract_ScriptFitsTheCommandLine: the script, with long values, fits
// the WinRM service's command line.
func TestExtract_ScriptFitsTheCommandLine(t *testing.T) {
	long := `C:\` + strings.Repeat("a", 200)
	r := request{src: long + `.zip`, dest: long, creates: long + `\x`}
	if err := winrmexec.CheckScript(winrmexec.ShellPowerShell, r.script(false), winrmexec.Options{}); err != nil {
		t.Fatal(err)
	}
}
