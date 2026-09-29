---
status: beta
---

# win.file.download

Downloads a file onto a Windows host over HTTPS and keeps it only if its SHA-256 matches.

Makes sure path on the host holds the file url serves, verified by sha256. This is ansible.windows.win_get_url with a checksum it will not run without. A file already at path with that digest reports no change and nothing is downloaded. Otherwise the host fetches url with the curl.exe Windows ships, over HTTPS only, redirects included, into a file beside path, and moves it into place only once its digest matches; a mismatch leaves path as it was and fails, naming the digest it got. A file at path with another digest is replaced. The download is the host's own, so url must be reachable from it; nothing passes through the machine running Pleiades. A check reads path's digest and downloads nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsShellCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `url` | `string` | yes | - | The https:// URL to fetch. It may not hold a quote, a space or a control character. |
| `path` | `string` | yes | - | The absolute path on the host to write, as G:\iso\ubuntu.ova. Its folder must exist. It may not hold a quote, a wildcard or a control character. |
| `sha256` | `string` | yes | - | The file's SHA-256, 64 hex digits, as the publisher lists it (Ubuntu's SHA256SUMS, for one). |
| `timeout` | `int` | no | `1800` | How many seconds the download may take. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The file this task made sure of. |
| `sha256` | `string` | always | Its SHA-256, lower case. |
| `size_bytes` | `int` | always | Its size. |
| `diff` | `dict` | always | The SHA-256 at path before this task and after it, empty where there was no file. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A download writes a file, and a file it replaces is not kept; nothing here removes a file yet, so neither a new file nor a replaced one can be undone.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.import_ova`

## Examples

Fetch an Ubuntu cloud image:

```yaml
- name: Fetch the Ubuntu 24.04 cloud image
  win.file.download:
    url: https://cloud-images.ubuntu.com/releases/noble/release-20260926/ubuntu-24.04-server-cloudimg-amd64.ova
    path: G:\PleiadesLab\media\ubuntu-24.04-server-cloudimg-amd64.ova
    sha256: 513a22ebe3982b9387b038f8a2a6dad1af980d4623dca03511312e55ba620b1a
```

