// Timer installation for the sweep tool.
//
// This WSL distribution boots systemd, which /etc/wsl.conf asks for with
// `systemd=true`, so a systemd user timer is the scheduler that actually
// fires here. A user timer is preferred over a root cron entry for two
// reasons: the sweep only ever touches files under the invoking user's own
// checkout, so it needs no privilege, and `systemctl --user list-timers`
// shows the next run time, which a crontab line does not.
//
// One WSL-specific detail matters. A user manager normally exits when the
// last session for that user closes, which would stop the timer from ever
// firing on a machine somebody opens a shell on only occasionally.
// `loginctl enable-linger` is what keeps the manager alive, and the
// installer runs it rather than leaving it as a step in a comment nobody
// reads.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// timerUnit is the systemd timer that schedules the sweep. OnCalendar
// fires weekly rather than daily because the default age threshold is
// seven days, so a daily run would spend six of every seven days finding
// nothing. Persistent=true makes a run that was missed while the machine
// was off happen at the next start, which on WSL is the normal case.
const timerUnit = `[Unit]
Description=Weekly sweep of stale build output in %s

[Timer]
OnCalendar=weekly
Persistent=true
RandomizedDelaySec=1h

[Install]
WantedBy=timers.target
`

// serviceUnit is the one-shot service the timer starts. It runs the
// already-built binary from a fixed path rather than `go run`, so a sweep
// triggered while the module is mid-edit cannot fail to compile, and so
// the sweep itself does not add to the build cache it is meant to help
// keep small.
const serviceUnit = `[Unit]
Description=Sweep stale build output in %s

[Service]
Type=oneshot
WorkingDirectory=%s
ExecStart=%s -max-age=%s
Nice=10
IOSchedulingClass=idle
`

// installTimer writes the unit files, reloads the user manager, enables
// the timer and turns on lingering. It returns the path the sweep binary
// was installed to, since the service unit hard-codes it.
func installTimer(root string, maxAgeFlag string) error {
	unitDir := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", unitDir, err)
	}

	// Build the sweep binary to a stable location. The service runs this
	// copy, so editing or breaking the source later cannot stop the
	// scheduled sweep from working.
	binDir := filepath.Join(os.Getenv("HOME"), ".local", "bin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", binDir, err)
	}
	binPath := filepath.Join(binDir, "pleiades-sweep")
	build := exec.Command("go", "build", "-o", binPath, "./tools/sweep")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building the sweep binary: %w: %s", err, out)
	}

	service := fmt.Sprintf(serviceUnit, root, root, binPath, maxAgeFlag)
	if err := os.WriteFile(filepath.Join(unitDir, "pleiades-sweep.service"), []byte(service), 0o600); err != nil {
		return fmt.Errorf("writing the service unit: %w", err)
	}
	timer := fmt.Sprintf(timerUnit, root)
	if err := os.WriteFile(filepath.Join(unitDir, "pleiades-sweep.timer"), []byte(timer), 0o600); err != nil {
		return fmt.Errorf("writing the timer unit: %w", err)
	}

	// Each systemctl call is spelled out rather than driven from a slice
	// of argument lists, so a failure names the step that failed instead
	// of echoing an argument vector back at the reader.
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("reloading the user manager: %w: %s", err, out)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", "pleiades-sweep.timer").CombinedOutput(); err != nil {
		return fmt.Errorf("enabling the weekly timer: %w: %s", err, out)
	}

	// Without lingering the user manager stops when the last shell closes,
	// and a timer in a stopped manager never fires.
	if out, err := exec.Command("loginctl", "enable-linger", os.Getenv("USER")).CombinedOutput(); err != nil {
		fmt.Printf("sweep: could not enable lingering (%v: %s)\n", err, out)
		fmt.Println("sweep: the timer will only fire while you have a shell open; run 'sudo loginctl enable-linger $USER' to fix that")
	}

	fmt.Printf("sweep: installed a weekly timer running %s -max-age=%s in %s\n", binPath, maxAgeFlag, root)
	fmt.Println("sweep: check it with 'systemctl --user list-timers pleiades-sweep.timer'")
	return nil
}

// removeTimer stops the timer and deletes both unit files. It leaves the
// installed binary in place, since removing a scheduled run is not a
// reason to take away the command.
func removeTimer() error {
	// Reported rather than returned: a timer that is already gone, or a
	// user manager that is not running, is the state this function is
	// trying to reach, so neither is a reason to stop before deleting the
	// unit files.
	if out, err := exec.Command("systemctl", "--user", "disable", "--now", "pleiades-sweep.timer").CombinedOutput(); err != nil {
		fmt.Printf("sweep: could not disable the timer (%v: %s)\n", err, out)
	}

	unitDir := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	for _, name := range []string{"pleiades-sweep.timer", "pleiades-sweep.service"} {
		if err := os.Remove(filepath.Join(unitDir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("reloading the user manager: %w: %s", err, out)
	}

	fmt.Println("sweep: removed the weekly timer; 'make sweep' still works by hand")
	return nil
}
