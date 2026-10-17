// The registry methods against this machine's real Docker engine and its
// own files. No registry runs here (testsupport pins none), so login is
// proved on the engine's refusal: a registry address nothing listens on,
// whose refusal must leave no file and no password anywhere.
package registry_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// password is the planted secret every test looks for in every output.
const password = "registry-sentinel-7Hq"

// holds reports whether any stat, or err, carries the password or its
// encodings.
func holds(rc *recorder, err error) string {
	forms := []string{password, base64.StdEncoding.EncodeToString([]byte("deployer:" + password))}
	text := fmt.Sprint(rc.stats)
	if err != nil {
		text += err.Error()
	}
	for _, f := range forms {
		if strings.Contains(text, f) {
			return f
		}
	}
	return ""
}

// TestLogin_ARefusedAccountIsNeverKept: the engine's refusal fails the
// task, run or check, before any file is written, with the password
// masked and in no output; with no credential bound the task is refused
// before the engine is asked; a malformed registry is refused.
func TestLogin_ARefusedAccountIsNeverKept(t *testing.T) {
	h := newHarness(t)
	authFile := filepath.Join(t.TempDir(), "docker", "config.json")
	params := map[string]any{"registry": "127.0.0.1:9", "auth_file": authFile}
	for _, check := range []bool{true, false} {
		rc := fresh()
		rc.accounts["127.0.0.1:9"] = [2]string{"deployer", password}
		_, err := call(t, h, rc, "container.registry.login", check, params)
		if err == nil || !strings.Contains(err.Error(), "refused the account deployer") {
			t.Fatalf("login (check %v) to nowhere = %v", check, err)
		}
		if leak := holds(rc, err); leak != "" {
			t.Errorf("login (check %v) put %q in its output", check, leak)
		}
		if !strings.Contains(strings.Join(rc.masked, "\n"), password) {
			t.Errorf("login (check %v) did not register the password for masking", check)
		}
		if _, err := os.Stat(authFile); !os.IsNotExist(err) {
			t.Fatalf("a refused login wrote %s (%v)", authFile, err)
		}
	}
	if _, err := call(t, h, fresh(), "container.registry.login", false, params); err == nil || !strings.Contains(err.Error(), "no registry credential is bound") {
		t.Errorf("login with no credential = %v", err)
	}
	other := fresh()
	other.accounts["ghcr.io"] = [2]string{"deployer", password}
	if _, err := call(t, h, other, "container.registry.login", false, params); err == nil || !strings.Contains(err.Error(), "no registry credential is bound") {
		t.Errorf("login with another host's credential = %v", err)
	}
	for _, bad := range []string{"ghcr.io/org", "-x", "a b", "HOST"} {
		if _, err := call(t, h, fresh(), "container.registry.login", false, map[string]any{"registry": bad}); err == nil {
			t.Errorf("registry %q was accepted", bad)
		}
	}
	if _, err := call(t, h, fresh(), "container.registry.login", false, map[string]any{"registry": "ghcr.io", "auth_file": "rel/config.json"}); err == nil {
		t.Error("a relative auth_file was accepted")
	}
}

// TestLogoutAndInfo_TheFileAsData: info names each login and its account
// and never a password; a check of logout writes nothing; logout removes
// the one login, keeps every other key, and leaves the file 0600; a second
// logout is no change; an absent file reads as no logins.
func TestLogoutAndInfo_TheFileAsData(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	authFile := filepath.Join(dir, "config.json")
	auth := base64.StdEncoding.EncodeToString([]byte("deployer:" + password))
	original := `{"auths":{"registry.example.com":{"auth":"` + auth + `"},"ghcr.io":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("bot:x")) + `"}},"detachKeys":"ctrl-q"}`
	if err := os.WriteFile(authFile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	rc := fresh()
	if res, err := call(t, h, rc, "container.registry.info", false, map[string]any{"registry": "registry.example.com", "auth_file": authFile}); err != nil || res.Changed {
		t.Fatalf("info = %+v, %v", res, err)
	}
	if rc.stats["logged_in"] != true || rc.stats["username"] != "deployer" {
		t.Errorf("info = %v", rc.stats)
	}
	if regs, _ := rc.stats["registries"].([]map[string]any); len(regs) != 2 || regs[0]["registry"] != "ghcr.io" || regs[0]["username"] != "bot" {
		t.Errorf("registries = %v", rc.stats["registries"])
	}
	if leak := holds(rc, nil); leak != "" {
		t.Errorf("info put %q in its output", leak)
	}

	params := map[string]any{"registry": "registry.example.com", "auth_file": authFile}
	if res, err := call(t, h, fresh(), "container.registry.logout", true, params); err != nil || !res.Changed {
		t.Fatalf("a check of logout = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(authFile); string(data) != original {
		t.Fatal("a check of logout changed the file")
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.registry.logout", false, params); err != nil || !res.Changed {
		t.Fatalf("logout = %+v, %v", res, err)
	}
	if _, ok := rc.stats["inverse"]; ok {
		t.Error("logout recorded an undo")
	}
	data, err := os.ReadFile(authFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "registry.example.com") || !strings.Contains(string(data), "ghcr.io") || !strings.Contains(string(data), `"detachKeys": "ctrl-q"`) {
		t.Errorf("after logout the file is %s", data)
	}
	if info, err := os.Stat(authFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("after logout the file's mode = %v, %v", info.Mode(), err)
	}
	if res, err := call(t, h, fresh(), "container.registry.logout", false, params); err != nil || res.Changed {
		t.Errorf("a second logout = %+v, %v; want no change", res, err)
	}

	rc = fresh()
	if _, err := call(t, h, rc, "container.registry.info", true, map[string]any{"registry": "ghcr.io", "auth_file": filepath.Join(dir, "absent.json")}); err != nil {
		t.Fatal(err)
	}
	if rc.stats["logged_in"] != false {
		t.Errorf("info of an absent file = %v", rc.stats)
	}
}

// TestLogin_RefusesAFileThatIsNotJSON: a login file that does not parse is
// refused without quoting it, since it may hold passwords.
func TestLogin_RefusesAFileThatIsNotJSON(t *testing.T) {
	h := newHarness(t)
	authFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(authFile, []byte("not json "+password), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := call(t, h, fresh(), "container.registry.logout", false, map[string]any{"registry": "ghcr.io", "auth_file": authFile})
	if err == nil || strings.Contains(err.Error(), password) {
		t.Errorf("logout of a file that is not JSON = %v", err)
	}
}
