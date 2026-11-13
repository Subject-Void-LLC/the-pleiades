// Tests for a deployment that checks API tokens against an identity
// provider's keys (JWKS_URL) rather than a JWT secret, on both of setup's
// targets. The controller refuses to start with both set (FAILURE_PATTERNS
// 1229), so every path setup offers has to be able to write, keep and check a
// file holding JWKS_URL alone.
package setup_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// testJWKSURL is an identity provider's key set address, the shape Keycloak
// publishes.
const testJWKSURL = "https://idp.example.com/realms/pleiades/protocol/openid-connect/certs"

// writeEnv replaces the .env file in dir with lines.
func writeEnv(t *testing.T, dir string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, setup.ComposeFile), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// envBytes reads the .env file in dir as written.
func envBytes(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, setup.ComposeFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRun_JWKSURLWritesTheURLAndNoJWTSecret: setup --jwks-url on a new
// deployment writes JWKS_URL and no JWT_SECRET, so the controller starts
// checking tokens against the identity provider.
func TestRun_JWKSURLWritesTheURLAndNoJWTSecret(t *testing.T) {
	dir := t.TempDir()
	result, out, err := runCompose(t, dir, setup.Options{JWKSURL: testJWKSURL})
	if err != nil {
		t.Fatalf("Run(--jwks-url) error = %v", err)
	}
	f, raw := readEnv(t, dir)
	if got, _ := f.Get(setup.VarJWKSURL); got != testJWKSURL {
		t.Errorf("JWKS_URL = %q, want %q", got, testJWKSURL)
	}
	if strings.Contains(string(raw), setup.VarJWTSecret) {
		t.Errorf("a --jwks-url run wrote a JWT_SECRET line, which the controller refuses beside JWKS_URL:\n%s", raw)
	}
	if _, ok := f.Get(setup.VarMasterKey); !ok {
		t.Error("a --jwks-url run wrote no master key")
	}
	if result.TokenCheck != setup.VarJWKSURL {
		t.Errorf("TokenCheck = %q, want %q", result.TokenCheck, setup.VarJWKSURL)
	}
	if !strings.Contains(out, "JWKS_URL                   "+testJWKSURL) || strings.Contains(out, "JWT_SECRET") {
		t.Errorf("the summary does not name the JWKS URL alone:\n%s", out)
	}
}

// TestRun_CheckAcceptsAJWKSFileAndRefusesBoth: --check, which `make up` runs
// first, treats a file holding a key and JWKS_URL as complete, so `make up`
// starts the stack rather than running setup again to write a JWT secret. A
// file holding both is a refusal, not an incomplete file: running setup
// cannot decide which one the operator meant.
func TestRun_CheckAcceptsAJWKSFileAndRefusesBoth(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCompose(t, dir, setup.Options{JWKSURL: testJWKSURL}); err != nil {
		t.Fatal(err)
	}
	result, _, err := runCompose(t, dir, setup.Options{Check: true})
	if err != nil {
		t.Fatalf("--check on a key and a JWKS_URL error = %v, want complete", err)
	}
	if result.TokenCheck != setup.VarJWKSURL {
		t.Errorf("--check TokenCheck = %q, want %q", result.TokenCheck, setup.VarJWKSURL)
	}

	f, _ := readEnv(t, dir)
	key, _ := f.Get(setup.VarMasterKey)
	writeEnv(t, dir, setup.VarMasterKey+"="+key, setup.VarJWKSURL+"="+testJWKSURL, setup.VarJWTSecret+"="+strings.Repeat("j", 44))
	_, _, err = runCompose(t, dir, setup.Options{Check: true})
	msg := refusalOf(t, err)
	for _, want := range []string{"JWT_SECRET", "JWKS_URL", "delete the JWT_SECRET line", "delete the JWKS_URL line"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the both-set refusal does not say %q:\n%s", want, msg)
		}
	}

	writeEnv(t, dir, setup.VarJWKSURL+"="+testJWKSURL)
	if _, _, err := runCompose(t, dir, setup.Options{Check: true}); !errors.Is(err, setup.ErrIncomplete) {
		t.Errorf("--check on a JWKS_URL with no key error = %v, want ErrIncomplete", err)
	}
}

