// The module tables as a test: what each mapped module becomes, the
// parameters it passes, and the finding each difference from Ansible
// raises.
package playbook_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
)

// convertTask translates a one-task play and returns its report.
func convertTask(t *testing.T, task string) playbook.Report {
	t.Helper()
	return convert(t, "- hosts: web\n  gather_facts: false\n  tasks:\n    - "+task+"\n").Report
}

// converted returns the report's converted tasks.
func converted(r playbook.Report) []playbook.TaskResult {
	var out []playbook.TaskResult
	for _, task := range r.Tasks {
		if task.Outcome != playbook.OutcomeBlocked {
			out = append(out, task)
		}
	}
	return out
}

// fqcns returns tasks' methods, in order.
func fqcns(tasks []playbook.TaskResult) []string {
	var out []string
	for _, task := range tasks {
		out = append(out, task.FQCN)
	}
	return out
}

// TestModules_Map converts one task per table behavior.
func TestModules_Map(t *testing.T) {
	for _, tc := range []struct {
		name, task string
		// fqcns are the converted tasks' methods; none means blocked.
		fqcns []string
		// params are checked on the first converted task.
		params map[string]any
		code   playbook.Code
		class  playbook.Class
	}{
		{name: "apt list", task: "{apt: {name: [curl, git]}}", fqcns: []string{"pkg.apt.install", "pkg.apt.install"}, code: "args.list_unrolled"},
		{name: "apt update_cache", task: "{apt: {name: curl, update_cache: yes}}", fqcns: []string{"pkg.apt.install"}, code: "module.semantics"},
		{name: "yum alias", task: "{yum: {name: curl, state: installed}}", fqcns: []string{"pkg.dnf.install"}, class: playbook.ClassAsserted},
		{name: "apt free-form", task: "{apt: name=curl state=absent}", fqcns: []string{"pkg.apt.remove"}, params: map[string]any{"name": "curl"}},
		{name: "apt comma names", task: "{apt: 'name=curl,git'}", fqcns: []string{"pkg.apt.install", "pkg.apt.install"}, code: "args.list_unrolled"},
		{name: "apt one-item list", task: "{apt: {name: [curl]}}", fqcns: []string{"pkg.apt.install"}, params: map[string]any{"name": "curl"}},
		{name: "apt empty name", task: "{apt: {name: 'curl,,git'}}", code: "args.value"},
		{name: "apt empty list", task: "{apt: {name: []}}", code: "args.value"},
		{name: "apt ambiguous item", task: "{apt: {name: [curl, yes]}}", code: "args.value"},
		{name: "apt force", task: "{apt: {name: curl, force: yes}}", code: "args.unmapped"},
		{name: "service reloaded", task: "{service: {name: nginx, state: reloaded}}", code: "args.value"},
		{name: "service restarted", task: "{service: {name: nginx, state: restarted}}", fqcns: []string{"svc.restart"}, class: playbook.ClassImperative},
		{name: "service templated state", task: "{service: {name: nginx, state: \"{{ s }}\"}}", code: "state.computed"},
		{name: "systemd reload only", task: "{systemd: {name: nginx, daemon_reload: yes}}", fqcns: []string{"svc.systemd.daemon_reload"}, code: "args.ignored"},
		{name: "service enabled quoted", task: "{service: {name: nginx, enabled: 'yes'}}", fqcns: []string{"svc.enable"}},
		{name: "service enabled number", task: "{service: {name: nginx, enabled: 0}}", fqcns: []string{"svc.disable"}},
		{name: "service enabled word", task: "{service: {name: nginx, enabled: maybe}}", code: "args.value"},
		{name: "systemd reload false", task: "{systemd: {name: nginx, daemon_reload: no, state: stopped}}", fqcns: []string{"svc.systemd.stop"}},
		{name: "win_service", task: "{win_service: {name: spooler, state: started}}", fqcns: []string{"svc.windows.start"}},
		{name: "win_service start_mode", task: "{win_service: {name: spooler, start_mode: auto}}", code: "args.unmapped"},
		{name: "file absent", task: "{file: {path: /x, state: absent, mode: '0644'}}", fqcns: []string{"file.remove"}, params: map[string]any{"path": "/x", "recurse": true}, code: "args.ignored"},
		{name: "file directory", task: "{file: {path: /x, state: directory, mode: '0755'}}", fqcns: []string{"file.directory"}, params: map[string]any{"path": "/x", "mode": "0755"}},
		{name: "file directory octal", task: "{file: {path: /x, state: directory, mode: 0755}}", fqcns: []string{"file.directory"}, params: map[string]any{"mode": "0755"}},
		{name: "file touch", task: "{file: {path: /x, state: touch}}", fqcns: []string{"file.touch"}, class: playbook.ClassImperative},
		{name: "file link", task: "{file: {src: /a, dest: /b, state: link}}", fqcns: []string{"file.symlink"}, params: map[string]any{"src": "/a", "path": "/b"}},
		{name: "file link owner", task: "{file: {src: /a, dest: /b, state: link, owner: root}}", code: "args.unmapped"},
		{name: "file hard", task: "{file: {src: /a, dest: /b, state: hard}}", code: "args.value"},
		{name: "file attributes", task: "{file: {path: /x, owner: root}}", fqcns: []string{"file.permissions"}, code: "module.semantics"},
		{name: "file recurse", task: "{file: {path: /x, state: directory, recurse: yes}}", code: "args.unmapped"},
		{name: "copy content", task: "{copy: {dest: /x, content: hi, mode: '0644'}}", fqcns: []string{"file.copy"}, params: map[string]any{"dest": "/x", "content": "hi", "mode": "0644"}},
		{name: "copy no mode", task: "{copy: {dest: /x, content: hi}}", fqcns: []string{"file.copy"}, code: "module.semantics"},
		{name: "copy src", task: "{copy: {src: a.conf, dest: /x}}", code: "args.unmapped"},
		{name: "lineinfile", task: "{lineinfile: {path: /x, regexp: '^a=', line: a=1}}", fqcns: []string{"file.line.set"}, params: map[string]any{"regexp": "^a=", "line": "a=1"}},
		{name: "blockinfile absent", task: "{blockinfile: {path: /x, state: absent}}", fqcns: []string{"file.block.remove"}},
		{name: "user", task: "{user: {name: app, shell: /bin/bash, uid: 1001}}", fqcns: []string{"identity.user.create"}, params: map[string]any{"uid": int64(1001)}},
		{name: "user absent", task: "{user: {name: app, state: absent, shell: /bin/bash, remove: yes}}", fqcns: []string{"identity.user.remove"}, params: map[string]any{"remove": true}, code: "args.ignored"},
		{name: "user password", task: "{user: {name: app, password: x}}", code: "template.secret"},
		{name: "user groups", task: "{user: {name: app, groups: [wheel]}}", code: "args.unmapped"},
		{name: "group", task: "{group: {name: app, gid: 900}}", fqcns: []string{"identity.group.create"}, params: map[string]any{"gid": int64(900)}},
		{name: "unarchive from controller", task: "{unarchive: {src: a.tgz, dest: /x}}", code: "module.manual"},
		{name: "unarchive remote", task: "{unarchive: {src: /tmp/a.tgz, dest: /x, remote_src: yes}}", fqcns: []string{"archive.extract"}},
		{name: "mount", task: "{mount: {path: /m, src: /dev/sdb1, fstype: ext4, state: mounted}}", fqcns: []string{"fs.mount"}, params: map[string]any{"persist": true}, code: "module.semantics"},
		{name: "mount ephemeral", task: "{mount: {path: /m, src: /dev/sdb1, fstype: ext4, state: ephemeral}}", fqcns: []string{"fs.mount"}, params: map[string]any{"persist": false}},
		{name: "mount unmounted", task: "{mount: {path: /m, src: /dev/sdb1, fstype: ext4, state: unmounted}}", fqcns: []string{"fs.unmount"}, params: map[string]any{"persist": false}, code: "args.ignored"},
		{name: "mount absent", task: "{mount: {path: /m, src: /dev/sdb1, state: absent}}", fqcns: []string{"fs.unmount"}, params: map[string]any{"persist": true}, code: "module.semantics"},
		{name: "mount present", task: "{mount: {path: /m, src: /dev/sdb1, fstype: ext4, state: present}}", code: "args.value"},
		{name: "mount passno", task: "{mount: {path: /m, src: /dev/sdb1, fstype: ext4, state: mounted, passno: 2}}", code: "args.unmapped"},
		{name: "wait_for port", task: "{wait_for: {port: 8080, timeout: 30}}", fqcns: []string{"pleiades.builtin.wait.port"}, params: map[string]any{"port": int64(8080), "timeout": int64(30)}, class: playbook.ClassObserve},
		{name: "wait_for path", task: "{wait_for: {path: /x}}", code: "args.unmapped"},
		{name: "wait_for sleep only", task: "{wait_for: {timeout: 5}}", code: "args.unmapped"},
		{name: "setup subset", task: "{setup: {gather_subset: [min]}}", fqcns: []string{"facts.gather"}, code: "args.ignored"},
		{name: "firewalld service", task: "{firewalld: {service: http, state: enabled}}", fqcns: []string{"fw.firewalld.allow"}, params: map[string]any{"permanent": false, "immediate": true}},
		{name: "firewalld permanent", task: "{firewalld: {service: http, state: disabled, permanent: yes}}", fqcns: []string{"fw.firewalld.deny"}, params: map[string]any{"permanent": true, "immediate": false}},
		{name: "firewalld both", task: "{firewalld: {service: http, state: enabled, permanent: yes, immediate: yes}}", fqcns: []string{"fw.firewalld.allow"}, params: map[string]any{"permanent": true, "immediate": true}},
		{name: "firewalld t", task: "{firewalld: {service: http, state: enabled, permanent: t}}", fqcns: []string{"fw.firewalld.allow"}, params: map[string]any{"permanent": true, "immediate": false}},
		{name: "firewalld port", task: "{firewalld: {port: 8080/tcp, state: enabled}}", code: "args.value"},
		{name: "ping", task: "{ping: {}}", fqcns: []string{"net.ssh.ping"}, class: playbook.ClassObserve},
		{name: "cli_command", task: "{cli_command: {command: show version}}", fqcns: []string{"net.cli.command"}, class: playbook.ClassImperative},
		{name: "cli_command prompt", task: "{cli_command: {command: reload, prompt: confirm, answer: y}}", code: "args.unmapped"},
		{name: "cli_config", task: "{cli_config: {config: hostname r1}}", fqcns: []string{"net.cli.config"}, code: "module.semantics"},
		{name: "ios_config", task: "{ios_config: {lines: [hostname r1], save_when: modified}}", fqcns: []string{"net.ios.config"}, params: map[string]any{"lines": []any{"hostname r1"}}, code: "module.semantics", class: playbook.ClassImperative},
		{name: "ios_config one line", task: "{ios_config: {lines: hostname r1}}", fqcns: []string{"net.ios.config"}, params: map[string]any{"lines": []any{"hostname r1"}}},
		{name: "ios_config parents", task: "{ios_config: {lines: [shutdown], parents: [interface Gi1]}}", code: "args.unmapped"},
		{name: "ios_facts", task: "{ios_facts: {gather_subset: [min]}}", fqcns: []string{"net.ios.facts"}, class: playbook.ClassObserve},
		{name: "netconf_config", task: "{netconf_config: {content: '<config/>'}}", fqcns: []string{"net.netconf.config"}, params: map[string]any{"lock": "always"}, code: "module.semantics"},
		{name: "netconf_config lock", task: "{netconf_config: {content: '<config/>', lock: if-supported}}", fqcns: []string{"net.netconf.config"}, params: map[string]any{"lock": "if_supported"}},
		{name: "netconf_config target", task: "{netconf_config: {content: '<config/>', target: candidate}}", code: "netconf.target"},
		{name: "netconf_config datastore", task: "{netconf_config: {content: '<config/>', datastore: running}}", code: "netconf.target"},
		{name: "raw", task: "{raw: uptime}", fqcns: []string{"exec.shell"}, params: map[string]any{"cmd": "uptime"}, code: "module.semantics"},
		{name: "uri", task: "{uri: {url: 'http://x'}}", code: "module.manual"},
		{name: "get_url", task: "{get_url: {url: 'http://x', dest: /x}}", code: "module.manual"},
		{name: "stat", task: "{stat: {path: /x}}", code: "module.manual"},
		{name: "legacy name", task: "{ansible.legacy.file: {path: /x, state: directory}}", fqcns: []string{"file.directory"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := convertTask(t, tc.task)
			tasks := converted(r)
			if got := fqcns(tasks); !slices.Equal(got, tc.fqcns) {
				t.Fatalf("converted to %v, want %v; findings %v", got, tc.fqcns, codesOf(r))
			}
			if len(tc.fqcns) == 0 && r.Counts.Blocked != 1 {
				t.Errorf("blocked %d, want 1", r.Counts.Blocked)
			}
			if tc.code != "" && !slices.Contains(codesOf(r), tc.code) {
				t.Errorf("findings %v lack %s", codesOf(r), tc.code)
			}
			for k, want := range tc.params {
				if got := tasks[0].Params[k]; !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %#v, want %#v", k, got, want)
				}
			}
			if tc.class != playbook.ClassUnclassified && tasks[0].Class != tc.class {
				t.Errorf("class %s, want %s", tasks[0].Class, tc.class)
			}
		})
	}
}

