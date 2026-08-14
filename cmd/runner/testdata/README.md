# Injection release-gate reference data

## `awx_reference_env.json`

The environment an AWX/Ansible Automation Platform job template is expected to
present to a playbook when the `Custom REST API Token` credential type defined in
`ansible_injection_release_gate_test.go` is bound to it, with the control values
that test supplies.

The gate compares this file, byte for byte, against the environment a real
`ansible-playbook` run inside a real ephemeral container actually observed.

### What "byte-identical" means here, precisely

It is the comparison over the **injected subset only**, sorted, and that filter
is stated rather than implied. A container's own `PATH`, `HOSTNAME`, `PWD`,
`HOME`, the base image's Python variables, and the three `ANSIBLE_*` variables
this platform's own adapter sets all legitimately differ between AWX and here,
and several of them differ between two runs of the same image. Comparing the
whole environment would be a claim the gate cannot support, so it compares the
three variables the credential type declares and says so.

### Provenance, stated because it is a limitation

**This file was written from AWX's documented injector semantics. It was not
captured from a running AWX or Ascender deployment.**

That distinction matters and is recorded here rather than left for somebody to
assume the stronger thing. What the file gives is an independent statement of the
expected result, written from the specification rather than derived from this
repository's own code, so the gate is not a test comparing an implementation
against itself. What it does not give is confirmation from a live deployment that
AWX in practice matches its own documentation for this shape of type.

Replacing it with a real capture is a bounded, worthwhile follow-up: run
`ansible-playbook` under a job template bound to a credential of the type below,
with a debug task dumping the same three variables, and paste the result here
alongside the AWX version it came from. Nothing in the gate changes when that
happens; only this paragraph and the file do.

### The credential type this reflects

```json
{
  "name": "Custom REST API Token",
  "kind": "cloud",
  "namespace": "custom_api_token",
  "inputs": {
    "fields": [
      {"id": "api_token", "type": "string", "label": "API Bearer Token", "secret": true},
      {"id": "api_url", "type": "string", "label": "API Base URL"}
    ],
    "required": ["api_token", "api_url"]
  },
  "injectors": {
    "env": {
      "REST_API_TOKEN": "{{ api_token }}",
      "REST_API_URL": "{{ api_url }}",
      "REST_API_CONFIG": "{{ tower.filename }}"
    },
    "extra_vars": {
      "ansible_api_url": "{{ api_url }}",
      "ansible_api_token": "{{ api_token }}"
    },
    "file": {"template": "token={{ api_token }}\nurl={{ api_url }}\n"}
  }
}
```

`REST_API_CONFIG` is the reserved-namespace case, and it is the one entry whose
expected value is not simply an input echoed back: AWX resolves
`{{ tower.filename }}` to the path of the file the type's `file` injector
generates. Here that path is `/run/pleiades/credentials/<credential id>`, and the
gate binds credential id 18 so the value is stable. AWX writes its generated
credential files somewhere else entirely, under a per-job private data directory,
so this is the one value that is deliberately **not** expected to match a real
AWX capture verbatim: what has to match is that the variable resolves to the path
of a file that exists and holds the rendered content, which the gate checks
separately by reading it from inside the container.
