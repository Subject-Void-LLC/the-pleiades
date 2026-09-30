// The WinRM execution modes Release Gate: every mode, against a real
// Windows host, with the WS-Man messages captured as they were sent.
//
// It is skipped unless PLEIADES_WINRM_HOST names a host and a credential
// is set, the same variables the other WinRM gates read:
// PLEIADES_WINRM_USER and PLEIADES_WINRM_PASSWORD for NTLM, or
// PLEIADES_WINRM_CERTIFICATE and PLEIADES_WINRM_CERTIFICATE_KEY (PEM
// files), or PLEIADES_WINRM_PFX and PLEIADES_WINRM_PFX_PASSWORD (or
// PLEIADES_WINRM_PFX_PASSWORD_FILE, naming the file the setup script writes), for a
// client certificate over HTTPS. PLEIADES_WINRM_CA names the authority
// that issued the listener's certificate. PLEIADES_WINRM_PORT
// overrides the port. examples/windows_lab/winrm-cert-setup.ps1
// configures a host for the certificate form.
//
// What it proves, one subtest each:
//
//   - ShellNone: a program receives exactly the arguments CommandLine was
//     given, cmd.exe metacharacters included, from a path with a space in
//     it too, and a near-limit line arrives whole. The WinRM service puts
//     cmd.exe in front of every command (cmdexe.go), so this is the proof
//     that transparentLine makes it pass the line through unchanged. The
//     program is argvecho (testdata/argvecho), copied to the host over
//     stdin, which is also the proof that stdin carries a large binary
//     intact through a pipe.
//   - ShellCmd: a builtin runs, its exit code survives, and a value read
//     as !NAME! stays text even when it contains & echo.
//   - ShellPowerShell: exit 42 is 42, a failing native program's code
//     survives, a failed cmdlet is 1, and a $(...) value stays text.
//   - A missing program is reported, by cmd.exe, as a failed command.
//   - No message on the wire holds a numeric character reference, which
//     the service does not decode.
//
// It also logs two measurements the design depends on rather than
// asserting them, because they describe the host: the profile directory
// a command sees with and without WINRS_NOPROFILE.
package winrmexec

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/masterzen/winrm"
	"github.com/masterzen/winrm/soap"
)

// gateHost reads the gate's target and credential from the environment,
// skipping the test when they are not set.
func gateHost(t *testing.T) (Target, Auth, Options) {
	t.Helper()
	host := os.Getenv("PLEIADES_WINRM_HOST")
	if host == "" {
		t.Skip("the WinRM modes Release Gate needs a real Windows host: set PLEIADES_WINRM_HOST and a credential")
	}
	target := Target{Host: host}
	if p := os.Getenv("PLEIADES_WINRM_PORT"); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("PLEIADES_WINRM_PORT: %v", err)
		}
		target.Port = port
	}
	var auth Auth
	if pfxPath := os.Getenv("PLEIADES_WINRM_PFX"); pfxPath != "" {
		// The form examples/windows_lab/winrm-cert-setup.ps1 exports,
		// opened the way a stored credential is.
		bundle, err := os.ReadFile(pfxPath)
		if err != nil {
			t.Fatalf("reading the PFX: %v", err)
		}
		passphrase := os.Getenv("PLEIADES_WINRM_PFX_PASSWORD")
		if file := os.Getenv("PLEIADES_WINRM_PFX_PASSWORD_FILE"); passphrase == "" && file != "" {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("reading the passphrase file: %v", err)
			}
			passphrase = strings.TrimRight(string(raw), "\r\n")
		}
		auth, err = AuthFromSecrets(map[string]string{
			wire.SecretPFXBase64:  base64.StdEncoding.EncodeToString(bundle),
			wire.SecretPassphrase: passphrase,
		})
		if err != nil {
			t.Fatalf("opening the PFX: %v", err)
		}
	} else if certPath := os.Getenv("PLEIADES_WINRM_CERTIFICATE"); certPath != "" {
		cert, err := os.ReadFile(certPath)
		if err != nil {
			t.Fatalf("reading the certificate: %v", err)
		}
		key, err := os.ReadFile(os.Getenv("PLEIADES_WINRM_CERTIFICATE_KEY"))
		if err != nil {
			t.Fatalf("reading the certificate key: %v", err)
		}
		auth = Auth{CertificatePEM: cert, PrivateKeyPEM: key}
	} else {
		auth = Auth{Username: os.Getenv("PLEIADES_WINRM_USER"), Password: os.Getenv("PLEIADES_WINRM_PASSWORD")}
		if auth.Username == "" {
			t.Skip("the WinRM modes Release Gate needs a credential: PLEIADES_WINRM_USER and _PASSWORD, or _CERTIFICATE and _CERTIFICATE_KEY")
		}
	}
	opts := Options{Timeout: 3 * time.Minute}
	// PLEIADES_WINRM_CA names the authority that issued the host's
	// listener certificate (winrm-cert-setup.ps1 writes it as ca.pem), so
	// the server is verified rather than trusted blindly. A DER file, the
	// ca.cer an older run of the setup wrote, is accepted here too.
	if caPath := os.Getenv("PLEIADES_WINRM_CA"); caPath != "" {
		raw, err := os.ReadFile(caPath)
		if err != nil {
			t.Fatalf("reading PLEIADES_WINRM_CA: %v", err)
		}
		if !strings.Contains(string(raw), "-----BEGIN") {
			raw = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
		}
		opts.CACert = raw
	}
	return target, auth, opts
}

