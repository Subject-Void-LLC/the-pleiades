// This file is the cheap, in-process sanity check on the catalog data
// itself: no duplicate names, and every entry passes the same Validate()
// the real pleiades forge CLI runs before writing anything. It catches a
// typo in this package's data at `go test` time, before tools/gencatalog
// ever shells out to the real binary.
package catalogdata_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
)

func TestCollections_NoDuplicatesAndAllValid(t *testing.T) {
	seen := make(map[string]bool, len(catalogdata.Collections))
	for _, c := range catalogdata.Collections {
		if seen[c.Name] {
			t.Errorf("duplicate collection name %q", c.Name)
		}
		seen[c.Name] = true
		if err := c.Validate(); err != nil {
			t.Errorf("collection %q failed Validate: %v", c.Name, err)
		}
	}
}

func TestCollections_MatchesDocumentedCount(t *testing.T) {
	// docs/hephaestus.md's own catalog table, counted by hand and cross
	// checked against this package's section files, previously resolved to
	// 75 individual <namespace>.<method> names. Phase 16 (Native Go
	// Execution Adapter) added the 76th, net.ssh.ping, its own real,
	// StatusImplemented Release Gate method. Part XV's WinRM work added
	// the 77th, exec.winrm.shell, the Windows counterpart of exec.shell
	// and the first method reaching a device over a transport other than
	// SSH or HTTPS. Phase 73's Workstream F added the 78th,
	// container.docker.exec, the first method to reach a device over a
	// raw Docker daemon socket instead of SSH, WinRM, or HTTPS. Phase
	// 86.5's follow-on work added the 79th, 80th and 81st: net.ios.facts,
	// net.ios.ping and net.ios.save, the first Cisco IOS methods that read
	// and parse a device's own output rather than only sending lines to
	// it, all three built on the interactive CLI transport that phase
	// introduced. Connection persistence added the 82nd,
	// pleiades.builtin.connection.reset, Ansible's meta: reset_connection.
	// The virt.vbox Collection added the 83rd through 88th: three VM
	// methods and three snapshot methods on a host that runs VirtualBox.
	// Making VMs there added the 89th through 94th: win.file.download,
	// and virt.vbox.vm.import_ova, clone, delete, host_keys and list.
	// Sizing VMs added the 95th, virt.vbox.vm.resize, and finding why
	// multi-CPU VMs hang on a hybrid Windows host the 96th,
	// win.cpu.topology. Windows guests added the 97th through 100th:
	// virt.vbox.vm.import_disk, install and eject_seed, and
	// wait.connection; seeing into a VM with no window the 101st through
	// 104th: virt.vbox.vm.screenshot, log, send_keys and addresses. The
	// gate Collection added the 105th and 106th: gate.list and gate.run,
	// a project's own checks run on a device from a gate manifest. The
	// build Collection added the 107th through 109th: build.digests.check,
	// build.digests.refresh and build.lock.verify. The db Collection added
	// the 110th through 115th: db.pg.query, dump, restore, migrate, grants
	// and rowcounts. Phase 119e retired the four container.docker.*
	// methods (115 less 4) into fourteen container.* ones built on the
	// engine's API (125): run, create, start, stop, restart, remove,
	// recreate, exec, info, list, logs, prune, copy_in and copy_out. The
	// container network, volume and engine methods added the 126th
	// through 134th: container.network.create, remove, connect,
	// disconnect and info, container.volume.create, remove and info, and
	// container.engine.info. The image and registry methods added the
	// 135th through 148th: container.image.pull, push, tag, remove, save,
	// load, import, prune, info, resolve and search, and
	// container.registry.login, logout and info. The scan Collection
	// added the 149th through 151st: scan.sbom, scan.secrets and
	// scan.vuln. Preparing a Windows host added the 171st through 173rd:
	// win.capability.install and remove, and win.reboot. win.firewall.rule
	// is the 174th, win.firewall.profile the 175th,
	// win.archive.extract the 176th, net.ios.command the 177th, and
	// file.stat the 178th. Network verification (Phase 127b) added the
	// 179th through 186th: net.frr.config, interface, facts and command,
	// wait.routes, net.trace, and net.ovs.bridge and port; net.batfish.check
	// is the 187th. net.ios.parse, net.ios.replace, http.wait and
	// pleiades.builtin.project.write are the 188th through 191st. Building
	// Phase 129a added the 202nd, net.ios.checkpoints; Phase 129b the 203rd through 207th, virt.cml.link.stop and start, virt.cml.capture.start and stop, and net.pcap.summary. CML labs (Phase 127d) added the 192nd through 201st: virt.cml.lab.list,
	// info, create, start, stop and delete, virt.cml.node.create and delete,
	// and virt.cml.link.create and delete. VMware Fusion added the 208th
	// through 210th: virt.fusion.vm.info, start and stop.
	// A lab VM's data disk added the 211th and 212th: virt.vbox.vm.attach_disk
	// and detach_disk. A containerlab API server's methods added the 213th
	// through 256th: virt.clab.lab.*, node.*, netem.*, veth, vxlan, image.*,
	// server.*, capture, topology.generate, ssh_session.* and edgeshark.*.
	// This test pins the number down so a future accidental entry loss or
	// duplication is a build failure, not a silent gap.
	const wantCollections = 256
	if got := len(catalogdata.Collections); got != wantCollections {
		t.Errorf("len(Collections) = %d, want %d", got, wantCollections)
	}
}

func TestDevices_AllValid(t *testing.T) {
	seen := make(map[string]bool, len(catalogdata.Devices))
	for _, d := range catalogdata.Devices {
		key := d.Vendor + "/" + d.TypeKey
		if seen[key] {
			t.Errorf("duplicate device %s", key)
		}
		seen[key] = true
		if err := d.Validate(); err != nil {
			t.Errorf("device %s failed Validate: %v", key, err)
		}
	}
}
