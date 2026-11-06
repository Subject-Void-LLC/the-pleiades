// Command doctor says what this machine can and cannot verify of The
// Pleiades, and how to fix each gap (Phase 118).
//
// Before it, the only way to learn that a machine could not run the gate
// was to run the gate: an hour in, a container package failed because no
// Docker daemon answered, or a pinned scanner was the wrong version, or
// the hooks were never installed and nothing had checked anything. doctor
// asks each question in seconds and prints one line per answer:
//
//	ok    the thing works
//	fix   it does not, and the line says the command that fixes it
//	info  optional: a gate this machine will skip, and what would run it
//
// It exits non-zero only when a required check says fix, so a script can
// gate on it; an info line never fails it.
//
// Usage: go run ./tools/doctor   (or: make doctor)
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// line is one answer: its status, what was checked, and what to do.
type line struct {
	status string // "ok", "fix" or "info"
	what   string
	detail string
}

func main() {
	var lines []line
	lines = append(lines, checkGo()...)
	lines = append(lines, checkDocker())
	lines = append(lines, checkPortRange()...)
	lines = append(lines, checkTools()...)
	lines = append(lines, checkHooks())
	lines = append(lines, checkOptional()...)

	failed := false
	for _, l := range lines {
		fmt.Printf("%-5s %-28s %s\n", l.status, l.what, l.detail)
		if l.status == "fix" {
			failed = true
		}
	}
	fmt.Println()
	if failed {
		fmt.Println("doctor: fix the lines marked fix, then run it again.")
		os.Exit(1)
	}
	fmt.Println("doctor: this machine can run the gate. `make ci-fast` needs no Docker; `make push-gate` runs everything.")
}

// run, lookPath, getenv and goVersion are how the checks ask the machine. Tests
// replace them to answer both ways, so every branch is proved on whatever
// machine runs the tests, not only the branches that machine happens to
// take.
var (
	run       = output
	lookPath  = exec.LookPath
	getenv    = os.Getenv
	goVersion = runtime.Version
	goos      = runtime.GOOS
)

// output runs a command with a short deadline and returns its trimmed
// standard output, so one hung daemon cannot hang the doctor.
func output(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// #nosec G204 -- fixed commands named in this file.
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// checkGo compares the running toolchain with the one go.mod asks for.
func checkGo() []line {
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		return []line{{"fix", "go.mod", "run this from the repository root: " + err.Error()}}
	}
	need := requiredToolchain(string(raw))
	have := goVersion()
	if !atLeast(have, need) {
		return []line{{"fix", "Go toolchain", fmt.Sprintf("%s, and go.mod asks for %s: install it, or leave GOTOOLCHAIN=auto so go fetches it", have, need)}}
	}
	return []line{{"ok", "Go toolchain", fmt.Sprintf("%s (go.mod asks for %s)", have, need)}}
}

// checkDocker asks the daemon for its version. Without one the container
// tier cannot run, which is a fix for the full gate and not for the fast
// tier, so it says both.
func checkDocker() line {
	version, err := run("docker", "info", "--format", "{{.ServerVersion}}")
	if err != nil || version == "" {
		return line{"fix", "Docker daemon", "not reachable, so the 31 container packages cannot run: start Docker, or use `make ci-fast`, which needs none"}
	}
	return line{"ok", "Docker daemon", "server " + version}
}

// dockerDesktopPorts is the lowest host port Docker Desktop publishes a
// container's port on (measured 2026-11-06: 32768 up to 65535).
const dockerDesktopPorts = 32768