// recorder wraps the real transport and keeps every message it posts.
type recorder struct {
	inner poster
	sent  []string
}

// Post implements poster.
func (r *recorder) Post(client *winrm.Client, message *soap.SoapMessage) (string, error) {
	r.sent = append(r.sent, message.String())
	return r.inner.Post(client, message)
}

// executeRecorded is Execute with the transport wrapped by rec, so the
// gate sees the messages exactly as they went out. It takes the same
// steps Execute takes, in the same order, minus the caller-release
// goroutine, which has no effect on what is sent.
func executeRecorded(ctx context.Context, t *testing.T, rec *recorder, target Target, auth Auth, cmd Command, opts Options) (Result, error) {
	t.Helper()
	opts = opts.resolve(auth)
	line, err := commandLine(cmd.Shell, cmd.Script, opts)
	if err != nil {
		return Result{}, err
	}
	if cmd.Shell == ShellCmd {
		if err := checkCmdEnvReads(cmd.Script, cmd.Env); err != nil {
			return Result{}, err
		}
	}
	spec, err := shellSpecFor(cmd, opts)
	if err != nil {
		return Result{}, err
	}
	x, err := newExchange(target, auth, opts)
	if err != nil {
		return Result{}, err
	}
	rec.inner = x.transport
	x.transport = rec
	return x.run(ctx, line, cmd.Stdin, spec)
}

// argvReport is what argvecho prints.
type argvReport struct {
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Dir         string            `json:"dir"`
	StdinLen    int               `json:"stdin_len"`
	StdinSHA256 string            `json:"stdin_sha256"`
}

