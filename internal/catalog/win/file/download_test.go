// Tests for win.file.download's parameters, its branches and what it
// records, against a model host that plays out the script's steps over a
// modeled filesystem. The script itself runs on a real host in the lab
// run and its Release Gate.
package file

import (
	"context"
	"fmt"
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

const (
	imageURL  = "https://cloud-images.ubuntu.com/releases/noble/release-20260926/ubuntu-24.04-server-cloudimg-amd64.ova"
	imagePath = `G:\PleiadesLab\ubuntu-24.04-server-cloudimg-amd64.ova`
	imageSHA  = "513a22ebe3982b9387b038f8a2a6dad1af980d4623dca03511312e55ba620b1a"
	otherSHA  = "58001251111ae414947c62799e5b74c522ecaa6fa4cd654f7846042a0c60cbd1"
)

// modelHost is a Windows host's files by path (their digests), the
// folders that exist, and what each URL serves.
type modelHost struct {
	files   map[string]string
	folders map[string]bool
	serves  map[string]string
	scripts []string
	opts    winrmexec.Options
	fail    error
}

// assignment reads one of the script's $name = '...' lines.
func assignment(script, name string) string {
	m := regexp.MustCompile(`(?m)^\$` + name + ` = (?:'([^']*)'|\$(true|false))$`).FindStringSubmatch(script)
	if m == nil {
		return ""
	}
	return m[1] + m[2]
}

// run plays out the script's steps, as winrmexec.Run.
func (h *modelHost) run(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, shell winrmexec.Shell, script string, opts winrmexec.Options) (winrmexec.Result, error) {
	h.scripts = append(h.scripts, script)
	h.opts = opts
	if h.fail != nil {
		return winrmexec.Result{}, h.fail
	}
	if shell != winrmexec.ShellPowerShell {
		return winrmexec.Result{}, fmt.Errorf("not PowerShell")
	}
	url, path, want, check := assignment(script, "url"), assignment(script, "path"), assignment(script, "want"), assignment(script, "check")
	before := h.files[path]
	out := "before=" + before + "\n"
	if before == want {
		return winrmexec.Result{Stdout: out + "after=" + before + "\nsize=594800640\n"}, nil
	}
	if check == "true" {
		return winrmexec.Result{Stdout: out}, nil
	}
	if !h.folders[path[:strings.LastIndex(path, `\`)]] {
		return winrmexec.Result{Stdout: out, ExitCode: exitNoFolder}, nil
	}
	got, ok := h.serves[url]
	if !ok {
		return winrmexec.Result{Stdout: out + "curl=22\n", Stderr: "curl: (22) The requested URL returned error: 404\n", ExitCode: exitFetch}, nil
	}
	if got != want {
		return winrmexec.Result{Stdout: out + "got=" + got + "\n", ExitCode: exitMismatch}, nil
	}
	h.files[path] = got
	return winrmexec.Result{Stdout: out + "after=" + got + "\r\nsize=594800640\r\n"}, nil
}

// onModel makes the method reach h.
func onModel(t *testing.T, h *modelHost) {
	t.Helper()
	old := run
	run = h.run
	t.Cleanup(func() { run = old })
}

// newHost is a host with the lab folder and one image to serve.
func newHost() *modelHost {
	return &modelHost{files: map[string]string{}, folders: map[string]bool{`G:\PleiadesLab`: true}, serves: map[string]string{imageURL: imageSHA}}
}

// recorder keeps what a method records.
type recorder struct {
	stats  map[string]any
	failOn string
}

func (r *recorder) InjectSecrets() map[string]string {
	return map[string]string{"username": "gate", "password": "secret"}
}

func (r *recorder) SetStat(k string, v any) error {
	if k == r.failOn {
		return fmt.Errorf("refusing %s", k)
	}
	r.stats[k] = v
	return nil
}

func (r *recorder) EmitFact(k string, v any) error { return r.SetStat(k, v) }

// windowsHost is a Windows server as the method sees one.
type windowsHost struct{ *inventorytest.Stub }

func (windowsHost) WinRMHost() string        { return "172.18.32.1" }
func (windowsHost) WinRMPort() int           { return 5986 }
func (windowsHost) WorkingDirectory() string { return "" }
func (windowsHost) CmdPath() string          { return `C:\Windows\System32\cmd.exe` }
func (windowsHost) PowerShellPath() string {
	return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
}

var host inventory.InventoryItem = windowsHost{&inventorytest.Stub{StubName: "vengeance", Caps: []capability.Name{capability.NameWindowsShell, capability.NameWinRM}}}

// call runs the method through its registered descriptor.
func call(t *testing.T, check bool, rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup("win.file.download")
	if !ok {
		t.Fatal("win.file.download is not registered")
	}
	if check {
		return d.Check(context.Background(), rc, host, params)
	}
	return d.Invoke(context.Background(), rc, host, params)
}

func params() map[string]any {
	return map[string]any{"url": imageURL, "path": imagePath, "sha256": strings.ToUpper(imageSHA)}
}

func TestDownload(t *testing.T) {
	h := newHost()
	onModel(t, h)
	rc := &recorder{stats: map[string]any{}}
	result, err := call(t, false, rc, params())
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if h.files[imagePath] != imageSHA || rc.stats["sha256"] != imageSHA || rc.stats["size_bytes"] != int64(594800640) || rc.stats["path"] != imagePath {
		t.Errorf("files %v, stats %v", h.files, rc.stats)
	}
	diff := rc.stats[sdk.StatDiff].(map[string]any)
	if diff["before"].(map[string]any)["sha256"] != "" || diff["after"].(map[string]any)["sha256"] != imageSHA {
		t.Errorf("diff %v", diff)
	}
	if h.opts.Timeout.Seconds() != defaultTimeout || !strings.HasSuffix(h.opts.PowerShellPath, `powershell.exe`) {
		t.Errorf("options %+v", h.opts)
	}

	// Already there: nothing fetched.
	h.serves = nil
	rc = &recorder{stats: map[string]any{}}
	if result, err := call(t, false, rc, params()); err != nil || result.Changed {
		t.Errorf("a second run: %+v, %v", result, err)
	}
}

func TestDownload_Check(t *testing.T) {
	h := newHost()
	h.files[imagePath] = otherSHA
	onModel(t, h)
	rc := &recorder{stats: map[string]any{}}
	result, err := call(t, true, rc, params())
	if err != nil || !result.Changed || h.files[imagePath] != otherSHA {
		t.Fatalf("%+v, %v, file %s", result, err, h.files[imagePath])
	}
	diff := rc.stats[sdk.StatDiff].(map[string]any)
	if diff["before"].(map[string]any)["sha256"] != otherSHA || diff["after"].(map[string]any)["sha256"] != imageSHA {
		t.Errorf("predicted %v", diff)
	}
	if !strings.Contains(h.scripts[0], "$check = $true") {
		t.Error("the check's script is not the check's")
	}
}

func TestDownload_Failures(t *testing.T) {
	for name, tt := range map[string]struct {
		change func(*modelHost, map[string]any)
		want   string
	}{
		"no folder":          {func(h *modelHost, p map[string]any) { p["path"] = `G:\missing\image.ova` }, `the folder G:\missing\ does not exist`},
		"a 404":              {func(h *modelHost, p map[string]any) { h.serves = nil }, "404"},
		"the wrong file":     {func(h *modelHost, p map[string]any) { h.serves[imageURL] = otherSHA }, "has SHA-256 " + otherSHA},
		"the host unreached": {func(h *modelHost, p map[string]any) { h.fail = fmt.Errorf("connection refused") }, "connection refused"},
	} {
		h := newHost()
		p := params()
		tt.change(h, p)
		onModel(t, h)
		_, err := call(t, false, &recorder{stats: map[string]any{}}, p)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, tt.want)
		}
		if h.files[imagePath] == otherSHA {
			t.Errorf("%s: a file that failed its digest was kept", name)
		}
	}
	h := newHost()
	onModel(t, h)
	for _, stat := range []string{"path", "sha256", "size_bytes", sdk.StatDiff} {
		if _, err := call(t, false, &recorder{stats: map[string]any{}, failOn: stat}, params()); err == nil {
			t.Errorf("recording %s failed and the download did not", stat)
		}
	}
}

func TestDownload_RefusedBeforeTheHost(t *testing.T) {
	h := newHost()
	onModel(t, h)
	for name, change := range map[string]func(map[string]any){
		"no url":              func(p map[string]any) { delete(p, "url") },
		"http":                func(p map[string]any) { p["url"] = "http://cloud-images.ubuntu.com/x.ova" },
		"a quote in the url":  func(p map[string]any) { p["url"] = imageURL + "';calc'" },
		"a space in the url":  func(p map[string]any) { p["url"] = "https://x/a b" },
		"a long url":          func(p map[string]any) { p["url"] = "https://x/" + strings.Repeat("a", maxURL) },
		"no path":             func(p map[string]any) { delete(p, "path") },
		"a relative path":     func(p map[string]any) { p["path"] = `iso\image.ova` },
		"a quote in the path": func(p map[string]any) { p["path"] = `G:\a'b.ova` },
		"a wildcard":          func(p map[string]any) { p["path"] = `G:\*.ova` },
		"a folder":            func(p map[string]any) { p["path"] = `G:\PleiadesLab\` },
		"dot dot":             func(p map[string]any) { p["path"] = `G:\PleiadesLab\..\Windows\x.ova` },
		"no digest":           func(p map[string]any) { delete(p, "sha256") },
		"a short digest":      func(p map[string]any) { p["sha256"] = "abc" },
		"a zero timeout":      func(p map[string]any) { p["timeout"] = 0 },
		"a text timeout":      func(p map[string]any) { p["timeout"] = "soon" },
	} {
		p := params()
		change(p)
		if _, err := call(t, false, &recorder{stats: map[string]any{}}, p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(h.scripts) != 0 {
		t.Errorf("a refused call reached the host")
	}
	plain := &inventorytest.Stub{StubName: "plain"}
	d, _ := collection.Lookup("win.file.download")
	if _, err := d.Invoke(context.Background(), &recorder{stats: map[string]any{}}, plain, params()); err == nil {
		t.Error("a device that is no Windows host was accepted")
	}
	if _, err := d.Invoke(context.Background(), &recorder{stats: map[string]any{}}, nil, params()); err == nil {
		t.Error("no device was accepted")
	}
}

// TestDownload_Script holds the script to the rules the host relies on:
// values in single quotes, curl by absolute path and HTTPS only, the
// file kept only after its digest matches.
func TestDownload_Script(t *testing.T) {
	d, err := readDownload(params())
	if err != nil {
		t.Fatal(err)
	}
	script := d.script(false)
	for _, want := range []string{
		"$url = '" + imageURL + "'",
		"$path = '" + imagePath + "'",
		"$want = '" + imageSHA + "'",
		"& 'C:\\Windows\\System32\\curl.exe' --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --output $part $url",
		"Move-Item -LiteralPath $part -Destination $path -Force",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q", want)
		}
	}
	if strings.Index(script, "if ($got -ne $want)") > strings.Index(script, "Move-Item") {
		t.Error("the file is moved into place before its digest is checked")
	}
}
