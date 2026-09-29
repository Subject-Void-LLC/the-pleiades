---
status: beta
---

# net.netconf.config

Applies a configuration document to a device over NETCONF, with an optional pre-change backup.

Opens an RFC 6241 NETCONF session over the SSH "netconf" subsystem and applies content to the chosen datastore with edit-config. Parameter names are ansible.netcommon.netconf_config's own, with its target parameter under its own alias, datastore, since target names the device a task runs on. The session negotiates RFC 6242 chunked framing whenever the device offers base:1.1, and requests rollback-on-error whenever the device advertises it, so a rejected document leaves the device unchanged rather than half configured; that matters most on a device offering only writable-running, which is what Cisco IOS XE offers, because such a device has no staging area and every element lands on the live configuration as it is applied. A datastore the device never advertised support for is refused when the session opens rather than at the first write, naming the missing capability. Unlike the net.cli.* and net.ios.config methods, a rejected element comes back as a structured error carrying the device's own error-tag and the XPath of the element it objected to. Reports changed whenever the document reaches the device and the device answers ok.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetconfCapable`, `SSHTransportCapable` |
| Transports | `netconf` |
| Requires elevation | no |
| Check mode | Not supported: a check run names this task as unchecked, since only applying the document says what the device makes of it: the running datastore has no staging area, and a check through the candidate datastore would stage the document on the device, which a check promises not to do |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `content` | `string` | yes | - | The configuration document to apply, as the XML that goes inside edit-config's <config> element. Per-element operations are expressed the standard way, with an nc:operation attribute; pair that with default_operation: none so the device changes only what the document explicitly names. |
| `datastore` | `string` | no | `running` | The datastore to configure: running, candidate or startup. A datastore the device does not advertise support for is refused before anything is applied. Cisco IOS XE offers only running. |
| `default_operation` | `string` | no | `merge` | What the device does with elements carrying no explicit operation attribute: merge, replace or none. RFC 6241 defines no "delete" here; express a delete with an nc:operation attribute in content. |
| `error_option` | `string` | no | `rollback-on-error when the device supports it, otherwise the device's own stop-on-error default` | How the device handles a rejected element: stop-on-error, continue-on-error or rollback-on-error. Left unset this method asks for rollback-on-error whenever the device advertises the capability, because stop-on-error leaves a rejected document half applied. |
| `lock` | `string` | no | `never` | Whether to lock the datastore for the duration: never, always, or if_supported. Locking prevents another client changing the datastore mid-edit; it also blocks every other client, which matters on a shared device. |
| `commit` | `bool` | no | `true` | Commit after a successful edit. Only meaningful when datastore is candidate, since a running-datastore edit is already live; ignored otherwise. |
| `backup` | `bool` | no | `false` | Capture the datastore's full contents with get-config before applying anything, recorded under the backup stat. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `backup` | `string` | when backup is true | The datastore's full contents as XML, captured immediately before this task's own document was applied. Not sanitized: a device's configuration genuinely contains its enable secret, local user password hashes, and any TACACS+/RADIUS shared key, in whatever strength of encoding the device applies. Mask it with "register_mask: backup" on this task. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

An edit-config merge has no reliable inverse: deleting what was added is not the same as restoring what was replaced, and this method cannot know which of the two a given element did. Set backup: true to capture the prior configuration for a human-directed rollback. Note that rollback-on-error, which this method requests whenever the device supports it, covers a DIFFERENT case: it undoes a partially applied edit that the device itself rejected, not one that succeeded and was later regretted.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ios.config`
- `net.ios.save`
- `net.cli.config`

## Examples

Set a device's hostname over NETCONF:

```yaml
- name: Set the hostname
  net.netconf.config:
    content: |
      <native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">
        <hostname>edge-01</hostname>
      </native>
    backup: true
  register_mask: backup
```

Remove an interface, changing nothing else:

```yaml
- name: Remove the loopback
  net.netconf.config:
    default_operation: none
    content: |
      <native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">
        <interface>
          <Loopback xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="delete">
            <name>8990</name>
          </Loopback>
        </interface>
      </native>
```