// TestPkgLatestMapsToUpgrade pins state: latest to each package module's
// upgrade method, whose manifest defines it as Ansible's latest: upgrade
// an installed package, install a missing one.
func TestPkgLatestMapsToUpgrade(t *testing.T) {
	for module, want := range map[string]string{"package": "pkg.upgrade", "apt": "pkg.apt.upgrade", "dnf": "pkg.dnf.upgrade", "yum": "pkg.dnf.upgrade"} {
		tasks := converted(convertTask(t, "{"+module+": {name: curl, state: latest}}"))
		if got := fqcns(tasks); !slices.Equal(got, []string{want}) {
			t.Errorf("%s state=latest = %v, want %s", module, got, want)
		}
	}
}

// TestSvcStateAndEnabledSplit pins a service task giving state and
// enabled (and systemd's daemon_reload) to one native task each, reload
// first, with the unit's name on each call that takes one.
func TestSvcStateAndEnabledSplit(t *testing.T) {
	tasks := converted(convertTask(t, "{systemd: {name: nginx, state: started, enabled: yes, daemon_reload: yes}}"))
	want := []string{"svc.systemd.daemon_reload", "svc.systemd.start", "svc.systemd.enable"}
	if got := fqcns(tasks); !slices.Equal(got, want) {
		t.Fatalf("= %v, want %v", got, want)
	}
	if _, ok := tasks[0].Params["name"]; ok {
		t.Error("daemon_reload was given the unit's name, which it does not take")
	}
	for _, task := range tasks[1:] {
		if task.Params["name"] != "nginx" {
			t.Errorf("%s name = %v", task.FQCN, task.Params["name"])
		}
	}
	tasks = converted(convertTask(t, "{service: {name: nginx, enabled: no}}"))
	if got := fqcns(tasks); !slices.Equal(got, []string{"svc.disable"}) {
		t.Errorf("enabled alone = %v, want svc.disable", got)
	}
}