func TestModesReleaseGate(t *testing.T) {
	target, auth, opts := gateHost(t)
	ctx := context.Background()
	rec := &recorder{}
	run := func(t *testing.T, cmd Command) Result {
		t.Helper()
		res, err := executeRecorded(ctx, t, rec, target, auth, cmd, opts)
		if err != nil {
			t.Fatalf("%v %q: %v", cmd.Shell, cmd.Script, err)
		}
		return res
	}

	// Build argvecho for Windows and copy it to the host over stdin.
	exe := filepath.Join(t.TempDir(), "argvecho.exe")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", exe, "./testdata/argvecho")
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building argvecho: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	upload := run(t, Command{
		Shell: ShellPowerShell,
		Script: "$d = Join-Path $env:TEMP 'pleiades-argvecho.exe'; [IO.File]::WriteAllBytes($d, [Convert]::FromBase64String([Console]::In.ReadToEnd())); " +
			"$s = Join-Path $env:TEMP 'pleiades argvecho (spaced)'; New-Item -ItemType Directory -Force -Path $s | Out-Null; Copy-Item $d (Join-Path $s 'argvecho.exe') -Force; " +
			"(Get-FileHash -Algorithm SHA256 $d).Hash; $d",
		Stdin: base64.StdEncoding.EncodeToString(binary),
	})
	lines := strings.Fields(upload.Stdout)
	if len(lines) != 2 {
		t.Fatalf("upload printed %q", upload.Stdout)
	}
	wantHash := sha256.Sum256(binary)
	if !strings.EqualFold(lines[0], hex.EncodeToString(wantHash[:])) {
		t.Fatalf("the program arrived as %s, want %x: stdin did not carry it intact", lines[0], wantHash)
	}
	remote := lines[1]
	spaced := remote[:strings.LastIndex(remote, `\`)] + `\pleiades argvecho (spaced)\argvecho.exe`
	t.Cleanup(func() {
		_, _ = Execute(ctx, target, auth, Command{Shell: ShellPowerShell, Script: "Remove-Item -Force " + QuotePS(remote) +
			"; Remove-Item -Recurse -Force " + QuotePS(spaced[:strings.LastIndex(spaced, `\`)])}, opts)
	})
	argvOf := func(t *testing.T, program string, args ...string) argvReport {
		t.Helper()
		line, err := CommandLine(program, args...)
		if err != nil {
			t.Fatal(err)
		}
		res := run(t, Command{Shell: ShellNone, Script: line})
		var got argvReport
		if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil {
			t.Fatalf("argvecho printed %q (stderr %q): %v", res.Stdout, res.Stderr, err)
		}
		return got
	}

	t.Run("none: the program receives exactly its arguments", func(t *testing.T) {
		args := []string{"a b", "c&d|e", "f^g", "^^", "%PATH%", `q"uote`, `C:\trailing\`, "", "!bang!", "$(Get-Date)", "(x)", "<y>", "\u00e9\U0001F600"}
		for _, program := range []string{remote, spaced} {
			got := argvOf(t, program, args...)
			if strings.Join(got.Args, "\x00") != strings.Join(args, "\x00") || len(got.Args) != len(args) {
				t.Errorf("from %s: args = %q, want %q", program, got.Args, args)
			}
		}
	})

	t.Run("none: a line near cmd.exe's limit arrives whole", func(t *testing.T) {
		long := strings.Repeat("x", MaxCmdLine-len(remote)-300)
		got := argvOf(t, remote, long)
		if len(got.Args) != 1 || got.Args[0] != long {
			t.Errorf("a %d character argument arrived as %d argument(s)", len(long), len(got.Args))
		}
	})

	t.Run("none: env and stdin reach the program", func(t *testing.T) {
		line, _ := CommandLine(remote)
		stdin := strings.Repeat("0123456789abcdef", 1<<16)
		res := run(t, Command{Shell: ShellNone, Script: line, Stdin: stdin, Env: map[string]string{"PLEIADES_X": "& echo INJECTED"}})
		var got argvReport
		if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil {
			t.Fatalf("argvecho printed %q: %v", res.Stdout, err)
		}
		sum := sha256.Sum256([]byte(stdin))
		if got.StdinLen != len(stdin) || got.StdinSHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("stdin arrived as %d bytes %s, want %d", got.StdinLen, got.StdinSHA256, len(stdin))
		}
		if got.Env["PLEIADES_X"] != "& echo INJECTED" {
			t.Errorf("env = %v", got.Env)
		}
	})

	t.Run("cmd: a builtin, an exit code, and a value that stays text", func(t *testing.T) {
		res := run(t, Command{Shell: ShellCmd, Script: "echo [!PLEIADES_X!]", Env: map[string]string{"PLEIADES_X": "a & echo INJECTED"}})
		if strings.TrimSpace(res.Stdout) != "[a & echo INJECTED]" {
			t.Errorf("stdout = %q: the value must print as text, not run", res.Stdout)
		}
		if res := run(t, Command{Shell: ShellCmd, Script: "ver & exit /b 5"}); res.ExitCode != 5 || !strings.Contains(res.Stdout, "Windows") {
			t.Errorf("ver & exit /b 5 = %+v", res)
		}
	})

	t.Run("powershell: exit codes are real", func(t *testing.T) {
		cases := []struct {
			script string
			want   int
		}{
			{script: "Write-Output ok", want: 0},
			{script: "exit 42", want: 42},
			{script: "& " + QuotePS(DefaultCmdPath) + " /c exit 7", want: 7},
			{script: "Get-Item -LiteralPath 'C:\\definitely\\not\\here'", want: 1},
			{script: "& " + QuotePS(DefaultCmdPath) + " /c exit 7\nWrite-Output recovered", want: 0},
		}
		for _, c := range cases {
			if res := run(t, Command{Shell: ShellPowerShell, Script: c.script}); res.ExitCode != c.want {
				t.Errorf("%q exited %d, want %d (stderr %q)", c.script, res.ExitCode, c.want, res.Stderr)
			}
		}
		res := run(t, Command{Shell: ShellPowerShell, Script: "Write-Output $env:PLEIADES_X", Env: map[string]string{"PLEIADES_X": "$(Write-Output INJECTED)"}})
		if strings.TrimSpace(res.Stdout) != "$(Write-Output INJECTED)" {
			t.Errorf("stdout = %q: the value must print as text", res.Stdout)
		}
	})

	t.Run("a missing program is a failed command", func(t *testing.T) {
		// The service starts it through cmd.exe, which reports it rather
		// than the service refusing the command, so it is an exit status.
		res, err := executeRecorded(ctx, t, rec, target, auth, Command{Shell: ShellNone, Script: `C:\no\such\program.exe`}, opts)
		if err != nil || res.ExitCode == 0 || strings.TrimSpace(res.Stderr) == "" {
			t.Errorf("result = %+v, err = %v; want a non-zero exit with cmd.exe's message", res, err)
		}
		t.Logf("a missing program reads: exit %d, %q", res.ExitCode, strings.TrimSpace(res.Stderr))
	})

	t.Run("no message holds a numeric character reference", func(t *testing.T) {
		for _, doc := range rec.sent {
			if strings.Contains(doc, "&#") {
				t.Fatalf("a message holds a numeric character reference, which the service does not decode: %s", doc)
			}
		}
		t.Logf("%d messages captured, none with a numeric character reference", len(rec.sent))
	})

	t.Run("measure: the profile a command sees", func(t *testing.T) {
		for _, noProfile := range []bool{false, true} {
			o := opts
			o.NoProfile = noProfile
			res, err := executeRecorded(ctx, t, rec, target, auth, Command{Shell: ShellPowerShell, Script: "$env:USERPROFILE"}, o)
			if err != nil {
				t.Fatalf("NoProfile=%v: %v", noProfile, err)
			}
			t.Logf("NoProfile=%v: USERPROFILE=%s", noProfile, strings.TrimSpace(res.Stdout))
		}
	})
}

