// The release gate for async and poll: runbooks run through the real binary
// against a real Debian device. A background job runs while a later task does
// and is collected by async_status; a task past its async limit is stopped on
// the device, the child process it started included. What ran, in what order,
// and what is still running are read back from the device itself.
package main_test

import (
	"strings"
	"testing"
	"time"
)

// asyncGateBackground starts a job that writes after a later task does, and
// collects it.
const asyncGateBackground = `# Appends two lines to /tmp/ag.order, one from a background job.
# Blast radius: the gate's one device.
# Changes: /tmp/ag.order.
# Undo: none recorded; delete the file by hand.
id: async-gate-background
hosts: debian
tasks:
  - name: slow write in the background
    exec.shell:
      cmd: sleep 3; echo built >> /tmp/ag.order
    async: 60
    poll: 0
    register: build
  - name: meanwhile
    exec.shell:
      cmd: echo meanwhile >> /tmp/ag.order
  - name: collect the job
    async_status:
      jid: "{{ result.build.ansible_job_id }}"
`

// asyncGateLimit starts a child process and waits for it past its limit.
const asyncGateLimit = `# Starts a long sleep under a three-second limit, which stops it.
# Blast radius: the gate's one device.
# Changes: /tmp/ag.child (the sleep's pid); /tmp/ag.after only if the limit failed.
# Undo: none recorded; delete the files by hand.
id: async-gate-limit
hosts: debian
tasks:
  - name: too slow
    exec.shell:
      cmd: sleep 60 & echo $! > /tmp/ag.child; wait; touch /tmp/ag.after
    async: 3
    poll: 1
`

// timeoutGate is a task whose child holds its output past its timeout, which
// Debian's sshd does not end on the client's signal or close.
const timeoutGate = `# Runs a background sleep under a three-second task timeout.
# Blast radius: the gate's one device.
# Changes: nothing; the sleep may run on to its end.
# Undo: none needed.
id: timeout-gate
hosts: debian
tasks:
  - name: holds its output
    exec.shell:
      cmd: sleep 30 & wait
    timeout: 3
`

// TestCLI_AsyncRunsInTheBackgroundAndItsLimitStopsTheDevicesWork: a plain
// task's timeout fails it promptly; the background job's line lands after the later task's, async_status waits for
// it, and nothing is left in a job directory; past its limit a task fails
// naming the limit, and the sleep it started is no longer running.
func TestCLI_AsyncRunsInTheBackgroundAndItsLimitStopsTheDevicesWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the async release gate, which builds and runs a real Debian sshd, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")
	writeFile(t, dir, "runbooks/background.yaml", asyncGateBackground)
	writeFile(t, dir, "runbooks/limit.yaml", asyncGateLimit)
	writeFile(t, dir, "runbooks/timeout.yaml", timeoutGate)

	// A plain task's timeout ends the task promptly, though the device's sshd
	// waits for the command's output to end.
	began := time.Now()
	out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/timeout.yaml")
	if err == nil || time.Since(began) > 20*time.Second {
		t.Fatalf("a task past its timeout should fail promptly (err %v, took %s):\n%s", err, time.Since(began), out)
	}

	out, err = runPleiadesWithHome(t, dir, home, "run", "runbooks/background.yaml")
	if err != nil {
		t.Fatalf("background run: %v\n%s", err, out)
	}
	if got, code := device.exec(t, "cat", "/tmp/ag.order"); code != 0 || got != "meanwhile\nbuilt" {
		t.Fatalf("the device's order is %q (exit %d), want meanwhile then built:\n%s", got, code, out)
	}
	leftovers := []string{"sh", "-c", "ls -d /root/.pleiades-async-*/job.* /home/*/.pleiades-async-*/job.* 2>/dev/null"}
	if got, _ := device.exec(t, leftovers...); strings.TrimSpace(got) != "" {
		t.Fatalf("job directories left on the device: %s", got)
	}

	out, err = runPleiadesWithHome(t, dir, home, "run", "runbooks/limit.yaml")
	if err == nil || !strings.Contains(out, "async limit of 3s") {
		t.Fatalf("a run past its async limit should fail naming it (err %v):\n%s", err, out)
	}
	stopped := `p=$(cat /tmp/ag.child) && { [ ! -d /proc/$p ] || grep -q ') Z ' /proc/$p/stat; }`
	if _, code := device.exec(t, "sh", "-c", stopped); code != 0 {
		got, _ := device.exec(t, "sh", "-c", "ps -ef")
		t.Fatalf("the sleep the stopped task started is still running:\n%s\n%s", got, out)
	}
	if _, code := device.exec(t, "test", "-e", "/tmp/ag.after"); code == 0 {
		t.Fatal("the stopped task ran on to its end")
	}
	if got, _ := device.exec(t, leftovers...); strings.TrimSpace(got) != "" {
		t.Fatalf("job directories left on the device after the stop: %s", got)
	}
}
