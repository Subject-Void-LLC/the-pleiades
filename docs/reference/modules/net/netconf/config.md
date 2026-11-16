---
status: beta
---

# net.netconf.config

Applies a configuration document to a device over NETCONF, reporting whether it changed, with its undo where exact, an optional pre-change backup, and a gated check through the candidate datastore.

Opens an RFC 6241 NETCONF session over the SSH "netconf" subsystem and applies content to the chosen datastore with edit-config. Parameter names are ansible.netcommon.netconf_config's own, with its target parameter under its own alias, datastore, since target names the device a task runs on. The session negotiates RFC 6242 chunked framing whenever the device offers base:1.1, and requests rollback-on-error whenever the device advertises it, so a rejected document leaves the device unchanged rather than half configured; that matters most on a device offering only writable-running, which is what Cisco IOS XE offers until its candidate datastore is configured, because such a device has no staging area and every element lands on the live configuration as it is applied. A datastore the device never advertised support for is refused when the session opens rather than at the first write, naming the missing capability. Unlike the net.cli.* and net.ios.config methods, a rejected element comes back as a structured error carrying the device's own error-tag and the XPath of the element it objected to. Whether the task changed anything is read, not assumed: the configuration the edit can touch is read with get-config before and after the edit (running, or startup for a write to startup), through a subtree filter built from the content that selects every instance of each element's name by name alone, never narrowed by a value, in RFC 6243 with-defaults explicit mode where the device offers it and otherwise in its own basic mode; the two reads are compared in document order with namespaces resolved and white space between elements set aside, so a re-run that leaves the configuration as it found it reports no change. A default_operation of replace reads the whole datastore. A candidate edit with commit false is validated and discarded and reports no change. A run that changes the configuration records its undo where the reads prove one exact, and says why when it records none. A check runs only with candidate_check: true, through the candidate datastore.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetconfCapable`, `SSHTransportCapable` |
| Transports | `netconf` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported for some calls, named in the description: those report what they would change and change nothing, a check run names any other as unchecked, and validation refuses check_mode on one |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `content` | `string` | yes | - | **Accepts `{{ secret.<name>.<field> }}`:** The configuration document to apply, as the XML that goes inside edit-config's <config> element: well-formed, holding no DOCTYPE, nesting no deeper than 256 elements, and holding no masked placeholder (pleiades-masked-), each refused before a session opens. Per-element operations are expressed the standard way, with an nc:operation attribute; pair that with default_operation: none so the device changes only what the document explicitly names. A leaf may name a stored secret, such as an IKE pre-shared key, with a secret reference, {{ secret.<name>.<field> }} (pleiades add-secret): it is resolved just before the session opens, escaped as XML text, and masked in everything the run reports; the content is never recorded, and a call whose content names a secret records no undo. A document holding one holds no other template expression, so a per-device value and a secret go in separate tasks, and a value with whitespace, a control character or ? is refused. |
| `datastore` | `string` | no | - | The datastore to configure: running, candidate or startup. Left unset it is chosen when the session opens, from what the device's hello advertises that session: running when it offers :writable-running, otherwise candidate (locked, validated when the device offers :validate, then committed). That is chosen afresh on every task, since a device's offer changes with its configuration: Cisco IOS XE offers only running until netconf-yang feature candidate-datastore is configured, and then only candidate. A datastore the device does not advertise is refused before anything is applied. **Choices:** `running`, `candidate`, `startup` (case ignored). |
| `default_operation` | `string` | no | `merge` | What the device does with elements carrying no explicit operation attribute: merge, replace or none. RFC 6241 defines no "delete" here; express a delete with an nc:operation attribute in content. **Choices:** `merge`, `replace`, `none`. |
| `error_option` | `string` | no | - | How the device handles a rejected element: stop-on-error, continue-on-error or rollback-on-error. Left unset this method asks for rollback-on-error whenever the device advertises the capability, and otherwise leaves the device its own default, stop-on-error, because stop-on-error leaves a rejected document half applied. **Choices:** `stop-on-error`, `continue-on-error`, `rollback-on-error`. |
| `lock` | `string` | no | `never` | Whether to lock the datastore for the duration: never, always, or if_supported. Locking prevents another client changing the datastore mid-edit; it also blocks every other client, which matters on a shared device. The candidate datastore is always locked, and never is refused there: the lock is refused while another client's changes sit in the candidate, so this task commits and discards only its own edit. **Choices:** `never`, `always`, `if_supported` (case ignored). |
| `commit` | `bool` | no | `true` | On the candidate datastore, commit after the edit, which is validated first when the device offers :validate. false validates the edit and discards it, so it is checked by the device and applied nowhere, and reports no change, with validated and applied saying what ran; unlike ansible.netcommon.netconf_config it never leaves the edit staged, where the next commit by any client would apply it. false is refused when the session writes running, where an edit is live at once. |
| `candidate_check` | `bool` | no | `false` | In a check (pleiades run --mode check, or check_mode: true), check this task through the device's candidate datastore rather than report it unchecked. The check takes the candidate's lock first (a lock the device refuses, as it does while the candidate holds changes not committed, reports the task unchecked with nothing sent), proves the candidate holds nothing uncommitted in what the content touches, stages the edit there, has the device validate it, reads it, and discards it, proving the candidate reads as it did before; it holds the lock for at most 30 seconds, after which the session is closed. Only a check's own RPCs can be sent (lock, unlock, edit-config and validate of the candidate, discard-changes, reads and close-session): a commit, or a write to running or startup, is refused before it is written. Its result says validated; commit not exercised, since a device may refuse at commit what it validated, and changed says whether the edit changed the candidate. It runs only from the command line, never on a simulate-locked device, never for a write to startup or content naming a secret, only when this session's own hello advertises :candidate and :validate, and only on a generic_netconf device whose server is Netopeer2 over sysrepo, the one server measured to refuse the lock on a candidate holding another session's changes, to leave the candidate as running when a session is killed after its edit, and to restore it on a discard. Any failure between sending the edit and proving the discard fails the check as candidate state unverified. A real run ignores it. |
| `backup` | `bool` | no | `false` | Capture the running datastore's full contents with get-config before applying anything, whichever datastore the task writes, recorded under the backup stat with every secret masked. A configuration masking cannot vouch for fails the task before anything is applied. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `validated` | `bool` | when commit is false | Whether the device validated the candidate edit before it was discarded: false on a device that does not offer :validate, where only the edit itself was accepted. |
| `applied` | `bool` | when commit is false | Always false: the edit was discarded, not applied. |
| `candidate_check` | `string` | in a check through the candidate datastore | What the check proved: validated; commit not exercised. The device validated the edit in its locked candidate datastore, the edit was discarded, and the candidate was proven to read as it did before; a device may still refuse at commit what it validated. |
| `backup` | `string` | when backup is true | The datastore's full contents as XML, captured immediately before this task's own document was applied, with every secret masked: the text of each leaf whose name holds a secret (password, secret, a key such as auth-key or pre-shared-key, encrypted-password, RFC 8177's key-string, keystring and hexadecimal-string, OpenConfig's key-clear-text, an SNMP community), of every other leaf inside such an element at any depth but its encryption type, id, algorithm or lifetime (and, two or more levels down outside SNMP, a name or address), and of each crypt-shaped value ($1$, $6$, $9$) is replaced by pleiades-masked- and sixteen hexadecimal digits, equal secrets alike within one run; the run's own credential is masked wherever it appears, other leaves are read by the configuration text masker net.ios.config's backup uses, and comments are withheld. Every element, name, address and encryption type stays where it was. A document that is not well-formed XML, holds a DOCTYPE, nests past 256 elements, has an attribute that may hold a secret, or has a line the text masker cannot vouch for fails the task before anything is applied, and none of it is recorded. net.netconf.config refuses content holding a placeholder, so this text cannot be applied back as it is. |
| `netconf.capabilities` | `list of string` | on every session, as a stat; as a fact only when its host key was verified | Every capability the device's hello advertised that names no YANG module (RFC 6241's own and a vendor's), with its parameters but the volatile content-id and module-set-id, which yang.content_id holds, sorted; the YANG module capabilities are in yang.modules instead. Left out, with a warning, when it does not fit in 60 KiB. Read on every session, so a device that changes what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) reads as a change. |
| `netconf.datastores` | `list of string` | on every session, as a stat; as a fact only when its host key was verified | The datastores the session could use, sorted: running always, candidate and startup when the hello advertises them, and every datastore the device's YANG library lists (an NMDA device's operational and intended). |
| `netconf.modules` | `list of string` | on every session, as a stat; as a fact only when its host key was verified | The modules NETCONF's server lists for itself, which yang.modules leaves out (owner decision 2026-11-15: yang.* is protocol-neutral), each as name@revision, sorted, from the measured list of protocol modules: ietf-netconf, ietf-netconf-with-defaults and tailf-netconf-extensions over NETCONF, and tailf-rest-query over RESTCONF, each measured on a Catalyst 8000V running IOS XE 17.15.1a on 2026-11-15 as one protocol's library listed it and the other's did not. Empty when the library lists none of them; left out, with a warning, when the list does not fit in 60 KiB. |
| `yang.modules` | `list of string` | when the list fits in 60 KiB as JSON | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one and from the hello otherwise. The protocols' own modules, which each server lists for itself, are left out (they are in netconf.modules or restconf.modules), so the list is the same whichever protocol read it. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | `int` | on every session, as a stat; as a fact only when its host key was verified | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | `string` | on every session, as a stat; as a fact only when its host key was verified | The SHA-256 (hex) of the device's data models in their canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), the protocols' own modules left out as yang.modules leaves them, so one device read over NETCONF and over RESTCONF gives one value, two devices implementing the same models read alike, and any change to the set reads as a change of this one value. |
| `yang.digest_rule` | `string` | on every session, as a stat; as a fact only when its host key was verified | The rule yang.modules_digest was computed under: which modules it leaves out as the protocols' own, as protocol-modules- and 12 hex digits that change whenever that list changes. Compare two digests only when their rules are equal: a release that adds a module to the protocols' list changes every device's digest once, the devices unchanged. |
| `yang.completeness` | `string` | on every session, as a stat; as a fact only when its host key was verified | How far yang.modules is every module the device implements: complete (read from the YANG library, or from the hello of a device with none), partial (a library that could not be read, or came back empty for an account the device's access control hides it from), or unknown (a device that named no modules). A document is refused for a namespace the device does not serve only when this is complete. |
| `yang.content_id` | `string` | when the device advertises yang-library:1.1 | The YANG library's content-id from the hello, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed the configuration records net.netconf.config with default_operation none and, as content, the undo of what it did, where the reads before and after the edit prove one exact. An element the read before holds no instance of was created, and its undo removes it (named by its own leaves, where a list entry's keys are). An element the read names for certain (a container, which carries no leaf; or the one instance agreeing with the content on every leading leaf it names, where RFC 7950 puts a list entry's keys) is recurred into, and where a value of it changes, or a part of it cannot be named, it is put back whole, nc:operation replace, as it was read. That undo is decided from the read before, so it is recorded before the edit is sent: an edit whose answer is lost still leaves it, and it stands if the task fails, since running it where nothing changed changes nothing. A list entry the read cannot name (one whose leading leaves are what the edit changes, directly under containers) is decided from the instances of its name before and after the edit: one new is removed, one changed into one other is put back; that undo can be recorded only once the read after answers, so a lost answer leaves none, and the run says so. No undo is recorded, and the run says why, for content naming a secret (so no secret reaches the run's journal); for default_operation replace, which replaces the whole datastore; for a put-back from a device that does not read in with-defaults explicit mode; for an undo longer than the 256 bytes on one line the run's journal keeps of a value, which most put-backs of a real device's configuration are; for one holding anything masking would hide (a secret-named leaf, a crypt-shaped value, the run's own credential); for a value directly under containers, which no read tells a leaf from a leaf-list for; and for a change the reads cannot attribute. A put-back holds configuration read from the device, which the run's journal keeps: masking decides it holds no secret, so a secret in a leaf whose name says nothing of one would be kept. A node the account's access control hides from its reads reads as absent: an undo recorded before an edit whose answer is lost would then remove it, and when both reads hide it they agree and the undo is withdrawn. A presence container the edit creates above what its undo removes is left in place. The reads count what another client changes at the same moment as this task's. A run that changed nothing records nothing.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `netconf.capabilities` | stable | list | Every capability the device's hello advertised that names no YANG module (RFC 6241's own and a vendor's), with its parameters but the volatile content-id and module-set-id, which yang.content_id holds, sorted; the YANG module capabilities are in yang.modules instead. Left out, with a warning, when it does not fit in 60 KiB. Read on every session, so a device that changes what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) reads as a change. |
| `netconf.datastores` | stable | list | The datastores the session could use, sorted: running always, candidate and startup when the hello advertises them, and every datastore the device's YANG library lists (an NMDA device's operational and intended). |
| `netconf.modules` | stable | list | The modules NETCONF's server lists for itself, which yang.modules leaves out (owner decision 2026-11-15: yang.* is protocol-neutral), each as name@revision, sorted, from the measured list of protocol modules: ietf-netconf, ietf-netconf-with-defaults and tailf-netconf-extensions over NETCONF, and tailf-rest-query over RESTCONF, each measured on a Catalyst 8000V running IOS XE 17.15.1a on 2026-11-15 as one protocol's library listed it and the other's did not. Empty when the library lists none of them; left out, with a warning, when the list does not fit in 60 KiB. |
| `yang.modules` | stable | list | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one and from the hello otherwise. The protocols' own modules, which each server lists for itself, are left out (they are in netconf.modules or restconf.modules), so the list is the same whichever protocol read it. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | stable | integer | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | stable | string | The SHA-256 (hex) of the device's data models in their canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), the protocols' own modules left out as yang.modules leaves them, so one device read over NETCONF and over RESTCONF gives one value, two devices implementing the same models read alike, and any change to the set reads as a change of this one value. |
| `yang.digest_rule` | stable | string | The rule yang.modules_digest was computed under: which modules it leaves out as the protocols' own, as protocol-modules- and 12 hex digits that change whenever that list changes. Compare two digests only when their rules are equal: a release that adds a module to the protocols' list changes every device's digest once, the devices unchanged. |
| `yang.completeness` | stable | string | How far yang.modules is every module the device implements: complete (read from the YANG library, or from the hello of a device with none), partial (a library that could not be read, or came back empty for an account the device's access control hides it from), or unknown (a device that named no modules). A document is refused for a namespace the device does not serve only when this is complete. |
| `yang.content_id` | volatile | string | The YANG library's content-id from the hello, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |

## See also

- `net.ios.config`
- `net.ios.save`
- `net.cli.config`
- `net.restconf.config`

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

