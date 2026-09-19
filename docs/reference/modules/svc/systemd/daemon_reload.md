---
status: beta
---

# svc.systemd.daemon_reload

Makes systemd re-read every unit file on disk.

Runs systemctl daemon-reload, which makes systemd pick up unit files that were written, changed or removed since it last read them. This is ansible.builtin.systemd with daemon_reload=yes on its own. It is the step that makes a unit file an earlier task wrote visible to systemd at all: without it, svc.systemd.start on a brand new unit fails with the unit not found, which reads as a broken unit file rather than as a stale view. It takes no unit name, because it is not about one unit, and it always reports changed: systemd exposes no way to ask whether a reload would have made any difference, so claiming otherwise would be a guess.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SystemdCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Re-reading unit files has no prior state to restore. What systemd held before the reload was a view of unit files that have since changed on disk, and nothing can ask it to go back to believing the old contents. Undoing whatever wrote those files is that task's inverse, not this one's.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.systemd.start`
- `svc.systemd.enable`

## Examples

Install a unit file and make systemd see it:

```yaml
- name: Write the unit file
  file.copy:
    src: ./app.service
    dest: /etc/systemd/system/app.service
    mode: "0644"

- name: Make systemd re-read its unit files
  svc.systemd.daemon_reload: {}

- name: Start the new service
  svc.systemd.start:
    name: app
```

