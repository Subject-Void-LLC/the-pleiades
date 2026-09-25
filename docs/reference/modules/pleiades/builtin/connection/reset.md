---
status: beta
---

# pleiades.builtin.connection.reset

Closes the SSH connection kept open to the device, so its next task logs in again.

A run keeps one SSH connection per device open between tasks unless persist_connections is turned off. A login made before a change to the account it logs in as does not see that change: a group the account was just added to, a new shell, a changed limit. The identity methods close the connection themselves after they run, so this is for the change they cannot see, such as a group membership edited by exec.command. It is Ansible's meta: reset_connection, and migrate-playbook converts that to it. It touches nothing on the device, so it never reports changed, and a check runs it as it is. Where connections do not persist it does nothing, since every task already logs in afresh.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SSHTransportCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It changes nothing on the device: it closes the platform's own connection, and the next task opens a new one, so the device is as it would have been had the task never run and there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `identity.user.modify`
- `identity.group.modify`
- `exec.command`

## Examples

Pick up a group membership in the next task:

```yaml
- name: Add deploy to the docker group
  exec.command:
    cmd: usermod -aG docker deploy
- name: Log in again, so the new group applies
  pleiades.builtin.connection.reset: {}
- name: Use docker as deploy
  exec.command:
    cmd: docker ps
```

