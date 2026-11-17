---
status: beta
---

# net.restconf.facts

Records what a device's RESTCONF server says about it: its root, capabilities, datastores and YANG modules.

Finds the device's RESTCONF root from its /.well-known/host-meta (RFC 8040 section 3.1), read with no credential; a host-meta answered 404 or a redirect, which is never followed (IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page, which answers 404, measured on 17.15.1a, 2026-11-15), leaves RFC 8040's conventional /restconf, which the first authenticated read under it confirms (and restconf.root_source says which way the root was learned), while a 404 there too refuses the device. Then it reads the capabilities ietf-restconf-monitoring lists and the device's YANG library (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it), and records them, changing nothing: the same yang.* facts net.netconf.facts records over NETCONF, and RESTCONF's own root, capabilities and datastores. The yang.* facts are protocol-neutral: each protocol's library also lists its own modules (measured on a Catalyst 8000V running IOS XE 17.15.1a, NETCONF's ietf-netconf, ietf-netconf-with-defaults and tailf-netconf-extensions, RESTCONF's tailf-rest-query), which yang.* leaves out and restconf.modules (or netconf.modules over NETCONF) records, so one device read over either protocol gives the same yang.modules, yang.module_count and yang.modules_digest. The device is reached at its own origin with its own TLS (always verified, a pinned certificate included) and its own stored credential, following no redirect, and a root its host-meta names on another scheme, host or port is refused. A read whose capabilities and library both failed fails the task rather than recording a device with nothing, as onboarding refuses it; a family whose own read failed is left out, with a warning. A device reached over http:// records its findings as stats only, never as facts, since nothing proves which server answered. Reports no change, and a check runs it for real.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `RestconfCapable` |
| Transports | `restconf` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | No exists return: it describes the device itself; a device not there is unreachable, not absent |
| Engine version | `>=0.4.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `restconf.root` | `string` | on every read, as a stat; as a fact only when the device was reached over https | The RESTCONF root, as an escaped path (such as /restconf), or / for a root at the device's origin itself: the one the device's /.well-known/host-meta names, or the conventional /restconf when host-meta answered 404 or a redirect and a read under it answered (restconf.root_source says which). |
| `restconf.root_source` | `string` | on every read, as a stat; as a fact only when the device was reached over https | How restconf.root was learned: host-meta (its restconf link) or default (host-meta answered 404 or a redirect, as IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page (404 there), and a read under the conventional /restconf answered). |
| `restconf.capabilities` | `list of string` | when the capability list was read and fits, as a stat; as a fact only when the device was reached over https | Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Left out, with a warning, when the list could not be read or does not fit in 60 KiB, so an unread list never reads as an empty one; an entry that is not printable is left out whole. |
| `restconf.datastores` | `list of string` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does. |
| `restconf.modules` | `list of string` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | The modules RESTCONF's server lists for itself, which yang.modules leaves out (owner decision 2026-11-15: yang.* is protocol-neutral), each as name@revision, sorted, from the measured list of protocol modules: ietf-netconf, ietf-netconf-with-defaults and tailf-netconf-extensions over NETCONF, and tailf-rest-query over RESTCONF, each measured on a Catalyst 8000V running IOS XE 17.15.1a on 2026-11-15 as one protocol's library listed it and the other's did not. Empty when the library lists none of them; left out, with a warning, when the list does not fit in 60 KiB. |
| `yang.modules` | `list of string` | when the list fits in 60 KiB as JSON | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library over RESTCONF (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it). The protocols' own modules, which each server lists for itself, are left out (they are in netconf.modules or restconf.modules), so the list is the same whichever protocol read it. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | `int` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | `string` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | The SHA-256 (hex) of the device's data models in their canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), the protocols' own modules left out as yang.modules leaves them, so one device read over NETCONF and over RESTCONF gives one value, two devices implementing the same models read alike, and any change to the set reads as a change of this one value. |
| `yang.digest_rule` | `string` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | The rule yang.modules_digest was computed under: which modules it leaves out as the protocols' own, as protocol-modules- and 12 hex digits that change whenever that list changes. Compare two digests only when their rules are equal: a release that adds a module to the protocols' list changes every device's digest once, the devices unchanged. |
| `yang.completeness` | `string` | when the YANG library was read, as a stat; as a fact only when the device was reached over https | How far yang.modules is every module the device implements: complete (the YANG library read whole) or partial (a library that dropped a malformed or oversized module entry; a refused capability does not count). A library that could not be read, or came back empty as it does for an account the device's access control hides it from, records no yang.* fact at all, so it never reads as a device that implements no module. |
| `yang.content_id` | `string` | when the device's YANG library names one | The YANG library's content-id (RFC 8525's content-id, or RFC 7895's module-set-id) from the library itself, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |
| `tls.certificate.fingerprint` | `string` | when the device was reached over TLS and presented one certificate, as a stat and a fact | The SHA-256 of the certificate the device presented, sha256:<hex>, the form a certificate pin (tls_fingerprint) takes: it changes whenever the certificate does, renewed or not. |
| `tls.certificate.key_fingerprint` | `string` | when the device was reached over TLS and presented one certificate, as a stat and a fact | The SHA-256 of its public key (SubjectPublicKeyInfo), spki-sha256:<hex>, the form a key pin takes: it stays the same across a certificate the device issues again for the same key. |
| `tls.certificate.subject` | `string` | when the device was reached over TLS and presented one certificate, as a stat and a fact | The certificate's subject, as text, with no control or format character and at most 512 bytes. |
| `tls.certificate.issuer` | `string` | when the device was reached over TLS and presented one certificate, as a stat and a fact | The certificate's issuer, as text, bounded as the subject is: the subject again for a self-signed certificate. |
| `tls.certificate.not_after` | `string` | when the device was reached over TLS and presented one certificate, as a stat and a fact | The last moment the certificate is valid, RFC 3339 in UTC. A read within 30 days of it, or past it, warns, in words that fit how the device is trusted: a certificate pin refuses the renewed certificate until it is pinned too. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## Facts it keeps

A real run stores each of these as the device's fact. A **stable** fact keeps every value it has had, and a change is drift; a **volatile** one keeps only its latest value and never counts as drift. A check stores none.

| Fact | Kind | Type | Description |
| --- | --- | --- | --- |
| `restconf.root` | stable | string | The RESTCONF root, as an escaped path (such as /restconf), or / for a root at the device's origin itself: the one the device's /.well-known/host-meta names, or the conventional /restconf when host-meta answered 404 or a redirect and a read under it answered (restconf.root_source says which). |
| `restconf.root_source` | stable | string | How restconf.root was learned: host-meta (its restconf link) or default (host-meta answered 404 or a redirect, as IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page (404 there), and a read under the conventional /restconf answered). |
| `restconf.capabilities` | stable | list | Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Left out, with a warning, when the list could not be read or does not fit in 60 KiB, so an unread list never reads as an empty one; an entry that is not printable is left out whole. |
| `restconf.datastores` | stable | list | The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does. |
| `restconf.modules` | stable | list | The modules RESTCONF's server lists for itself, which yang.modules leaves out (owner decision 2026-11-15: yang.* is protocol-neutral), each as name@revision, sorted, from the measured list of protocol modules: ietf-netconf, ietf-netconf-with-defaults and tailf-netconf-extensions over NETCONF, and tailf-rest-query over RESTCONF, each measured on a Catalyst 8000V running IOS XE 17.15.1a on 2026-11-15 as one protocol's library listed it and the other's did not. Empty when the library lists none of them; left out, with a warning, when the list does not fit in 60 KiB. |
| `yang.modules` | stable | list | Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read from the device's YANG library over RESTCONF (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it). The protocols' own modules, which each server lists for itself, are left out (they are in netconf.modules or restconf.modules), so the list is the same whichever protocol read it. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded. |
| `yang.module_count` | stable | integer | How many YANG modules the device implements, as yang.modules counts them. |
| `yang.modules_digest` | stable | string | The SHA-256 (hex) of the device's data models in their canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), the protocols' own modules left out as yang.modules leaves them, so one device read over NETCONF and over RESTCONF gives one value, two devices implementing the same models read alike, and any change to the set reads as a change of this one value. |
| `yang.digest_rule` | stable | string | The rule yang.modules_digest was computed under: which modules it leaves out as the protocols' own, as protocol-modules- and 12 hex digits that change whenever that list changes. Compare two digests only when their rules are equal: a release that adds a module to the protocols' list changes every device's digest once, the devices unchanged. |
| `yang.completeness` | stable | string | How far yang.modules is every module the device implements: complete (the YANG library read whole) or partial (a library that dropped a malformed or oversized module entry; a refused capability does not count). A library that could not be read, or came back empty as it does for an account the device's access control hides it from, records no yang.* fact at all, so it never reads as a device that implements no module. |
| `yang.content_id` | volatile | string | The YANG library's content-id (RFC 8525's content-id, or RFC 7895's module-set-id) from the library itself, a hint the device may regenerate on restart; yang.modules_digest is the module set's identity. |
| `tls.certificate.fingerprint` | stable | string | The SHA-256 of the certificate the device presented, sha256:<hex>, the form a certificate pin (tls_fingerprint) takes: it changes whenever the certificate does, renewed or not. |
| `tls.certificate.key_fingerprint` | stable | string | The SHA-256 of its public key (SubjectPublicKeyInfo), spki-sha256:<hex>, the form a key pin takes: it stays the same across a certificate the device issues again for the same key. |
| `tls.certificate.subject` | stable | string | The certificate's subject, as text, with no control or format character and at most 512 bytes. |
| `tls.certificate.issuer` | stable | string | The certificate's issuer, as text, bounded as the subject is: the subject again for a self-signed certificate. |
| `tls.certificate.not_after` | stable | string | The last moment the certificate is valid, RFC 3339 in UTC. A read within 30 days of it, or past it, warns, in words that fit how the device is trusted: a certificate pin refuses the renewed certificate until it is pinned too. |

## See also

- `net.netconf.facts`

## Examples

Read what a device offers over RESTCONF:

```yaml
- name: Discover the device's YANG modules over RESTCONF
  net.restconf.facts:
  register: discovered
```

