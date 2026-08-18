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
				{Name: "Open a port", RunbookYAML: "- name: Allow HTTPS\n  fqcn: fw.firewalld.allow\n  params:\n    port: 443\n"},
				{Name: "Allow a service immediately without persisting it", RunbookYAML: "- name: Temporarily allow SSH from a maintenance zone\n  fqcn: fw.firewalld.allow\n  params:\n    service: ssh\n    zone: maintenance\n    permanent: false\n"},
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
				{Name: "Close a port", RunbookYAML: "- name: Deny telnet\n  fqcn: fw.firewalld.deny\n  params:\n    port: 23\n"},
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
				{Name: "Persist a rule and apply it now", RunbookYAML: "- name: Allow HTTPS permanently\n  fqcn: fw.firewalld.allow\n  params:\n    port: 443\n    immediate: false\n\n- name: Apply it\n  fqcn: fw.firewalld.reload\n  params: {}\n"},
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
			Description: "Makes sure path is mounted from src, creating the mount if it is not already there. This is close to ansible.builtin.mount with state=mounted, split so that persisting to fstab (see the persist parameter) is independent of mounting: either can be true without the other. Mount state is read from findmnt before anything is sent, so a path already mounted from src with a matching fstype reports no change; a path already mounted from a different src or fstype is refused rather than silently remounted, since that is not something this method can do without first unmounting it. opts is compared only when the task actually names it: a path already mounted with different options than an unspecified opts is left alone rather than treated as drift.",
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
				{Name: "Mount and persist a data volume", RunbookYAML: "- name: Mount the data volume\n  fqcn: fs.mount\n  params:\n    path: /data\n    src: /dev/sdb1\n    fstype: ext4\n"},
				{Name: "Mount without touching fstab", RunbookYAML: "- name: Mount a scratch volume for this run only\n  fqcn: fs.mount\n  params:\n    path: /mnt/scratch\n    src: /dev/sdb2\n    fstype: ext4\n    persist: false\n"},
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
				{Name: "Unmount and forget a volume", RunbookYAML: "- name: Unmount the old data volume\n  fqcn: fs.unmount\n  params:\n    path: /data\n"},
				{Name: "Unmount but leave the fstab entry", RunbookYAML: "- name: Unmount temporarily for maintenance\n  fqcn: fs.unmount\n  params:\n    path: /data\n    persist: false\n"},
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
		Doc:               collection.Doc{Summary: "Installs a Windows feature or role."},
	},
	{
		Name:              "win.feature.remove",
		Capabilities:      []capability.Name{capability.NameWindowsFeature},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Removes a Windows feature or role."},
	},
	{
		Name:          "archive.create",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Creates an archive (tar or tar.gz) from files on the target.",
			Description: "Creates path as a tar archive of src, entirely from files already on the target -- this is community.general.archive without a zip option. Idempotency here is existence-only: a run finding path already there reports no change and reads none of src, the same way file.copy's checksum comparison decides on bytes rather than a name but simpler still, since this does not even open the archive to compare. remove, when true, deletes src once the archive has been written; the archive itself is not touched a second time to verify it.",
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
				{Name: "Archive a directory", RunbookYAML: "- name: Archive the release build\n  fqcn: archive.create\n  params:\n    path: /tmp/release.tar.gz\n    src:\n      - /opt/app/dist\n"},
				{Name: "Archive and remove the originals", RunbookYAML: "- name: Archive old logs and delete them\n  fqcn: archive.create\n  params:\n    path: /var/backups/logs-2026-08.tar.gz\n    src:\n      - /var/log/app/2026-08\n    remove: true\n"},
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
			Description: "Extracts src into dest, where src is an archive already present on the target -- this is community.general.unarchive with remote_src implied true always; nothing in this platform can transfer a file from wherever a runbook runs to the target (file.copy explicitly refuses that too), so a src living anywhere else is out of scope. Compression is auto-detected by tar itself, so there is no format parameter here the way archive.create has one. Idempotency is opt-in: naming creates skips extraction when that path is already there, and naming none means every run extracts again, the same honesty exec.command already has for a command with no built-in idempotency of its own.",
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
				{Name: "Extract a build already staged on the target", RunbookYAML: "- name: Unpack the release\n  fqcn: archive.extract\n  params:\n    src: /tmp/release.tar.gz\n    dest: /opt/app\n"},
				{Name: "Extract only once", RunbookYAML: "- name: Unpack the SDK if it is not already there\n  fqcn: archive.extract\n  params:\n    src: /tmp/sdk.tar.gz\n    dest: /opt/sdk\n    creates: /opt/sdk/bin/sdk\n"},
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
				{Name: "Run a container", RunbookYAML: "- name: Run nginx\n  fqcn: container.docker.run\n  params:\n    name: web\n    image: nginx:1.27\n    ports:\n      - \"8080:80\"\n"},
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
				{Name: "Stop a container", RunbookYAML: "- name: Stop web\n  fqcn: container.docker.stop\n  params:\n    name: web\n"},
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
			Description: "Makes sure a container named name does not exist, removing it if present. Container state is read from docker inspect before anything is sent, so a container already absent reports no change and no command reaches the device.",
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
				{Name: "Remove a stopped container", RunbookYAML: "- name: Remove web\n  fqcn: container.docker.remove\n  params:\n    name: web\n"},
				{Name: "Force-remove a running container", RunbookYAML: "- name: Remove web even if it is still running\n  fqcn: container.docker.remove\n  params:\n    name: web\n    force: true\n"},
			},
			SeeAlso: []string{"container.docker.run", "container.docker.stop"},
		},
	},
	{
		Name:          "cloud.aws.ec2.create",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Creates an EC2 instance via the AWS API."},
	},
	{
		Name:          "cloud.aws.ec2.terminate",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Terminates an EC2 instance via the AWS API."},
	},
	{
		Name:          "cloud.aws.s3.create_bucket",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Creates an S3 bucket via the AWS API."},
	},
	{
		Name:          "cloud.aws.s3.delete_bucket",
		Capabilities:  []capability.Name{capability.NameAWSAPI},
		EngineVersion: engineVersion,
		Doc:           collection.Doc{Summary: "Deletes an S3 bucket via the AWS API."},
	},
}