// TestRun_AJWKSFileNeverGainsAJWTSecret: a run over a file that already
// checks tokens against JWKS_URL adds what is missing (here the facts digest
// key) and never a JWT secret, and a run with nothing to add is refused as
// for any complete file.
func TestRun_AJWKSFileNeverGainsAJWTSecret(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCompose(t, dir, setup.Options{JWKSURL: testJWKSURL}); err != nil {
		t.Fatal(err)
	}
	f, _ := readEnv(t, dir)
	key, _ := f.Get(setup.VarMasterKey)
	writeEnv(t, dir, setup.VarMasterKey+"="+key, setup.VarJWKSURL+"="+testJWKSURL, setup.VarMaxOutage+"=30m")

	if _, out, err := runCompose(t, dir, setup.Options{}); err != nil || !strings.Contains(out, "JWKS_URL                   unchanged") {
		t.Fatalf("a run over an older JWKS file: err = %v, output:\n%s", err, out)
	}
	raw := envBytes(t, dir)
	if strings.Contains(raw, setup.VarJWTSecret) || !strings.Contains(raw, setup.VarFactsDigestKey+"=") {
		t.Fatalf("the run added a JWT secret, or not the missing facts digest key:\n%s", raw)
	}

	if _, _, err := runCompose(t, dir, setup.Options{}); err == nil {
		t.Fatal("a re-run over a complete JWKS file was not refused")
	}
	if after := envBytes(t, dir); after != raw {
		t.Fatal("a refused re-run changed the file")
	}
}

// TestRun_MovingToJWKSTakesForceAndRemovesTheSecret is the path the
// controller's refusal sends an operator down: a deployment set up with a
// JWT secret moves to an identity provider. It takes --force, since every
// token signed with the secret stops working, and it removes the secret
// rather than leaving it to sign tokens again if JWKS_URL is removed later.
func TestRun_MovingToJWKSTakesForceAndRemovesTheSecret(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCompose(t, dir, setup.Options{}); err != nil {
		t.Fatal(err)
	}
	before, raw := readEnv(t, dir)
	key, _ := before.Get(setup.VarMasterKey)

	_, _, err := runCompose(t, dir, setup.Options{JWKSURL: testJWKSURL})
	if msg := refusalOf(t, err); !strings.Contains(msg, "--force") || !strings.Contains(msg, "JWT_SECRET") {
		t.Fatalf("moving to JWKS without --force: refusal does not ask for --force over the JWT secret:\n%s", msg)
	}
	if _, now := readEnv(t, dir); string(now) != string(raw) {
		t.Fatal("a refused move changed the file")
	}

	_, out, err := runCompose(t, dir, setup.Options{JWKSURL: testJWKSURL, Force: true})
	if err != nil {
		t.Fatalf("--jwks-url --force error = %v", err)
	}
	after, afterRaw := readEnv(t, dir)
	if strings.Contains(string(afterRaw), setup.VarJWTSecret) {
		t.Errorf("the move left a JWT_SECRET line:\n%s", afterRaw)
	}
	if got, _ := after.Get(setup.VarJWKSURL); got != testJWKSURL {
		t.Errorf("JWKS_URL = %q after the move", got)
	}
	if got, _ := after.Get(setup.VarMasterKey); got != key {
		t.Error("moving to JWKS changed the master key")
	}
	if !strings.Contains(out, "JWT_SECRET                 removed") {
		t.Errorf("the summary does not say the JWT secret was removed:\n%s", out)
	}
	if _, _, err := runCompose(t, dir, setup.Options{Check: true}); err != nil {
		t.Errorf("--check after the move error = %v", err)
	}

	// Pointing at another identity provider is the same kind of change.
	other := "https://login.example.org/keys"
	if _, _, err := runCompose(t, dir, setup.Options{JWKSURL: other}); !strings.Contains(refusalOf(t, err), "--force") {
		t.Fatalf("pointing JWKS_URL elsewhere without --force was not refused for want of it: %v", err)
	}
	if _, _, err := runCompose(t, dir, setup.Options{JWKSURL: other, Force: true}); err != nil {
		t.Fatalf("pointing JWKS_URL elsewhere with --force error = %v", err)
	}
	if f, _ := readEnv(t, dir); func() string { v, _ := f.Get(setup.VarJWKSURL); return v }() != other {
		t.Error("JWKS_URL was not replaced")
	}
}

