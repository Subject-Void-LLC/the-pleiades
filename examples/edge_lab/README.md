# Checking a Cloudflare account with the edge methods

The edge methods ask whether what a Cloudflare account serves is what you meant it to serve:
- whether a tunnel is up, and on how many connectors;
- whether an Access application admits the people it should;
- whether a DNS record points where it should.

They only read, and they fail a run listing every difference they find.

## What is here

- `pleiades/inventory.yaml` names one account, `prod-cf`. Its API token is its own stored credential
  (`pleiades add-credential prod-cf --token-stdin`), never a file here, and it is sent only to the account's
  `api_base`. Give the token read permission for Cloudflare Tunnel, Access (applications and policies, and the
  organization) and the zone's DNS.
- `pleiades/runbooks/edge.yaml` checks three things:
  - the `prod` tunnel is healthy, with two connectors up;
  - the `admin.example.com` Access application's issuer is as expected, and staff is its only policy;
  - `www.example.com` resolves to two addresses, proxied.

## Running it

```sh
cd pleiades
pleiades onboard prod-cf
pleiades validate runbooks/edge.yaml
pleiades run runbooks/edge.yaml --mode check   # the same reads: nothing here changes anything
pleiades run runbooks/edge.yaml
```

## What this example has been run against

`cmd/pleiades`'s edge release gate copies this directory and points `api_base` at a real HTTPS server in the test
process. That server answers as Cloudflare's API documents it. The gate onboards the account, validates the
runbook, and runs it in check mode and for real. It then changes what the server reports and checks that every
difference is named, and that the token appears in no output.

Nothing here has been run against a real Cloudflare account. Put your account id and a token in, and it will be.
