# ServiceNo!

A local mock of the ServiceNow Table API, for testing a ticket runbook without an instance. It
answers the part of the Table API such a runbook uses, shaped the way ServiceNow shapes it, and says
no to the rest. One Python file, standard library only, Python 3.9 or later; everything is held in
memory and lost when it stops.

## Run it

```bash
python3 tests/serviceno/serviceno.py --port 8443 --password s3cret
# or over HTTPS, as an instance is reached:
python3 tests/serviceno/serviceno.py --port 8443 --password s3cret \
    --tls-cert cert.pem --tls-key key.pem
```

| Flag | Meaning |
|---|---|
| `--port`, `--host` | Where to listen (default `0.0.0.0:8443`). |
| `--user`, `--password` | The basic-auth account (default user `admin`; the password is required). |
| `--token` | Also accept this bearer token. |
| `--tls-cert`, `--tls-key` | Serve HTTPS with this PEM certificate chain and key (TLS 1.2 at least). |
| `--base` | The instance URL reference links point at (default: derived from the listener). |
| `--seed` | A JSON file of `{"table": [record, ...]}` to load at start. |

Point a `generic_http` device at it and run a runbook as you would against an instance:

```bash
pleiades add-host snow --type generic_http --set base_url=http://127.0.0.1:8443 --set http_auth=basic \
    --set http_allow_plaintext_credentials=true
pleiades add-credential snow --username admin --password s3cret
pleiades onboard snow
```

Over plain HTTP a credential needs `http_allow_plaintext_credentials`, which The Pleiades warns
about on every run; serve HTTPS to avoid it.

## What it answers

- `GET /api/now/table/<table>` with `sysparm_query` (conditions `field=value`, `field!=value`,
  `fieldLIKEvalue` and `fieldSTARTSWITHvalue`, ANDed with `^`, plus `ORDERBY` and `ORDERBYDESC`;
  `^OR` and `^NQ` are refused rather than matched loosely), `sysparm_limit`, `sysparm_offset`,
  `sysparm_fields`, `sysparm_display_value` (`false`, `true`, `all`) and
  `sysparm_exclude_reference_link`, with `X-Total-Count`.
- `GET`, `PATCH`, `PUT` and `DELETE` on `/api/now/table/<table>/<sys_id>`, and `POST` to create.
- Tables `incident`, `cmdb_ci`, `sys_user` and `sys_journal_field`. A new incident gets a number
  (`INC0010001` onward), state New and priority 4 unless given.
- `incident.cmdb_ci` and `assigned_to` are reference fields, returned as `{"link", "value"}`, the
  target's name, or all three, by display mode. `priority`, `impact`, `urgency` and `state` are choice
  fields with a stock instance's labels (`1 - Critical`, `In Progress`).
- `work_notes` and `comments` are journal fields: writing one adds a `sys_journal_field` entry
  (`element_id`, `element`, `value`), and reading the incident returns them empty, as the Table API
  does. Read them back from `sys_journal_field`.
- ServiceNow's error envelope, `{"error": {"message", "detail"}, "status": "failure"}`, for 401,
  400 and 404.

## How The Pleiades uses it

`cmd/pleiades/servicenow_runbook_release_gate_test.go` runs it in a `python:3-alpine` container over
TLS, so Python is never a build or CI dependency, and drives a ticket runbook through the real
`pleiades` binary: read an incident, collect from the switch its configuration item names, and write
the finding to its work notes. Setting `PLEIADES_SNOW_INSTANCE`, `PLEIADES_SNOW_USER` and
`PLEIADES_SNOW_PASSWORD` runs the same test against a real instance.
