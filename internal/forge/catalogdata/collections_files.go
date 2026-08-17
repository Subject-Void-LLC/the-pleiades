// This file holds the Files and configuration section of
// docs/hephaestus.md's catalog: ansible.builtin.copy, .template, .file,
// .lineinfile, and .blockinfile. None of these require elevation by
// default, mirroring Ansible's own file/copy/lineinfile modules, which run
// as the connecting user unless a runbook separately elevates.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var filesCollections = []collectionscaffold.Config{
	{
		Name:          "file.copy",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Writes inline content to a file on the target, only when the bytes there differ.",
			Description: "Writes content to a path on the device, comparing a SHA-256 of the content against a SHA-256 the device computes of what is already there, so a run that finds the same bytes sends no write at all and reports no change. A write that does happen is atomic: the bytes go to a temporary file in the destination's own directory and are renamed over it, so a reader of the path sees either the whole old file or the whole new one and never a half-written one. Only content is supported; src is refused rather than half-implemented, because src names a file beside the runbook on the controller while this method runs on the runner, which under the Crawl tier is a per-task container that cannot see it. Anything at dest that is not a regular file is refused rather than replaced. An existing file keeps the mode, owner and group it had unless the task names them, matching what Ansible's own atomic move does; a file this method creates and the task gave no mode gets 0600, which is a fixed, documentable default rather than Ansible's umask-derived one.",
			Params: []collection.Param{
				{Name: "dest", Type: "string", Required: true, Description: "The path to write on the device. Its parent directory must already exist: this method writes a file and never creates the directories above it, which is file.directory's job."},
				{Name: "content", Type: "string", Required: true, Description: "The bytes to write, used verbatim: nothing is appended, so a file that should end in a newline needs one in the value. An empty string is a real request and truncates the file to nothing. Quote a value YAML would otherwise read as a number or a boolean, since a non-string is refused rather than rendered."},
				{Name: "src", Type: "string", Description: "Refused, always. In Ansible src names a file beside the playbook and the controller reads it; here a Collection method runs on the runner, which under the Crawl tier is a per-task container with no access to the controller's filesystem, so honoring src would work on the Walk tier and fail on the Crawl tier. Read the file into a variable and pass it as content instead."},
				{Name: "mode", Type: "string", Description: "The permission bits as one to four octal digits, quoted, for example \"0644\". Quote it: an unquoted 0644 is a number in YAML, not text. A symbolic mode such as u+x is refused, because it cannot be compared against the mode the device reports and the task would then report changed on every run. Left unset, an existing file keeps its mode and a newly created one gets 0600."},
				{Name: "owner", Type: "string", Description: "The user name to give the file. A numeric id is refused: chown reads an all-digit argument as an id while the device reports names, so the two could never compare equal and the task would report changed forever. Left unset, an existing file keeps its owner and a new one belongs to the connecting account."},
				{Name: "group", Type: "string", Description: "The group name to give the file, refused as a numeric id for the same reason owner is."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "dest", Type: "string", Returned: "always", Description: "The path this task wrote."},
				{Name: "checksum", Type: "string", Returned: "always", Description: "The SHA-256 of the file's contents in hex, read back from the device after any write rather than computed from what was asked for. Ansible's copy reports a SHA-1 here; this reports SHA-256, which is the one hash this platform computes."},
				{Name: "mode", Type: "string", Returned: "always", Description: "The permission bits the file carries now, as four octal digits."},
				{Name: "owner", Type: "string", Returned: "always", Description: "The user name owning the file now."},
				{Name: "group", Type: "string", Returned: "always", Description: "The group owning the file now."},
				{Name: "size", Type: "int", Returned: "always", Description: "The file's size in bytes now."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The before and after state of the path, each holding its kind, mode, owner, group, size, modification time and the checksum of its contents. Recorded even when nothing changed, in which case the two halves are identical."},
				{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The concrete task that undoes this run: a file.remove when this run created the file, or a file.permissions restoring the attributes it found when the file already existed. Absent from a run that changed nothing, which is how the record says undoing it means doing nothing. When the previous content was overwritten, the description says plainly that those bytes are not restored."},
			},
			Examples: []collection.Example{
				{
					Name:        "Write a configuration file",
					RunbookYAML: "- name: Install the agent configuration\n  fqcn: file.copy\n  params:\n    dest: /etc/pleiades/agent.yaml\n    content: |\n      endpoint: https://controller.internal:8443\n      verify: true\n    mode: \"0644\"\n",
				},
				{
					Name:        "Write a private file",
					RunbookYAML: "- name: Drop the deploy token\n  fqcn: file.copy\n  params:\n    dest: /etc/pleiades/token\n    content: \"{{ deploy_token }}\"\n    mode: \"0600\"\n    owner: pleiades\n    group: pleiades\n",
				},
				{
					Name:        "Truncate a file to nothing",
					RunbookYAML: "- name: Empty the local override file\n  fqcn: file.copy\n  params:\n    dest: /etc/app/local.conf\n    content: \"\"\n",
				},
			},
			SeeAlso: []string{"file.template", "file.touch", "file.permissions", "file.remove"},
		},
	},
	{
		Name:          "file.template",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Renders a template and writes the result to the target.",
		},
	},
	{
		Name:          "file.directory",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes sure a directory exists on the target, with the mode, owner and group the task asks for.",
			Description: "Ensures a directory exists at a path, creating any missing parents along the way exactly as mkdir -p does, and sets the mode, owner and group when the task names them. This is ansible.builtin.file with state=directory; use file.remove to delete a directory and file.permissions to change attributes without creating anything. State is read before anything is written, so a run that finds the directory already correct reports no change, and only the attributes that actually differ are applied. If something that is not a directory already exists at the path, the task fails rather than replacing it: turning a file into a directory would destroy the file.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The directory to ensure exists. Missing parent directories are created too, the way mkdir -p does. Ansible creates parents implicitly and has no parameter for it, so neither does this."},
				{Name: "mode", Type: "string", Description: "The permission bits in octal, written as a quoted string such as \"0755\". Quote it: an unquoted 0755 is read as a number by YAML and its leading zero is lost, so an unquoted value is refused rather than applied. A symbolic mode such as u+rwx is refused too, since it cannot be compared against the mode the device reports. Left unset, a new directory gets whatever the device's umask gives it and an existing one keeps the mode it has. Applies to the directory this task names, never to a parent created along the way."},
				{Name: "owner", Type: "string", Description: "The user name that should own the directory. A name rather than a numeric id, because a name is what the device reports back and therefore the only form this method can compare against. Left unset, ownership is not touched."},
				{Name: "group", Type: "string", Description: "The group name that should own the directory, under the same rule as owner: a name, not a numeric id. Left unset, the group is not touched."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The directory this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the path looked like before this task and what it looks like after, each holding exists, kind, mode, owner and group. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Create a directory tree",
					RunbookYAML: "- name: Make sure the release directory is there\n  fqcn: file.directory\n  params:\n    path: /opt/app/releases/current\n",
				},
				{
					Name:        "Create it with a mode",
					RunbookYAML: "- name: Make a private directory\n  fqcn: file.directory\n  params:\n    path: /var/lib/app/secrets\n    mode: \"0700\"\n",
				},
				{
					Name:        "Hand it to a service account",
					RunbookYAML: "- name: Own the data directory\n  fqcn: file.directory\n  params:\n    path: /var/lib/app/data\n    owner: app\n    group: app\n    mode: \"0750\"\n",
				},
			},
			SeeAlso: []string{"file.remove", "file.permissions"},
		},
	},
	{
		Name:          "file.symlink",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes a path a symbolic link pointing at a target, and refuses to replace a real file or directory.",
			Description: "Makes path be a symbolic link pointing at src, which is ansible.builtin.file with state=link. It reads the path before it acts and only sends a command when the answer differs, so a run that finds the link already pointing at src reports no change and sends no ln at all. A link pointing somewhere else is repointed. A path with nothing at it gets a new link. Anything else already there, a regular file, a directory, a socket, is refused rather than replaced, because ln would delete it and this method could not put it back; Ansible's force parameter is deliberately not implemented for that reason. src does not have to exist, which is ln's own behavior: a link to a path that is not there yet is a dangling link, and creating one is a real change like any other.",
			Params: []collection.Param{
				{Name: "src", Type: "string", Required: true, Description: "The path the link points AT, written into the link exactly as given. It does not have to exist. A relative value is stored verbatim and is resolved by the system against the directory holding the link, not against the directory this task runs in."},
				{Name: "path", Type: "string", Required: true, Description: "The link itself, the path this task creates or repoints. Its parent directory must already exist: this method creates a link, never the directories above it. Ansible's alias dest is accepted for this too."},
				{Name: "dest", Type: "string", Description: "Ansible's own alias for path, accepted so a converted playbook needs no renaming. Setting both of them to different paths is refused rather than guessed at."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "diff", Type: "dict", Returned: "always", Description: "The path's state before and after, each carrying exists and kind, plus target when it is a link. It is recorded even on a run that changed nothing, because 'it was already like this' is exactly what tells a rollback to do nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Point a stable name at a versioned release",
					RunbookYAML: "- name: Point current at the new release\n  fqcn: file.symlink\n  params:\n    src: /opt/app/releases/1.4.2\n    path: /opt/app/current\n",
				},
				{
					Name:        "Repoint a link that already exists",
					RunbookYAML: "- name: Select the staging configuration\n  fqcn: file.symlink\n  params:\n    src: /etc/app/config.staging.yaml\n    path: /etc/app/config.yaml\n",
				},
				{
					Name:        "Use dest, the way a converted playbook writes it",
					RunbookYAML: "- name: Link the vendor binary onto the path\n  fqcn: file.symlink\n  params:\n    src: /opt/vendor/bin/tool\n    dest: /usr/local/bin/tool\n",
				},
			},
			SeeAlso: []string{"file.copy", "file.remove"},
		},
	},
	{
		Name:          "file.remove",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a file or directory from the target.",
			Description: "Ensures nothing exists at a path, which is ansible.builtin.file with state=absent. It reads the path first, so a path that is already gone reports no change and sends no removal command at all, and a symbolic link is removed as the link rather than followed to whatever it points at. It diverges from Ansible in exactly one place, deliberately: Ansible deletes a non-empty directory without being asked, and this refuses unless the task sets recurse, because a recursive delete is the most destructive thing in this namespace and should be readable in the runbook that asks for it. Nothing this method removes can be restored by this platform.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The path that must not exist when this task finishes. A symbolic link is removed as the link, so whatever it points at is left alone."},
				{Name: "recurse", Type: "bool", Default: "false", Description: "Permit removing a directory that is not empty, and everything inside it. Without it, a non-empty directory is refused rather than emptied. Ansible's state=absent recurses without asking; this does not."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "diff", Type: "object", Returned: "always", Description: "What was at the path before this task and what is there after, under before and after. The before half carries exists, kind, mode, owner, group and size, which is everything a rollback can report about what it cannot restore. On a path that was already absent the two halves are identical, which is how a reader tells \"nothing to do\" from \"never ran\"."},
			},
			Examples: []collection.Example{
				{
					Name:        "Remove a file",
					RunbookYAML: "- name: Drop the leftover lock file\n  fqcn: file.remove\n  params:\n    path: /var/run/deploy.lock\n",
				},
				{
					Name:        "Remove an empty directory",
					RunbookYAML: "- name: Drop the empty spool directory\n  fqcn: file.remove\n  params:\n    path: /var/spool/old-queue\n",
				},
				{
					Name:        "Remove a directory and everything in it",
					RunbookYAML: "- name: Drop the previous release\n  fqcn: file.remove\n  params:\n    path: /opt/app/releases/2024-11-02\n    recurse: true\n",
				},
			},
			SeeAlso: []string{"file.directory", "file.touch"},
		},
	},
	{
		Name:          "file.touch",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Creates an empty file on the target, or updates its modification time.",
			Description: "Makes sure a regular file exists at a path, creating it empty when nothing is there and updating its modification time when it already is. This is ansible.builtin.file with state: touch, and it reports changed on every run for the same reason that module does: moving a modification time is a real change to the device, so a run that claimed otherwise would be wrong rather than tidy. The two kinds of change are told apart in the recorded diff, where a created file reads exists false then true and a re-stamped one reads true then true. Anything at the path that is not a regular file is refused rather than replaced.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The full path to the file. It is created empty when nothing is there, and left alone apart from its modification time when a regular file already is. A directory, a symbolic link or anything else at the path is refused."},
				{Name: "mode", Type: "string", Description: "The permission bits, written the way chmod takes them, for example 0644. Sent to the device only when it differs from what is already there, so a converged file is not re-chmodded. Left alone when not set."},
				{Name: "owner", Type: "string", Description: "The user that should own the file, by name rather than numeric id, since a numeric id is not portable between devices. Sent only when it differs. Left alone when not set."},
				{Name: "group", Type: "string", Description: "The group that should own the file, by name rather than numeric id. Sent only when it differs, and in one chown alongside owner when both are set. Left alone when not set."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "dest", Type: "string", Returned: "always", Description: "The path this touched, exactly as the task named it."},
				{Name: "created", Type: "bool", Returned: "always", Description: "True when nothing was at the path and an empty file was made. False when a file was already there and only its modification time moved."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The before and after state of the path, each holding exists, kind, and for a path that is there its mode, owner, group, size and mtime. This is the record a rollback reads, so it is written even though this method always reports changed."},
			},
			Examples: []collection.Example{
				{
					Name:        "Create a marker file",
					RunbookYAML: "- name: Mark the host as provisioned\n  fqcn: file.touch\n  params:\n    path: /var/lib/pleiades/provisioned\n",
				},
				{
					Name:        "Create a log file with an owner and a mode",
					RunbookYAML: "- name: Make the log file the service will append to\n  fqcn: file.touch\n  params:\n    path: /var/log/app/app.log\n    owner: app\n    group: app\n    mode: \"0640\"\n",
				},
				{
					Name:        "Act only when the file had to be created",
					RunbookYAML: "- name: Create the seed file\n  fqcn: file.touch\n  params:\n    path: /opt/app/seed\n  register: seed\n\n- name: Seed the database the first time only\n  fqcn: exec.command\n  params:\n    cmd: /opt/app/bin/seed\n  when_cel: seed.created\n",
				},
			},
			SeeAlso: []string{"file.remove", "file.permissions", "file.copy"},
		},
	},
	{
		Name:          "file.permissions",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Sets a file's owner, group, and mode on the target.",
			Description: "Sets the mode, owner and group of a path that already exists, changing only the ones that differ from what the device reports. A path that is absent is a refusal rather than a create, since making a file exist is file.touch's job and making a directory exist is file.directory's; that keeps a typo in a path from silently producing an empty file. A symbolic link is refused too, because chmod and chown follow a link while a stat of the link reports the link itself, so such a task would change one path while comparing against another and could never report converged. At least one of mode, owner and group must be given: a task that asks for nothing is a runbook mistake, not a no-op.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The path to change. It must already exist: this method changes a path rather than creating one."},
				{Name: "mode", Type: "string", Description: "The permission bits as one to four octal digits, quoted, for example \"0644\". Quote it: an unquoted 0644 is a number in YAML, not text. A symbolic mode such as u+x is refused, because it cannot be compared against the mode the device reports and the task would then report changed on every run."},
				{Name: "owner", Type: "string", Description: "The user name to give the path. A numeric id is refused: chown reads an all-digit argument as an id while the device reports names, so the two could never compare equal and the task would report changed forever."},
				{Name: "group", Type: "string", Description: "The group name to give the path, refused as a numeric id for the same reason owner is."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The path this task acted on."},
				{Name: "mode", Type: "string", Returned: "always", Description: "The permission bits the path carries now, as four octal digits."},
				{Name: "owner", Type: "string", Returned: "always", Description: "The user name owning the path now."},
				{Name: "group", Type: "string", Returned: "always", Description: "The group owning the path now."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The before and after state of the path, each holding its kind, mode, owner, group, size and modification time. Recorded even when nothing changed, in which case the two halves are identical."},
			},
			Examples: []collection.Example{
				{
					Name:        "Lock down a key file",
					RunbookYAML: "- name: Keep the deploy key private\n  fqcn: file.permissions\n  params:\n    path: /etc/pleiades/deploy.key\n    mode: \"0600\"\n    owner: pleiades\n    group: pleiades\n",
				},
				{
					Name:        "Make a script executable",
					RunbookYAML: "- name: Allow the rotation script to run\n  fqcn: file.permissions\n  params:\n    path: /usr/local/bin/rotate-logs\n    mode: \"0755\"\n",
				},
				{
					Name:        "Hand a directory to a service account",
					RunbookYAML: "- name: Give the cache directory to the service\n  fqcn: file.permissions\n  params:\n    path: /var/cache/pleiades\n    owner: pleiades\n",
				},
			},
			SeeAlso: []string{"file.touch", "file.directory", "file.copy"},
		},
	},
	{
		Name:          "file.line.set",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Ensures one line matching a pattern is present in a file, replacing or appending it.",
			Description: "Makes sure one line is present in a text file, which is ansible.builtin.lineinfile with state: present. The whole file is read, the new text is worked out locally, and the file is written back only when the bytes really differ, so a run that finds the line already in place sends no write at all. When regexp is set, the last line it matches is replaced; when it is not set, a line already exactly equal to line means the task is done. A line that has to be added goes at the end of the file unless insertafter or insertbefore names a pattern to place it against. The file has to exist already, since Ansible's create is not implemented, so file.touch or file.copy is what makes one. Two differences from Ansible are deliberate and worth knowing. A regexp that does not match the line being placed is refused, because such a task adds the line again on every single run and never settles. And a file that ends without a newline keeps ending without one, where Ansible would add it. Patterns are Go's RE2, which has no backreferences and no lookaround; backrefs, search_string, firstmatch, create, backup and validate are not implemented. Lines are split on the newline byte alone, so a file with Windows endings carries its carriage return as part of each line's text and a pattern meant to match one has to say so.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The text file to edit. It has to exist already and it has to be a regular file. A symbolic link is refused rather than followed, because writing replaces the path and would leave a regular file where the link was."},
				{Name: "line", Type: "string", Required: true, Description: "The exact text of the line to make sure is present, written verbatim, so leading whitespace is part of the line. It may not contain a newline: this method places one line, and file.block.set is what places several."},
				{Name: "regexp", Type: "string", Description: "A pattern naming the line to replace. The last line it matches is replaced by line, and if nothing matches then line is added. It has to match line itself, and is refused when it does not, because otherwise no run would ever find the line it just added and the file would grow forever. Matched anywhere in a line unless anchored with ^ or $."},
				{Name: "insertafter", Type: "string", Description: "Where to put the line when it has to be added: a pattern, and the line goes after the last line matching it. The value EOF means the end of the file, which is also what happens when this is left out and when the pattern matches nothing. Cannot be combined with insertbefore."},
				{Name: "insertbefore", Type: "string", Description: "Where to put the line when it has to be added: a pattern, and the line goes before the last line matching it. The value BOF means the start of the file. A pattern matching nothing puts the line at the end, which is Ansible's own behavior. Cannot be combined with insertafter."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The file this task edited."},
				{Name: "msg", Type: "string", Returned: "always", Description: "What happened, in lineinfile's own words: \"line added\", \"line replaced\", or empty when the file was already correct."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The file's state before and after, each holding exists, kind, mode, owner, group, size, mtime and the file's whole text. Recorded even when nothing changed, in which case the two halves are identical."},
			},
			Examples: []collection.Example{
				{
					Name:        "Correct a setting whether or not it is commented out",
					RunbookYAML: "- name: Turn off root login over SSH\n  fqcn: file.line.set\n  params:\n    path: /etc/ssh/sshd_config\n    regexp: '^#?PermitRootLogin'\n    line: PermitRootLogin no\n",
				},
				{
					Name:        "Append an entry that is either there or not",
					RunbookYAML: "- name: Add the internal registry to the hosts file\n  fqcn: file.line.set\n  params:\n    path: /etc/hosts\n    line: 10.0.4.12 registry.internal\n",
				},
				{
					Name:        "Place a line against an anchor",
					RunbookYAML: "- name: Put the include ahead of the defaults section\n  fqcn: file.line.set\n  params:\n    path: /etc/app/app.conf\n    line: include /etc/app/conf.d/all.conf\n    insertbefore: '^\\[defaults\\]'\n",
				},
			},
			SeeAlso: []string{"file.line.remove", "file.block.set", "file.copy"},
		},
	},
	{
		Name:          "file.line.remove",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Ensures no line matching a pattern remains in a file.",
			Description: "Makes sure no line matching a pattern remains in a text file, which is ansible.builtin.lineinfile with state: absent. The whole file is read, the surviving lines are worked out locally, and the file is written back only when the bytes really differ, so a run that finds nothing to remove sends no write at all. Every matching line goes, not only the first. A file that is not there is already in the wanted state, so the task reports no change instead of failing, which is what lets one runbook strip a setting from a fleet where not every host has the file. Three differences from Ansible are deliberate: setting both regexp and line is refused rather than silently preferring regexp, insertafter and insertbefore are refused rather than accepted and ignored, and a file that ends without a newline keeps ending without one. Patterns are Go's RE2, which has no backreferences and no lookaround; search_string, backup and validate are not implemented. Lines are split on the newline byte alone, so a file with Windows endings carries its carriage return as part of each line's text and a pattern meant to match one has to say so.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The text file to edit. A file that is not there is reported as no change rather than as an error, since it holds no lines to remove. A symbolic link is refused rather than followed, because writing replaces the path and would leave a regular file where the link was."},
				{Name: "regexp", Type: "string", Description: "Remove every line this pattern matches. Matched anywhere in a line unless anchored with ^ or $. Cannot be combined with line, and one of the two is required."},
				{Name: "line", Type: "string", Description: "Remove every line whose text is exactly this. It may not contain a newline, since a line is matched whole. Cannot be combined with regexp, and one of the two is required."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The file this task acted on."},
				{Name: "found", Type: "int", Returned: "always", Description: "How many lines were removed. Zero when the file held none matching, and zero when the file was not there at all."},
				{Name: "msg", Type: "string", Returned: "always", Description: "What happened, in lineinfile's own words: \"3 line(s) removed\", \"file not present\", or empty when the file held nothing to remove."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The file's state before and after, each holding exists and kind, plus mode, owner, group, size, mtime and the file's whole text when it is there. Recorded even when nothing changed, in which case the two halves are identical."},
			},
			Examples: []collection.Example{
				{
					Name:        "Drop a package source by pattern",
					RunbookYAML: "- name: Drop the retired package mirror\n  fqcn: file.line.remove\n  params:\n    path: /etc/apt/sources.list\n    regexp: '^deb .*mirror\\.old\\.example\\.com'\n",
				},
				{
					Name:        "Drop one exact entry",
					RunbookYAML: "- name: Remove the decommissioned host entry\n  fqcn: file.line.remove\n  params:\n    path: /etc/hosts\n    line: 10.0.4.9 registry.internal\n",
				},
				{
					Name:        "Act only when something was really removed",
					RunbookYAML: "- name: Strip every commented out override\n  fqcn: file.line.remove\n  params:\n    path: /etc/app/app.conf\n    regexp: '^#\\s*override'\n  register: overrides\n\n- name: Reload the service that read them\n  fqcn: exec.command\n  params:\n    cmd: systemctl reload app\n  when_cel: overrides.found > 0\n",
				},
			},
			SeeAlso: []string{"file.line.set", "file.block.remove", "file.remove"},
		},
	},
	{
		Name:          "file.block.set",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Ensures a marked, multi-line block of text is present in a file.",
			Description: "Keeps a region of a text file, delimited by a begin and an end marker line, exactly as the runbook declares it. The markers are what make this safe to run twice: the region carries its own boundaries on the device, so a later run replaces the text between them in place rather than appending a second copy below the first. A file with no such markers gets the block, with its markers, appended at the end. A file whose block already matches is not written at all. The file must already exist, since file.touch and file.copy are what create; a symbolic link is refused, because writing the file back would replace the link with a regular file. Its mode, owner and group are put back after every rewrite, so managing a block in a file that services read does not quietly make it private. Markers that do not pair up (two begin markers, or a begin with no end) are an error rather than a guess about where the block stops.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The file to edit. It must already exist: this method edits a file rather than creating one."},
				{Name: "block", Type: "string", Required: true, Description: "The text to keep between the markers, usually written as a YAML block scalar. A trailing newline is not significant, so the same block written inline and as a block scalar produce the same region. It must not be empty and must not itself contain a marker line."},
				{Name: "marker", Type: "string", Default: "# {mark} ANSIBLE MANAGED BLOCK", Description: "The template for both marker lines. The {mark} placeholder is replaced by marker_begin on the line above the block and by marker_end on the line below it. Change it for a file whose comment character is not #, and keep it stable afterwards: a task that changes its marker stops finding the block it wrote last time and appends a second one."},
				{Name: "marker_begin", Type: "string", Default: "BEGIN", Description: "The word {mark} becomes on the line above the block."},
				{Name: "marker_end", Type: "string", Default: "END", Description: "The word {mark} becomes on the line below the block."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The file this task acted on."},
				{Name: "present", Type: "bool", Returned: "always", Description: "Whether a marked block is in the file now, read back from the device."},
				{Name: "block", Type: "string", Returned: "always", Description: "The text between the markers now, with no trailing newline."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The before and after state of the marked region, each holding whether the block was present and what was between the markers. Recorded even when nothing changed, in which case the two halves are identical."},
				{Name: "inverse", Type: "dict", Returned: "when the run changed the file", Description: "The task that undoes this run: a file.block.set carrying the previous body, or a file.block.remove when the file carried no block before. A converged run records none, which is how the journal says undoing it means doing nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Manage a hosts file entry",
					RunbookYAML: "- name: Keep the cluster's short names resolvable\n  fqcn: file.block.set\n  params:\n    path: /etc/hosts\n    block: |\n      10.0.0.11 db1\n      10.0.0.12 db2\n",
				},
				{
					Name:        "Use a marker a file's own syntax allows",
					RunbookYAML: "- name: Manage the sshd hardening stanza\n  fqcn: file.block.set\n  params:\n    path: /etc/ssh/sshd_config\n    marker: \"# {mark} PLEIADES HARDENING\"\n    block: |\n      PermitRootLogin no\n      PasswordAuthentication no\n",
				},
				{
					Name:        "Keep two independent blocks in one file",
					RunbookYAML: "- name: Manage the proxy stanza only\n  fqcn: file.block.set\n  params:\n    path: /etc/environment\n    marker_begin: OPEN PROXY\n    marker_end: CLOSE PROXY\n    block: |\n      http_proxy=http://proxy.internal:3128\n",
				},
			},
			SeeAlso: []string{"file.block.remove", "file.line.set", "file.copy", "file.permissions"},
		},
	},
	{
		Name:          "file.block.remove",
		Capabilities:  []capability.Name{capability.NamePOSIXFileSystem},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Removes a marked, multi-line block of text from a file.",
			Description: "Takes away the region a file.block.set task left in a file, the two marker lines included, and leaves every other line alone. A file with no such markers is already in the state this asks for, so the task reports no change and writes nothing. The file must already exist, since a path that is not there is far more often a typo than a file whose block is missing; a symbolic link is refused, because writing the file back would replace the link with a regular file. Its mode, owner and group are put back after the rewrite. Markers that do not pair up (two end markers, or an end with no begin) are an error rather than a guess about which lines to delete, which matters more here than anywhere else in this namespace: the guess would be a deletion.",
			Params: []collection.Param{
				{Name: "path", Type: "string", Required: true, Description: "The file to edit. It must already exist."},
				{Name: "marker", Type: "string", Default: "# {mark} ANSIBLE MANAGED BLOCK", Description: "The template for both marker lines, which must be the one the block was written with. The {mark} placeholder is replaced by marker_begin on the line above the block and by marker_end on the line below it."},
				{Name: "marker_begin", Type: "string", Default: "BEGIN", Description: "The word {mark} becomes on the line above the block."},
				{Name: "marker_end", Type: "string", Default: "END", Description: "The word {mark} becomes on the line below the block."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "path", Type: "string", Returned: "always", Description: "The file this task acted on."},
				{Name: "present", Type: "bool", Returned: "always", Description: "Whether a marked block is in the file now, read back from the device. False after a successful run."},
				{Name: "block", Type: "string", Returned: "always", Description: "The text between the markers now, which is empty once the block is gone."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The before and after state of the marked region, each holding whether the block was present and what was between the markers. The before half is where the removed text is recorded. Recorded even when nothing changed, in which case the two halves are identical."},
				{Name: "inverse", Type: "dict", Returned: "when the run removed a block", Description: "The task that undoes this run: a file.block.set carrying the body that was removed. A run that found no block records none, which is how the journal says undoing it means doing nothing."},
			},
			Examples: []collection.Example{
				{
					Name:        "Stop managing a hosts file entry",
					RunbookYAML: "- name: Drop the cluster short names\n  fqcn: file.block.remove\n  params:\n    path: /etc/hosts\n",
				},
				{
					Name:        "Remove a block written with its own marker",
					RunbookYAML: "- name: Retire the hardening stanza\n  fqcn: file.block.remove\n  params:\n    path: /etc/ssh/sshd_config\n    marker: \"# {mark} PLEIADES HARDENING\"\n",
				},
			},
			SeeAlso: []string{"file.block.set", "file.line.remove", "file.remove"},
		},
	},
}