// TestModesStress sends a burst of real commands, one after another and
// then four at a time, and reports latency. Every command must succeed:
// this is the check that nothing in the exchange (message IDs, the
// transport a client holds, shell cleanup) misbehaves under repetition
// or concurrency, and the numbers are the per-command cost on a real host.
func TestModesStress(t *testing.T) {
	target, auth, opts := gateHost(t)
	whoami, err := CommandLine(`C:\Windows\System32\whoami.exe`)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range stressWorkloads(whoami) {
		t.Logf("%-32s %v", w.label, measureExecute(t, target, auth, opts, w.n, w.workers, w.cmd))
	}
}

// stressWorkload is one row of the stress gate: a command, how many
// times to run it, and how many at once.
type stressWorkload struct {
	label      string
	n, workers int
	cmd        Command
}

// stressWorkloads is the stress gate's table, shared with
// TestModesComparedToPywinrm so the comparison runs exactly the work the
// published numbers describe.
func stressWorkloads(whoami string) []stressWorkload {
	return []stressWorkload{
		{"none, sequential", 40, 1, Command{Shell: ShellNone, Script: whoami}},
		{"none, 4 at a time", 40, 4, Command{Shell: ShellNone, Script: whoami}},
		{"powershell, sequential", 20, 1, Command{Shell: ShellPowerShell, Script: "$env:USERNAME"}},
		{"cmd, 4 at a time", 20, 4, Command{Shell: ShellCmd, Script: "ver"}},
	}
}

// TestModesReleaseGate_ASilentMinute runs a command that writes nothing
// for longer than a minute, which is when a real host answers a waiting
// Receive with its TimedOut fault; shorter silences never draw it, so no
// quick command could catch what this does. Over the certificate
// transport the fault once arrived cut short, was taken for a failure, and
// ended every such command at sixty seconds: an installer, an import, an
// update. It takes about seventy-five seconds.
func TestModesReleaseGate_ASilentMinute(t *testing.T) {
	target, auth, opts := gateHost(t)
	opts.Timeout = 3 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	res, err := Run(ctx, target, auth, ShellPowerShell, "Start-Sleep 75; 'slept 75'", opts)
	if err != nil {
		t.Fatalf("a command silent for 75 seconds: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "slept 75" || res.ExitCode != 0 {
		t.Errorf("result = %+v", res)
	}
}
