// This file holds the Extended infrastructure section of
// docs/hephaestus.md's catalog: ansible.posix.firewalld/mount,
// ansible.windows.win_feature, community.general.archive/unarchive,
// community.docker.docker_container, and the two amazon.aws modules. The
// two cloud entries declare no transport at all: they run against an API,
// not an inventory device over SSH, matching docs/hephaestus.md's own
// "clearest controller side cases in the catalog" description.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var extendedCollections = []collectionscaffold.Config{
	{
		Name:              "fw.firewalld.allow",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Opens a port or service in firewalld.",
			Description: "Makes sure a port or service is allowed through firewalld in the given zone. This is close to community.general.firewalld with state=enabled, except permanent and immediate are decided independently rather than as one of firewalld's own permanent/runtime toggle: either can be true without the other. Both configurations are read before anything is sent, so a half already allowing the rule is left alone and reports no change from that half.",
			Params: []collection.Param{
				{Name: "port", Type: "int", Description: "The port to allow. Exactly one of port or service is required."},
				{Name: "protocol", Type: "string", Default: "tcp", Description: "The protocol for port (tcp or udp). Ignored when service is given."},
				{Name: "service", Type: "string", Description: "The firewalld service name to allow, e.g. http. Exactly one of port or service is required."},
				{Name: "zone", Type: "string", Default: "public", Description: "The firewalld zone to allow the rule in."},
				{Name: "permanent", Type: "bool", Default: "true", Description: "Also add the rule to the permanent configuration, so it survives a reload."},
				{Name: "immediate", Type: "bool", Default: "true", Description: "Also add the rule to the runtime configuration, so it takes effect now."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "zone", Type: "string", Returned: "always", Description: "The zone this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether the rule was allowed in the permanent configuration and in the runtime one, before this task and after it. Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{Name: "Open a port", RunbookYAML: "- name: Allow HTTPS\n  fw.firewalld.allow:\n    port: 443\n"},
				{Name: "Allow a service immediately without persisting it", RunbookYAML: "- name: Temporarily allow SSH from a maintenance zone\n  fw.firewalld.allow:\n    service: ssh\n    zone: maintenance\n    permanent: false\n"},
			},
			SeeAlso: []string{"fw.firewalld.deny", "fw.firewalld.reload"},
		},
	},
	{
		Name:              "fw.firewalld.deny",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Closes a port or service in firewalld.",
			Description: "Makes sure a port or service is not allowed through firewalld in the given zone. This is close to community.general.firewalld with state=disabled, except permanent and immediate are decided independently rather than as one of firewalld's own permanent/runtime toggle: either can be true without the other. Both configurations are read before anything is sent, so a half already denying the rule is left alone and reports no change from that half.",
			Params: []collection.Param{
				{Name: "port", Type: "int", Description: "The port to deny. Exactly one of port or service is required."},
				{Name: "protocol", Type: "string", Default: "tcp", Description: "The protocol for port (tcp or udp). Ignored when service is given."},
				{Name: "service", Type: "string", Description: "The firewalld service name to deny, e.g. http. Exactly one of port or service is required."},
				{Name: "zone", Type: "string", Default: "public", Description: "The firewalld zone to remove the rule from."},
				{Name: "permanent", Type: "bool", Default: "true", Description: "Also remove the rule from the permanent configuration."},
				{Name: "immediate", Type: "bool", Default: "true", Description: "Also remove the rule from the runtime configuration, so it takes effect now."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "zone", Type: "string", Returned: "always", Description: "The zone this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether the rule was allowed in the permanent configuration and in the runtime one, before this task and after it. Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{Name: "Close a port", RunbookYAML: "- name: Deny telnet\n  fw.firewalld.deny:\n    port: 23\n"},
			},
			SeeAlso: []string{"fw.firewalld.allow", "fw.firewalld.reload"},
		},
	},
	{
		Name:              "fw.firewalld.reload",
		Capabilities:      []capability.Name{capability.NameFirewalld},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Reloads firewalld to apply pending rule changes.",
			Description: "Runs firewall-cmd --reload, which applies the permanent configuration to the runtime one and is what makes an fw.firewalld.allow or fw.firewalld.deny run with permanent: true, immediate: false visible without a full firewalld restart. This is ansible.posix.firewalld's own no-op-detecting reload, except firewall-cmd exposes no way to ask whether a reload would have made any difference, so this always reports changed the same way svc.systemd.daemon_reload does for the identical reason.",
			Params: []collection.Param{
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Examples: []collection.Example{
				{Name: "Persist a rule and apply it now", RunbookYAML: "- name: Allow HTTPS permanently\n  fw.firewalld.allow:\n    port: 443\n    immediate: false\n\n- name: Apply it\n  fw.firewalld.reload: {}\n"},
			},
			SeeAlso: []string{"fw.firewalld.allow", "fw.firewalld.deny"},
		},
	},
	{
		Name:              "fs.mount",
		Capabilities:      []capability.Name{capability.NameLinux},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Mounts a filesystem on the target, and optionally persists it to fstab.",
			Description: "Makes sure path is mounted from src, creating the mount if it is not already there. This is close to ansible.builtin.mount with state=mounted, split so that persisting to fstab (see the persist parameter) is independent of mounting: either can be true without the other. Mount state is read from findmnt before anything is sent, so a path already mounted from src with a matching fstype reports no change; a path already mounted from a different src or fstype is refused rather than silently remounted, since that is not something this method can do without first unmounting it. opts is compared only when the task actually names it: a path already mounted with different options than an unspecified opts is left alone rather than treated as drift. A check reads findmnt and fstab, mounts nothing and writes nothing, and leaves a new mount's options out of its prediction, since the kernel rewrites them.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The mountpoint to mount onto."},
				{Name: "src", Type: "string", Required: true, Description: "The device, share or filesystem source to mount."},
				{Name: "fstype", Type: "string", Required: true, Description: "The filesystem type, as mount's own -t takes it (e.g. ext4, nfs, xfs)."},
				{Name: "opts", Type: "string", Description: "Mount options, as mount's own -o takes them. Defaults to \"defaults\" for a fresh mount and a fresh fstab entry; an existing mount or fstab entry is only compared or rewritten against this when the task actually sets it."},
				{Name: "persist", Type: "bool", Default: "true", Description: "Also make sure path has a matching entry in fstab, so it mounts again on the next boot."},
				{Name: "fstab", Type: "string", Default: "/etc/fstab", Description: "The fstab-format file to read and, if persist is true, write."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The mountpoint this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What findmnt and the fstab file reported about path before this task and after it, each holding mounted, source, fstype, options and persisted. Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{Name: "Mount and persist a data volume", RunbookYAML: "- name: Mount the data volume\n  fs.mount:\n    path: /data\n    src: /dev/sdb1\n    fstype: ext4\n"},
				{Name: "Mount without touching fstab", RunbookYAML: "- name: Mount a scratch volume for this run only\n  fs.mount:\n    path: /mnt/scratch\n    src: /dev/sdb2\n    fstype: ext4\n    persist: false\n"},
			},
			SeeAlso: []string{"fs.unmount"},
		},
	},
	{
		Name:              "fs.unmount",
		Capabilities:      []capability.Name{capability.NameLinux},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Unmounts a filesystem on the target, and optionally removes it from fstab.",
			Description: "Makes sure path is not mounted, unmounting it if it is. This is close to ansible.builtin.mount with state=unmounted, split so that removing the fstab entry (see the persist parameter) is independent of unmounting: either can happen without the other. Mount state is read from findmnt before anything is sent, so a path already unmounted reports no change from the unmount itself.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The mountpoint to unmount."},
				{Name: "persist", Type: "bool", Default: "true", Description: "Also remove any matching entry from fstab, so it does not mount again on the next boot."},
				{Name: "fstab", Type: "string", Default: "/etc/fstab", Description: "The fstab-format file to read and, if persist is true, write."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The mountpoint this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What findmnt and the fstab file reported about path before this task and after it, each holding mounted, source, fstype, options and persisted. After always reports mounted: false on a successful unmount."},
			},
			Examples: []collection.Example{
				{Name: "Unmount and forget a volume", RunbookYAML: "- name: Unmount the old data volume\n  fs.unmount:\n    path: /data\n"},
				{Name: "Unmount but leave the fstab entry", RunbookYAML: "- name: Unmount temporarily for maintenance\n  fs.unmount:\n    path: /data\n    persist: false\n"},
			},
			SeeAlso: []string{"fs.mount"},
		},
	},
	{
		Name:              "win.feature.install",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Enables a Windows optional feature or role via DISM, including its required parent features.",
			Description: "Makes sure a Windows optional feature or role is enabled. This is ansible.windows.win_optional_feature with state=present (or win_feature's default), built on dism.exe /online /enable-feature rather than the ServerManager PowerShell module, since dism.exe works on every Windows SKU and this platform's own DISMLogPath capability already commits to it. /all is passed, so enabling a feature also enables the parent features it requires, matching what the Windows GUI's own \"Add roles and features\" does by default. State is read before anything is sent, so a feature that is already enabled reports no change and no command reaches the device. A feature name DISM does not recognize is refused rather than reported as already enabled, since that is nearly always a typo. Many features need a restart before they finish taking effect; check reboot_required rather than assuming changed alone means the feature is fully usable. A check reads the feature and sends nothing; when the feature would change, its diff leaves out the state the feature would end in and reboot_required, since DISM decides between the finished and pending states only when it runs.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The Windows optional feature or role's DISM feature name, such as IIS-WebServerRole, not its display name. The feature must be one DISM recognizes: a name it does not is refused rather than reported as already the target state, since that is nearly always a typo."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The feature this task acted on."},
				{Name: "reboot_required", Type: "bool", Returned: "always", Description: "Whether DISM reported that a restart is needed for this change to take full effect (its own ERROR_SUCCESS_REBOOT_REQUIRED). False on a run that changed nothing."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What DISM reported about the feature before this task and after it, each holding exists and state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Enable IIS", RunbookYAML: "- name: Make sure the web server role is enabled\n  win.feature.install:\n    name: IIS-WebServerRole\n  register: iis\n\n- name: Reboot if DISM asked for one\n  exec.winrm.shell:\n    shell: powershell\n    command: Restart-Computer -Force\n    expect_disconnect: true\n  when:\n    - iis.reboot_required\n"},
			},
			SeeAlso: []string{"win.feature.remove"},
		},
	},
	{
		Name:              "win.feature.remove",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Disables a Windows optional feature or role via DISM.",
			Description: "Makes sure a Windows optional feature or role is disabled. This is ansible.windows.win_optional_feature with state=absent, built on dism.exe /online /disable-feature. Unlike install, this does not pass /all: removing a feature should not silently remove the parent features it depended on. State is read before anything is sent, so a feature that is already disabled reports no change. A feature name DISM does not recognize is refused rather than reported as already disabled, since that is nearly always a typo. Many features need a restart before removal fully takes effect; check reboot_required rather than assuming changed alone means the feature is gone. A check reads the feature and sends nothing; when the feature would change, its diff leaves out the state the feature would end in and reboot_required, since DISM decides between the finished and pending states only when it runs.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The Windows optional feature or role's DISM feature name, such as IIS-WebServerRole, not its display name. The feature must be one DISM recognizes: a name it does not is refused rather than reported as already the target state, since that is nearly always a typo."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The feature this task acted on."},
				{Name: "reboot_required", Type: "bool", Returned: "always", Description: "Whether DISM reported that a restart is needed for this change to take full effect (its own ERROR_SUCCESS_REBOOT_REQUIRED). False on a run that changed nothing."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What DISM reported about the feature before this task and after it, each holding exists and state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Disable IIS", RunbookYAML: "- name: Make sure the web server role is disabled\n  win.feature.remove:\n    name: IIS-WebServerRole\n"},
			},
			SeeAlso: []string{"win.feature.install"},
		},
	},
	{
		Name:          "archive.create",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Creates an archive (tar or tar.gz) from files on the target.",
			Description: "Creates path as a tar archive of src, entirely from files already on the target -- this is community.general.archive without a zip option. Idempotency here is existence-only: a run finding path already there reports no change and reads none of src, the same way file.copy's checksum comparison decides on bytes rather than a name but simpler still, since this does not even open the archive to compare. remove, when true, deletes src once the archive has been written; the archive itself is not touched a second time to verify it. A check reads what a real run reads and runs no tar. A src, or the directory the archive would go in, that is missing when a check runs makes the call unchecked rather than failed, since an earlier task in the same run may be what creates it.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The archive file to create."},
				{Name: "src", Type: "list", Required: true, Description: "The paths on the target to include, at least one."},
				{Name: "format", Type: "string", Default: "tar.gz", Choices: []string{"tar", "tar.gz"}, Description: "The archive format. tgz is accepted as a synonym for tar.gz."},
				{Name: "remove", Type: "bool", Default: "false", Description: "Delete src once the archive has been written."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The archive this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What path looked like before this task and after it (exists, kind, mode, owner, group, size, mtime). Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{Name: "Archive a directory", RunbookYAML: "- name: Archive the release build\n  archive.create:\n    path: /tmp/release.tar.gz\n    src:\n      - /opt/app/dist\n"},
				{Name: "Archive and remove the originals", RunbookYAML: "- name: Archive old logs and delete them\n  archive.create:\n    path: /var/backups/logs-2026-08.tar.gz\n    src:\n      - /var/log/app/2026-08\n    remove: true\n"},
			},
			SeeAlso: []string{"archive.extract", "file.remove"},
		},
	},
	{
		Name:          "archive.extract",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Extracts an archive (tar or tar.gz) on the target.",
			Description: "Extracts src into dest, where src is an archive already present on the target -- this is community.general.unarchive with remote_src implied true always; nothing in this platform can transfer a file from wherever a runbook runs to the target (file.copy explicitly refuses that too), so a src living anywhere else is out of scope. Compression is auto-detected by tar itself, so there is no format parameter here the way archive.create has one. Idempotency is opt-in: naming creates skips extraction when that path is already there, and naming none means every run extracts again, the same honesty exec.command already has for a command with no built-in idempotency of its own. A check reads what a real run reads and extracts nothing. A src missing when a check runs, or a dest that is there but is not a directory, makes the call unchecked rather than failed, since an earlier task in the same run may be what fixes it.",
			Params: []collection.Param{
				{Name: "src", Type: "string", Required: true, Description: "The archive on the target to extract. Never a path on the machine running this task."},
				{Name: "dest", Type: "string", Required: true, Description: "The directory to extract into, created if it does not exist."},
				{Name: "remove", Type: "bool", Default: "false", Description: "Delete src once it has been extracted."},
				{Name: "creates", Type: "string", Description: "A path whose existence means extraction already happened; when it is already there, this task reports no change and does not extract again. Left unset, every run extracts."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "dest", Type: "string", Returned: "always", Description: "The directory this task extracted into."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What dest looked like before this task and after it (exists, kind, mode, owner, group, size, mtime). Recorded even on a run that skipped extraction because creates was already there."},
			},
			Examples: []collection.Example{
				{Name: "Extract a build already staged on the target", RunbookYAML: "- name: Unpack the release\n  archive.extract:\n    src: /tmp/release.tar.gz\n    dest: /opt/app\n"},
				{Name: "Extract only once", RunbookYAML: "- name: Unpack the SDK if it is not already there\n  archive.extract:\n    src: /tmp/sdk.tar.gz\n    dest: /opt/sdk\n    creates: /opt/sdk/bin/sdk\n"},
			},
			SeeAlso: []string{"archive.create"},
		},
	},
	{
		Name:              "container.docker.run",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs a Docker container on the target.",
			Description: "Makes sure a container named name is running, starting one from image if no container by that name exists. This is a narrow slice of community.docker.docker_container: idempotency here is existence of the NAME only, not a comparison of the running container's configuration against what this task asked for. A container already present under name is left exactly as it is, regardless of whether its image, ports, volumes, env or restart policy match; this method never recreates. Always runs detached (-d), since this platform has no interactive session to attach one to.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The container name to create or leave alone."},
				{Name: "image", Type: "string", Required: true, Description: "The image to run. Ignored when a container already exists under name."},
				{Name: "command", Type: "list", Description: "The command and its arguments, as separate list elements rather than one shell string. Overrides the image's own default command."},
				{Name: "ports", Type: "list", Description: "Port mappings, each \"host:container\" (docker run's own -p syntax)."},
				{Name: "volumes", Type: "list", Description: "Volume mappings, each \"host:container\" (docker run's own -v syntax)."},
				{Name: "env", Type: "dict", Description: "Environment variables to set in the container, as a map of name to value."},
				{Name: "restart_policy", Type: "string", Description: "The restart policy (docker run's own --restart value, e.g. unless-stopped)."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The container this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What docker inspect reported about the container before this task and after it (exists, status). Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{Name: "Run a container", RunbookYAML: "- name: Run nginx\n  container.docker.run:\n    name: web\n    image: nginx:1.27\n    ports:\n      - \"8080:80\"\n"},
			},
			SeeAlso: []string{"container.docker.stop", "container.docker.remove"},
		},
	},
	{
		Name:              "container.docker.stop",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops a running Docker container on the target.",
			Description: "Makes sure a container named name is not running, stopping it if it is. Container state is read from docker inspect before anything is sent, so a container already stopped, or absent entirely, reports no change and no command reaches the device.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The container to stop."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The container this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What docker inspect reported about the container before this task and after it (exists, status)."},
			},
			Examples: []collection.Example{
				{Name: "Stop a container", RunbookYAML: "- name: Stop web\n  container.docker.stop:\n    name: web\n"},
			},
			SeeAlso: []string{"container.docker.run", "container.docker.remove"},
		},
	},
	{
		Name:              "container.docker.remove",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a Docker container from the target.",
			Description: "Makes sure a container named name does not exist, removing it if present. Container state is read from docker inspect before anything is sent, so a container already absent reports no change and no command reaches the device. A check reads the same state and removes nothing; a container that is not stopped, with force unset, makes the call unchecked rather than failed, since docker rm refuses one unless an earlier task in the same run stops it.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The container to remove."},
				{Name: "force", Type: "bool", Default: "false", Description: "Remove the container even if it is still running (docker rm -f). Left false, removing a running container fails rather than stopping it first."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The container this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What docker inspect reported about the container before this task and after it. After always reports exists: false on a successful run."},
			},
			Examples: []collection.Example{
				{Name: "Remove a stopped container", RunbookYAML: "- name: Remove web\n  container.docker.remove:\n    name: web\n"},
				{Name: "Force-remove a running container", RunbookYAML: "- name: Remove web even if it is still running\n  container.docker.remove:\n    name: web\n    force: true\n"},
			},
			SeeAlso: []string{"container.docker.run", "container.docker.stop"},
		},
	},
	{
		Name:              "container.docker.exec",
		Capabilities:      []capability.Name{capability.NameDocker},
		Transports:        []string{"docker"},
		RequiresElevation: false,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs one command inside a running Docker container, reached directly through the daemon socket.",
			Description: "Runs cmd inside the container named name, through /bin/sh -c on the daemon's own exec endpoints (pkg/dockerexec), never over SSH. The container must already be running; this method does not start one (see container.docker.run). Reports the command's real exit code, stdout and stderr. A non-zero exit is an error, not a result to inspect, the same line exec.command draws.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The running container to exec into."},
				{Name: "cmd", Type: "string", Required: true, Description: "The command line, run inside the container's own /bin/sh -c. Pipes, redirects and quoting all work, because the container's shell sees them."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The container this task acted on."},
				{Name: "exit_code", Type: "int", Returned: "always", Description: "The command's real exit status, reported by the Docker daemon."},
				{Name: "stdout", Type: "string", Returned: "always", Description: "Everything the command wrote to standard output."},
				{Name: "stderr", Type: "string", Returned: "always", Description: "Everything the command wrote to standard error."},
			},
			Examples: []collection.Example{
				{Name: "Check a running container's own view of a file", RunbookYAML: "- name: Read the app's version file\n  container.docker.exec:\n    name: web\n    cmd: cat /opt/app/VERSION\n  register: version\n"},
			},
			SeeAlso: []string{"container.docker.run", "container.docker.stop", "container.docker.remove", "exec.shell"},
		},
	},
	{
		Name:          "cloud.aws.ec2.create",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Launches an EC2 instance via the AWS API.",
			Description: "Makes sure an instance tagged Name=name exists among the account/region's " +
				"non-terminated instances, launching one from image_id if none does. This is a narrow slice of " +
				"amazon.aws.ec2_instance: idempotency here is existence of the Name tag only, not a comparison of " +
				"a matching instance's configuration against what was requested. An instance already present under " +
				"that name is left exactly as it is, regardless of whether its image or instance type match; this " +
				"method never recreates. The target device is the AWS account/region context itself " +
				"(an aws_account inventory item), not a device this task reaches over any transport. A check looks the name up and, when nothing matches, predicts a launch without sending RunInstances, not even as a dry run; it leaves the instance ID and state out, since AWS assigns both.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The Name tag to find or create an instance under."},
				{Name: "image_id", Type: "string", Required: true, Description: "The AMI id to launch from. Ignored when an instance already exists under name."},
				{Name: "instance_type", Type: "string", Required: true, Description: "The EC2 instance type (e.g. t3.micro). Ignored when an instance already exists under name."},
			},
			Returns: []collection.ReturnField{
				{Name: "instance_id", Type: "string", Returned: "when an instance exists", Description: "The instance this task found or launched."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the account reported about the Name-tagged instance before this task and after it (exists, instance_id, state). Recorded even on a run that changed nothing."},
				{Name: "inverse", Type: "dict", Returned: "when this task launched a new instance", Description: "The cloud.aws.ec2.terminate task that undoes this run."},
			},
			Examples: []collection.Example{
				{
					Name:        "Launch a small instance",
					RunbookYAML: "- name: Launch the build agent\n  cloud.aws.ec2.create:\n    name: build-agent-1\n    image_id: ami-0abcdef1234567890\n    instance_type: t3.micro\n",
				},
			},
			SeeAlso: []string{"cloud.aws.ec2.terminate"},
		},
	},
	{
		Name:          "cloud.aws.ec2.terminate",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Terminates an EC2 instance via the AWS API.",
			Description: "Terminates the instance named by instance_id. A no-op if AWS has no record of that id " +
				"at all, or if it is already terminated. Unlike cloud.aws.ec2.create, this takes an exact " +
				"instance_id rather than a Name-tag lookup: terminating by a fuzzy match is a worse default than " +
				"requiring the exact resource for a destructive action.",
			Params: []collection.Param{
				{Name: "instance_id", Type: "string", Required: true, Description: "The instance id to terminate."},
			},
			Returns: []collection.ReturnField{
				{Name: "instance_id", Type: "string", Returned: "always", Description: "The instance this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the account reported about the instance before this task and after it (exists, instance_id, state). Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Terminate an instance",
					RunbookYAML: "- name: Tear down the build agent\n  cloud.aws.ec2.terminate:\n    instance_id: i-0123456789abcdef0\n",
				},
			},
			SeeAlso: []string{"cloud.aws.ec2.create"},
		},
	},
	{
		Name:          "cloud.aws.s3.create_bucket",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Creates an S3 bucket via the AWS API.",
			Description: "Makes sure bucket exists in the account/region, creating it if it does not. Idempotent " +
				"on existence alone: this method has no bucket configuration surface (versioning, encryption, " +
				"policy) to compare or converge, the same restraint every other narrowly-scoped method in this " +
				"catalog applies against its own upstream's larger surface. The target device is the AWS " +
				"account/region context itself (an aws_account inventory item), not a device this task reaches " +
				"over any transport.",
			Params: []collection.Param{
				{Name: "bucket", Type: "string", Required: true, Description: "The bucket name to create or leave alone."},
			},
			Returns: []collection.ReturnField{
				{Name: "bucket", Type: "string", Returned: "always", Description: "The bucket this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether the bucket existed before this task and after it. Recorded even on a run that changed nothing."},
				{Name: "inverse", Type: "dict", Returned: "when this task created the bucket", Description: "The cloud.aws.s3.delete_bucket task that undoes this run."},
			},
			Examples: []collection.Example{
				{
					Name:        "Create a bucket",
					RunbookYAML: "- name: Create the release artifacts bucket\n  cloud.aws.s3.create_bucket:\n    bucket: my-release-artifacts\n",
				},
			},
			SeeAlso: []string{"cloud.aws.s3.delete_bucket"},
		},
	},
	{
		Name:          "cloud.aws.s3.delete_bucket",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Deletes an S3 bucket via the AWS API.",
			Description: "Deletes bucket if it exists; a no-op otherwise. This method does not empty a non-empty " +
				"bucket first: AWS itself refuses to delete one that still holds objects, and that refusal is the " +
				"safety rail, not an error this method routes around. A check reads the bucket and whether it holds anything, deleting nothing; one that holds objects makes the call unchecked rather than failed, since an earlier task in the same run may be what empties it.",
			Params: []collection.Param{
				{Name: "bucket", Type: "string", Required: true, Description: "The bucket name to delete."},
			},
			Returns: []collection.ReturnField{
				{Name: "bucket", Type: "string", Returned: "always", Description: "The bucket this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether the bucket existed before this task and after it. Recorded even on a run that changed nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Delete a bucket",
					RunbookYAML: "- name: Remove the release artifacts bucket\n  cloud.aws.s3.delete_bucket:\n    bucket: my-release-artifacts\n",
				},
			},
			SeeAlso: []string{"cloud.aws.s3.create_bucket"},
		},
	},
}
