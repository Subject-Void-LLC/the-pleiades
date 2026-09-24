// Benchmark for converting a large playbook.
package playbook_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// largePlaybook returns plays plays on one host group, each holding tasks
// tasks that cycle through the common shapes: a module with a map of
// arguments, one with free-form arguments, a condition over a variable, a
// short loop and a registered result a later condition reads.
func largePlaybook(plays, tasks int) string {
	var b strings.Builder
	for p := range plays {
		fmt.Fprintf(&b, "- name: play %d\n  hosts: web\n  gather_facts: false\n  vars: {base%d: /srv/p%d, on%d: true}\n  tasks:\n", p, p, p, p)
		for i := range tasks {
			switch i % 5 {
			case 0:
				fmt.Fprintf(&b, "    - name: dir %d\n      ansible.builtin.file: {path: \"{{ base%d }}/d%d\", state: directory, mode: \"0755\"}\n", i, p, i)
			case 1:
				fmt.Fprintf(&b, "    - name: pkg %d\n      ansible.builtin.apt: name=tool%d state=present\n", i, i)
			case 2:
				fmt.Fprintf(&b, "    - name: line %d\n      ansible.builtin.lineinfile: {path: /etc/app%d.conf, line: \"k%d=v\"}\n      when: on%d\n", i, p, i, p)
			case 3:
				fmt.Fprintf(&b, "    - name: loop %d\n      ansible.builtin.command: echo {{ item }}\n      loop: [a, b]\n", i)
			case 4:
				fmt.Fprintf(&b, "    - name: probe %d\n      ansible.builtin.command: test -d /srv\n      register: r%d_%d\n", i, p, i)
				fmt.Fprintf(&b, "    - name: after %d\n      ansible.builtin.service: {name: app%d, state: started}\n      when: r%d_%d.rc == 0\n", i, i, p, i)
			}
		}
	}
	return b.String()
}

// BenchmarkTranslate_LargePlaybook converts 50 plays of 100 task entries.
// Set PLAYBOOK_BENCH_OUT to a path to write the playbook there too, so the
// same file can be timed through ansible-playbook.
func BenchmarkTranslate_LargePlaybook(b *testing.B) {
	source := largePlaybook(50, 100)
	if out := os.Getenv("PLAYBOOK_BENCH_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(source), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	fsys := fstest.MapFS{"site.yml": {Data: []byte(source)}}
	b.SetBytes(int64(len(source)))
	b.ReportAllocs()
	for b.Loop() {
		res, err := playbook.Translate(fsys, "site.yml", playbook.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if res.Report.Counts.Blocked != 0 {
			b.Fatalf("%d tasks blocked; the benchmark would time refusals", res.Report.Counts.Blocked)
		}
	}
}