// checkPortRange asks, where the engine is Docker Desktop's on Linux (WSL),
// whether this machine's ephemeral ports sit below the ones Docker Desktop
// publishes. Docker Desktop's engine picks a published port in a network
// namespace of its own, and its forwarder then binds the port in this one;
// when a socket of this machine's own already has it, the bind fails, is
// logged as a warning and never retried, and the container's port refuses
// every connection for its life (FAILURE_PATTERNS 470, 471 and 481). A
// native engine binds where the tests run and picks another port, so it
// needs nothing, and nor does Docker Desktop on macOS, whose tests do not
// share a namespace with the forwarder's.
func checkPortRange() []line {
	engine, err := run("docker", "info", "--format", "{{.OperatingSystem}}")
	if err != nil || engine == "" {
		return nil // checkDocker already says the daemon is unreachable
	}
	if goos != "linux" || !strings.Contains(engine, "Docker Desktop") {
		return []line{{"ok", "published ports", "the engine publishes ports where the tests run (" + engine + ")"}}
	}
	raw, err := run("cat", "/proc/sys/net/ipv4/ip_local_port_range")
	lo, hi, ok := portRange(raw)
	if err != nil || !ok {
		return []line{{"fix", "published ports", "could not read /proc/sys/net/ipv4/ip_local_port_range, so whether a test's own socket can take a container's published port is unknown"}}
	}
	if hi >= dockerDesktopPorts {
		return []line{{"fix", "published ports", fmt.Sprintf("ephemeral ports %d-%d overlap the ones Docker Desktop publishes (from %d), so a container's port can be lost to a test's own socket and refuse for the container's life: "+
			"echo 'net.ipv4.ip_local_port_range = 20000 32767' | sudo tee /etc/sysctl.d/60-pleiades-docker-ports.conf && sudo sysctl -p /etc/sysctl.d/60-pleiades-docker-ports.conf", lo, hi, dockerDesktopPorts)}}
	}
	return []line{{"ok", "published ports", fmt.Sprintf("ephemeral ports %d-%d sit below Docker Desktop's (from %d)", lo, hi, dockerDesktopPorts)}}
}

// checkTools compares each pinned scanner on PATH with the Makefile's pin.
func checkTools() []line {
	raw, err := os.ReadFile("Makefile")
	if err != nil {
		return []line{{"fix", "Makefile", err.Error()}}
	}
	pins := pinnedTools(string(raw))
	var out []line
	for _, tool := range []string{"gosec", "govulncheck", "actionlint"} {
		want := pins[tool]
		path, err := lookPath(tool)
		if err != nil {
			out = append(out, line{"fix", tool, fmt.Sprintf("not on PATH: run `make tools` (pinned %s), and put $(go env GOPATH)/bin on PATH", want)})
			continue
		}
		modinfo, _ := run("go", "version", "-m", path)
		have := moduleVersion(modinfo)
		if have != want {
			out = append(out, line{"fix", tool, fmt.Sprintf("%s, pinned %s: run `make tools`", orNone(have), want)})
			continue
		}
		out = append(out, line{"ok", tool, want})
	}
	return out
}

// checkHooks asks whether this clone's hooks are the repository's.
func checkHooks() line {
	path, _ := run("git", "config", "core.hooksPath")
	if path != ".githooks" {
		return line{"fix", "git hooks", "not installed, so commits and pushes are unchecked here: run `make hooks`"}
	}
	return line{"ok", "git hooks", ".githooks"}
}

// checkOptional names the gates this machine will skip and what would run
// each one. None of these fails the doctor.
func checkOptional() []line {
	var out []line
	if _, err := run("gopls", "version"); err != nil {
		out = append(out, line{"info", "gopls", "not installed; agents working here need it (go install golang.org/x/tools/gopls@latest, then make lsp)"})
	} else {
		out = append(out, line{"ok", "gopls", "installed"})
	}
	if _, err := run("python3", "-c", "import winrm"); err != nil {
		out = append(out, line{"info", "pywinrm", "absent: the WinRM comparison against pywinrm and Ansible skips (python3 -m pip install --user pywinrm)"})
	} else {
		out = append(out, line{"ok", "pywinrm", "the WinRM comparison can run"})
	}
	for _, gate := range realHostGates {
		if getenv(gate.env) == "" {
			out = append(out, line{"info", gate.name, "skips: set " + gate.env + " (" + gate.what + ")"})
		} else {
			out = append(out, line{"ok", gate.name, gate.env + " is set"})
		}
	}
	return out
}

// realHostGates are the gates that need something no container provides.
var realHostGates = []struct{ name, env, what string }{
	{"WinRM gates", "PLEIADES_WINRM_HOST", "a Windows host with the certificate listener examples/windows_lab sets up"},
	{"lab project gates", "PLEIADES_WINRM_PROJECT", "the VirtualBox lab project, run through pleiades"},
	{"ServiceNow gate", "PLEIADES_SNOW_INSTANCE", "a ServiceNow developer instance"},
}

// orNone names an empty version.
func orNone(v string) string {
	if v == "" {
		return "unknown version"
	}
	return v
}
