// Behavior gate for `pleiades forge migrate-playbook`: one playbook of
// file and command tasks, run by real Ansible and, converted, by
// `pleiades run` against a real sshd, must leave the same files behind.
package main_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// behaviorPlaybook builds a small tree under /tmp/mgate with every file
// module the converter maps and a guarded shell command, each with the
// arguments a playbook commonly passes.
const behaviorPlaybook = `- hosts: web
  gather_facts: false
  tasks:
    - name: start clean
      ansible.builtin.file: {path: /tmp/mgate, state: absent}
    - name: make the tree
      ansible.builtin.file: {path: /tmp/mgate/app/conf, state: directory, mode: "0750"}
    - name: write the config
      ansible.builtin.copy:
        dest: /tmp/mgate/app/conf/app.conf
        content: "a=1\nkeep=yes\n"
        mode: "0640"
    - name: change a line
      ansible.builtin.lineinfile: {path: /tmp/mgate/app/conf/app.conf, regexp: "^a=", line: "a=3"}
    - name: add a line
      ansible.builtin.lineinfile: {path: /tmp/mgate/app/conf/app.conf, line: "b=2"}
    - name: drop a line
      ansible.builtin.lineinfile: {path: /tmp/mgate/app/conf/app.conf, regexp: "^keep=", state: absent}
    - name: add a block
      ansible.builtin.blockinfile:
        path: /tmp/mgate/app/conf/app.conf
        block: "x=1\ny=2"
    - name: link to it
      ansible.builtin.file: {src: /tmp/mgate/app/conf/app.conf, dest: /tmp/mgate/current, state: link}
    - name: touch a stamp
      ansible.builtin.file: {path: /tmp/mgate/app/stamp, state: touch, mode: "0600"}
    - name: make and remove a scratch tree
      ansible.builtin.file: {path: /tmp/mgate/scratch/deep, state: directory, mode: "0700"}
    - name: remove it
      ansible.builtin.file: {path: /tmp/mgate/scratch, state: absent}
    - name: run a guarded command
      ansible.builtin.shell: echo built > built.txt && chmod 0604 built.txt
      args: {chdir: /tmp/mgate/app, creates: built.txt}
    - name: set its mode
      ansible.builtin.file: {path: /tmp/mgate/app/built.txt, mode: "0644"}
`

// treeListing prints every path under /tmp/mgate with its kind, mode and
// content (base64, so every byte compares), in path order.
const treeListing = `cd /tmp/mgate && find . | sort | while read -r p; do
  if [ -L "$p" ]; then echo "$p link $(readlink "$p")";
  elif [ -d "$p" ]; then echo "$p dir $(stat -c %a "$p")";
  else echo "$p file $(stat -c %a "$p") $(base64 < "$p" | tr -d '\n')"; fi
done`

// TestMigratePlaybook_BehaviorMatchesAnsible runs behaviorPlaybook with
// ansible-playbook in the pinned runner image, against that container
// itself, and runs its conversion with the real pleiades binary against a
// real sshd; the two trees must be identical in kind, mode and content.
func TestMigratePlaybook_BehaviorMatchesAnsible(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the migrate-playbook behavior gate, which runs real Ansible and a real sshd, in short mode")
	}
	image := testsupport.BuildAnsibleRunnerImage(t)
	dir := t.TempDir()
	writeFile(t, dir, "site.yml", behaviorPlaybook)

	// Ansible runs the playbook against its own container, which has the
	// Python its modules need; the sshd image has none.
	script := `printf 'web ansible_connection=local ansible_python_interpreter=/usr/local/bin/python3\n' > /tmp/inventory &&
ansible-playbook -i /tmp/inventory /work/site.yml >/tmp/ansible.log 2>&1 || { cat /tmp/ansible.log; exit 1; }
echo '=== tree'
` + treeListing
	out, err := exec.Command("docker", "run", "--rm", "-v", dir+":/work:ro", image, "sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("ansible-playbook: %v\n%s", err, out)
	}
	_, ansibleTree, found := strings.Cut(string(out), "=== tree\n")
	if !found {
		t.Fatalf("no tree listing from the Ansible side:\n%s", out)
	}

	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(homeDir, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"add-host", "web", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "web", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithHome(t, dir, homeDir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// A review (file.permissions refusing a link) is expected; a blocked
	// task would leave nothing runnable to compare.
	if stdout, stderr, _ := runMigrate(t, dir, "site.yml"); !strings.Contains(stdout, ", 0 blocked") {
		t.Fatalf("the conversion blocked a task, so it cannot be compared as it stands:\n%s%s", stdout, stderr)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/site.yaml"); err != nil {
		t.Fatalf("pleiades run: %v\n%s", err, out)
	}
	pleiadesTree := verifyOverSSH(t, addr, treeListing)

	if strings.TrimSpace(ansibleTree) != strings.TrimSpace(pleiadesTree) {
		t.Errorf("the trees differ\nAnsible:\n%s\npleiades:\n%s", ansibleTree, pleiadesTree)
	}
	if !strings.Contains(pleiadesTree, "./app/conf/app.conf file 640") {
		t.Errorf("the listing does not hold the converted config, so the comparison proved nothing:\n%s", pleiadesTree)
	}
}