// TestRun_JWKSRefusals: every request setup cannot turn into a file the
// controller starts with is refused before anything is written, and no
// refusal repeats a credential a URL carried.
func TestRun_JWKSRefusals(t *testing.T) {
	cases := []struct {
		name string
		opts setup.Options
		want string
	}{
		{"plain http", setup.Options{JWKSURL: "http://idp.example.com/keys"}, "https"},
		{"a password in the URL", setup.Options{JWKSURL: "https://admin:hunter2@idp.example.com/keys"}, "user name or password"},
		{"a user in the URL", setup.Options{JWKSURL: "https://hunter2@idp.example.com/keys"}, "user name or password"},
		{"a relative URL", setup.Options{JWKSURL: "/keys"}, "absolute"},
		{"both ways at once", setup.Options{JWKSURL: testJWKSURL, NewJWTSecret: true, Force: true}, "opposite things"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, _, err := runCompose(t, dir, tc.opts)
			msg := refusalOf(t, err)
			if !strings.Contains(msg, tc.want) || strings.Contains(msg, "hunter2") {
				t.Errorf("refusal = %q, want it to name %q without repeating a credential", msg, tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, setup.ComposeFile)); !os.IsNotExist(err) {
				t.Error("a refused run wrote .env")
			}
		})
	}

	// A JWT secret cannot be "replaced" in a file that holds none.
	dir := t.TempDir()
	if _, _, err := runCompose(t, dir, setup.Options{JWKSURL: testJWKSURL}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCompose(t, dir, setup.Options{NewJWTSecret: true, Force: true}); !strings.Contains(refusalOf(t, err), "delete the JWKS_URL line") {
		t.Errorf("--new-jwt-secret on a JWKS file was not refused with the way back: %v", err)
	}

	// A hand-edited file holding both is refused by an ordinary run too.
	f, _ := readEnv(t, dir)
	key, _ := f.Get(setup.VarMasterKey)
	writeEnv(t, dir, setup.VarMasterKey+"="+key, setup.VarJWKSURL+"="+testJWKSURL, setup.VarJWTSecret+"="+strings.Repeat("j", 44))
	if _, _, err := runCompose(t, dir, setup.Options{}); !strings.Contains(refusalOf(t, err), "holds both JWT_SECRET and JWKS_URL") {
		t.Errorf("a run over a file holding both was not refused: %v", err)
	}
}

// TestParseEnvFile_ReadsJWKSURLByTheControllersRule: a JWKS_URL line is one
// of setup's own, read strictly, and refused when the controller would
// refuse it, without the refusal repeating the value.
func TestParseEnvFile_ReadsJWKSURLByTheControllersRule(t *testing.T) {
	if f, err := setup.ParseEnvFile([]byte(setup.VarJWKSURL + "=" + testJWKSURL + "\n")); err != nil {
		t.Fatalf("a good JWKS_URL line was refused: %v", err)
	} else if got, _ := f.Get(setup.VarJWKSURL); got != testJWKSURL {
		t.Fatalf("JWKS_URL read as %q", got)
	}
	for _, bad := range []string{"http://idp.example.com/keys", "https://admin:hunter2@idp.example.com/keys"} {
		_, err := setup.ParseEnvFile([]byte(setup.VarJWKSURL + "=" + bad + "\n"))
		if err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "JWKS_URL") {
			t.Errorf("JWKS_URL=%s: error = %v, want a refusal naming the variable and not the value", bad, err)
		}
	}
}