// TestModules_TemplatedValues covers arguments whose values come from the
// playbook's own variables: a list unrolls, a comma text splits, a text
// resolved for a list parameter becomes a one-item list, and a bool
// parameter takes Ansible's spellings.
func TestModules_TemplatedValues(t *testing.T) {
	r := convert(t, `- hosts: web
  gather_facts: false
  vars: {pkgs: [curl, git], csv: "vim,tmux", line: hostname r1, reload: "t", nothing: "maybe"}
  tasks:
    - {apt: {name: "{{ pkgs }}"}}
    - {apt: {name: "{{ csv }}"}}
    - {ios_config: {lines: "{{ line }}"}}
    - {systemd: {name: nginx, state: started, daemon_reload: "{{ reload }}"}}
    - {copy: {dest: /x, content: "{{ pkgs }}"}}
`).Report
	want := []string{"pkg.apt.install", "pkg.apt.install", "pkg.apt.install", "pkg.apt.install", "net.ios.config", "svc.systemd.daemon_reload", "svc.systemd.start"}
	tasks := converted(r)
	if got := fqcns(tasks); !slices.Equal(got, want) {
		t.Fatalf("= %v, want %v; findings %v", got, want, codesOf(r))
	}
	var names []any
	for _, task := range tasks[:4] {
		names = append(names, task.Params["name"])
	}
	if !reflect.DeepEqual(names, []any{"curl", "git", "vim", "tmux"}) {
		t.Errorf("names = %v", names)
	}
	if got := tasks[4].Params["lines"]; !reflect.DeepEqual(got, []any{"hostname r1"}) {
		t.Errorf("lines = %#v", got)
	}
	if r.Counts.Blocked != 1 {
		t.Errorf("blocked %d, want the copy of a list into text", r.Counts.Blocked)
	}
}
