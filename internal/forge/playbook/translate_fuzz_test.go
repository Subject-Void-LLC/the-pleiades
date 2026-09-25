// Fuzz test for the whole conversion.
package playbook_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// FuzzTranslate converts arbitrary playbooks. A conversion may refuse its
// input, but it must never reach its own panic boundary (an "internal
// error" is a panic it caught), and every runbook it writes must build,
// which Translate proves before returning one.
func FuzzTranslate(f *testing.F) {
	seeds := []string{
		"- hosts: web\n  tasks:\n    - command: /bin/true\n",
		"- hosts: web\n",
		"- hosts: web\n  tasks: {}\n",
		"- hosts: web\n  tasks:\n    - block:\n        - block:\n            - block:\n                - command: x\n          rescue:\n            - command: y\n",
		"- hosts: web\n  tasks:\n    - frobnicate: {a: 1}\n    - apt: name=x\n",
		"- hosts: web\n  vars: {a: &x [1, 2]}\n  tasks:\n    - command: echo {{ item }}\n      loop: *x\n",
		"- hosts: web\n  tasks:\n    - <<: {command: x}\n      name: merged\n",
		"- hosts: web\n  vars:\n    s: !vault |\n      $ANSIBLE_VAULT;1.1;AES256\n      00\n  tasks:\n    - command: echo {{ s }}\n",
		"- hosts: web\n  tasks:\n    - command: echo {% if x %}y{% endif %}\n",
		"- hosts: web\n  tasks:\n    - command: x\n      when: a and (b or not c) and d.e == 'f'\n",
		"- hosts: web\n  tasks:\n    - command: x\n      command: y\n",
		"- import_playbook: other.yml\n- 42\n",
		"not a playbook",
		"- hosts: [a, b]\n  tasks:\n    - import_tasks: self.yml\n",
		"\xff\xfe- hosts: web",
		"- hosts: web\n  tasks:\n    - command: x\n      loop: \"" + strings.Repeat("{{ ", 50) + "\"\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, doc string) {
		fsys := fstest.MapFS{"pb.yml": {Data: []byte(doc)}, "self.yml": {Data: []byte("- import_tasks: self.yml\n")}}
		_, err := playbook.Translate(fsys, "pb.yml", playbook.Options{})
		if err != nil && strings.Contains(err.Error(), "internal error") {
			t.Fatalf("a panic was caught converting %q: %v", doc, err)
		}
	})
}
