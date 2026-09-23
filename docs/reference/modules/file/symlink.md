---
status: beta
---

# file.symlink

Makes a path a symbolic link pointing at a target, and refuses to replace a real file or directory.

Makes path be a symbolic link pointing at src, which is ansible.builtin.file with state=link. It reads the path before it acts and only sends a command when the answer differs, so a run that finds the link already pointing at src reports no change and sends no ln at all. A link pointing somewhere else is repointed. A path with nothing at it gets a new link. Anything else already there, a regular file, a directory, a socket, is refused rather than replaced, because ln would delete it and this method could not put it back; Ansible's force parameter is deliberately not implemented for that reason. src does not have to exist, which is ln's own behavior: a link to a path that is not there yet is a dangling link, and creating one is a real change like any other.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `POSIXFileSystemCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `src` | `string` | yes | - | The path the link points AT, written into the link exactly as given. It does not have to exist. A relative value is stored verbatim and is resolved by the system against the directory holding the link, not against the directory this task runs in. |
| `path` | `string` | yes | - | The link itself, the path this task creates or repoints. Its parent directory must already exist: this method creates a link, never the directories above it. Ansible's alias dest is accepted for this too. |
| `dest` | `string` | no | - | Ansible's own alias for path, accepted so a converted playbook needs no renaming. Setting both of them to different paths is refused rather than guessed at. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `diff` | `dict` | always | The path's state before and after, each carrying exists and kind, plus target when it is a link. It is recorded even on a run that changed nothing, because 'it was already like this' is exactly what tells a rollback to do nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the link emits a file.remove naming it, which deletes the link and never what it points at. A run that found a link already there and repointed it emits a file.symlink pointing back at the target it had, because removing that link would delete something the run never created. A converged run emits nothing, since it sent no command at all.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `file.copy`
- `file.remove`

## Examples

Point a stable name at a versioned release:

```yaml
- name: Point current at the new release
  file.symlink:
    src: /opt/app/releases/1.4.2
    path: /opt/app/current
```

Repoint a link that already exists:

```yaml
- name: Select the staging configuration
  file.symlink:
    src: /etc/app/config.staging.yaml
    path: /etc/app/config.yaml
```

Use dest, the way a converted playbook writes it:

```yaml
- name: Link the vendor binary onto the path
  file.symlink:
    src: /opt/vendor/bin/tool
    dest: /usr/local/bin/tool
```