// TestRenderHelm_AJWKSURLGoesToTheValuesAndNoJWTSecretToTheSecret is the
// Helm half: the Secret carries no JWT_SECRET, and the values file sets
// controller.jwksUrl, which the chart turns into JWKS_URL.
func TestRenderHelm_AJWKSURLGoesToTheValuesAndNoJWTSecretToTheSecret(t *testing.T) {
	in := helmInput()
	in.JWTSecret, in.JWKSURL = "", testJWKSURL
	s, _, _, valuesYAML := render(t, in)
	if _, has := s.StringData[setup.VarJWTSecret]; has {
		t.Errorf("the Secret carries JWT_SECRET beside a JWKS URL: %v", s.StringData)
	}
	var v struct {
		Controller struct {
			JWKSURL string `yaml:"jwksUrl"`
		} `yaml:"controller"`
	}
	if err := yaml.Unmarshal(valuesYAML, &v); err != nil {
		t.Fatal(err)
	}
	if v.Controller.JWKSURL != testJWKSURL {
		t.Errorf("controller.jwksUrl = %q, want %q", v.Controller.JWKSURL, testJWKSURL)
	}

	// A secret-backed render sets no jwksUrl.
	_, _, _, plain := render(t, helmInput())
	if strings.Contains(string(plain), "jwksUrl") {
		t.Error("a render with a JWT secret set controller.jwksUrl")
	}

	for name, bad := range map[string]setup.HelmInput{
		"both":    func() setup.HelmInput { b := helmInput(); b.JWKSURL = testJWKSURL; return b }(),
		"neither": func() setup.HelmInput { b := helmInput(); b.JWTSecret = ""; return b }(),
		"plain http": func() setup.HelmInput {
			b := helmInput()
			b.JWTSecret, b.JWKSURL = "", "http://idp.example.com/keys"
			return b
		}(),
	} {
		if _, _, err := setup.RenderHelm(bad); err == nil {
			t.Errorf("RenderHelm accepted %s", name)
		}
	}
}

// TestRun_HelmWithAJWKSURL runs the Helm target end to end with --jwks-url:
// the Secret file has no JWT_SECRET and the values file names the URL.
func TestRun_HelmWithAJWKSURL(t *testing.T) {
	dir := t.TempDir()
	result, out, err := runHelmTarget(t, dir, setup.Options{JWKSURL: testJWKSURL})
	if err != nil {
		t.Fatalf("Run(helm, --jwks-url) error = %v", err)
	}
	secret, err := os.ReadFile(filepath.Join(dir, setup.HelmSecretFile))
	if err != nil {
		t.Fatal(err)
	}
	values, err := os.ReadFile(filepath.Join(dir, setup.HelmValuesFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secret), setup.VarJWTSecret) {
		t.Errorf("the Secret file carries JWT_SECRET:\n%s", secret)
	}
	if !strings.Contains(string(values), "jwksUrl: "+testJWKSURL) {
		t.Errorf("the values file does not set controller.jwksUrl:\n%s", values)
	}
	if result.TokenCheck != setup.VarJWKSURL || strings.Contains(out, "the JWT secret") {
		t.Errorf("TokenCheck = %q; summary:\n%s", result.TokenCheck, out)
	}

	refusedDir := t.TempDir()
	if _, _, err := runHelmTarget(t, refusedDir, setup.Options{JWKSURL: "http://idp.example.com/keys"}); !strings.Contains(refusalOf(t, err), "https") {
		t.Errorf("a plain http --jwks-url on the Helm target was not refused: %v", err)
	}
	if entries, _ := os.ReadDir(refusedDir); len(entries) != 0 {
		t.Error("a refused Helm run wrote files")
	}
}

// runHelmTarget runs the Helm target in dir and returns what it printed.
func runHelmTarget(t *testing.T, dir string, opts setup.Options) (setup.Result, string, error) {
	t.Helper()
	opts.Target, opts.Dir, opts.Namespace = setup.TargetHelm, dir, "pleiades"
	var out bytes.Buffer
	result, err := setup.Run(context.Background(), opts, nil, &out)
	return result, out.String(), err
}
