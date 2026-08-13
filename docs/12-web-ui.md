# The web UI

The control plane serves its own web interface. `cmd/controller` mounts it at `/ui`,
same-origin, from assets compiled into the binary — there is no separate front-end
build, no Node toolchain, no second container and no reverse proxy between the two.

Browse to the controller's address and add `/ui`. In a default local run that is
<http://localhost:8080/ui>.

## Why it is built this way

Pleiades targets air-gapped and high-security networks. In that setting a Node build
chain, an npm dependency tree, a separate nginx image and a client-side permission
table are all liabilities, and a CDN reference is not a slow page — it is a page that
never renders, with no configuration setting that rescues it at runtime.

So every asset the UI serves is embedded in the binary, including the two third-party
JavaScript bundles it uses. Their licences, notices, upstream URLs and SHA-256 digests
are recorded in `internal/ui/static/vendor/PROVENANCE.md`, and a test asserts the
embedded bytes still hash to the reviewed values, so a silent substitution fails the
build rather than reaching a deployment.

Serving same-origin removes more than a proxy hop. It is what lets a cookie
authenticate the log stream, and it is why the UI needs no CORS configuration at all.

## Signing in

`/ui/login` takes a token this control plane already accepts and exchanges it for a
session cookie. The token is validated by the same evaluator the `Authorization:
Bearer` path uses, so signing in proves an identity exactly the way an API call does.

The UI therefore adds no new credential storage, no password hashing and no second
notion of who a caller is. Interactive sign-in through an identity provider is a real
authentication surface of its own — redirect endpoints, state, PKCE, discovery,
refresh — and belongs in its own phase with its own security review. Token paste is
the bootstrap and break-glass path until then.

The session is a row in the shared database, not process-local state. A cookie minted
by one controller authenticates against another, so this forces no session affinity
and no sticky-session load balancer. It is a row rather than a signed token for one
reason: **a signed token cannot be revoked.** Signing out deletes the row, and the
credential stops working everywhere at once.

| Setting | Value |
|---|---|
| Cookie name | `__Host-pleiades_session` |
| Idle timeout | 30 minutes |
| Absolute timeout | 8 hours |
| Attributes | `HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`, no `Max-Age` |

The `__Host-` prefix is browser-enforced rather than conventional: it *requires*
`Secure` and `Path=/` and forbids a `Domain`, which structurally prevents a subdomain
from setting the cookie.

### Running without TLS

`__Host-` requires HTTPS, so a developer on `http://localhost` cannot sign in at all.
`PLEIADES_UI_INSECURE_COOKIES=1` drops the prefix and the `Secure` attribute. The
controller logs a warning at startup whenever it is set.

Never set it in a deployment anyone else can reach.

## What each view does

Thirteen views are registered, grouped in the sidebar the way AWX groups its own, so
an operator arriving from there finds things where they expect them. A view that is
not implemented says so on the page rather than rendering an empty table, because an
empty table and an unimplemented view look identical to a reader, and the difference
between "nothing has happened yet" and "this does not work" is exactly the one worth
being told.

| Group | View | State | What it does |
|---|---|---|---|
| Views | Dashboard | Real | Job outcomes across the most recent 200 dispatches, as a chart and as an equivalent table, plus any live operator announcements. |
| Views | Jobs | Real | List and open jobs, and watch a running job's live output. A job is launched from a Template, so this view has no launch form of its own. |
| Views | Activity Stream | Real | Who changed which managed object, and when. Append-only: it offers no way to edit or remove what it says. |
| Resources | Templates | Real | The saved definitions this platform launches: what to run, where, and how. What it runs is picked from the deployment's own catalog (runbooks, and playbooks when `PLAYBOOK_DIR` is configured), never typed, and the kind badge is derived from that choice. Create, edit, copy, delete and launch, with the survey a launch is asked and the jobs it has run on the record itself. |
| Resources | Credentials | Declared | Unbuilt. Note that it will not become a browsable catalog of credential names: see the absences below. |
| Resources | Runbooks | Real | Read-only catalog of what can be dispatched, with each runbook's required capabilities. Its one action saves a runbook as a template rather than launching it. |
| Resources | Inventories | Real | Create, read, update and delete the shareable device sets a dispatch targets. |
| Resources | Devices | Real | Create, read, update and retire devices. |
| Access | Organizations | Real | The tenancy boundary, with the grants made against it and the contacts accountable for it on the record itself, plus its ownership attestation. |
| Access | Teams | Real | The principals roles are granted to, with what the team reaches and who answers for it, and the same ownership metadata an organization carries. |
| Access | Users | Real | The identities a token's subject maps onto, and their team membership. |
| Access | Contacts | Real | Who is accountable for a tenant or a team, and how to reach them, across the whole deployment. The page an access review reads to find the tenants nobody is named against. |
| Access | Access | Real | Every role binding in the deployment: who reaches what, and where each grant sits. |
| Administration | Governance | Declared | Registered so the shape and navigation are real. Nothing backs it yet. |

Three deliberate absences, each for a stated reason rather than for want of time:

- **Device properties are not editable.** They decrypt to real secrets — enable
  passwords, API keys — and the masking ruleset that would make them safe to render
  belongs to an unbuilt phase. A form field for them would be a secret-exposure
  surface with a friendly label.
- **Jobs cannot be cancelled or deleted.** There is no `job:write` scope and no
  cancellation path anywhere in this build, so offering either would be a button for
  a route nobody mounted.
- **Credentials will not list names even when implemented.** The set of credential
  names in a deployment tells a reader which vendors are present and which accounts
  exist to be attacked. That is reconnaissance, and omitting the secret values does
  not make it safe.

## What you can do is what you are allowed to do

Every control the UI renders is decided by the same authorization chain that enforces
the request, reading the same endpoint definition the router mounts. A button's
scope, link relation, method and URL all come from one value.

There is no permission table in the UI. There is no list of routes in the UI. A
viewer is not shown a delete button, and the delete route refuses a viewer — those
are two readings of one decision rather than two rules kept in agreement.

If authorization cannot be evaluated at all, the page renders **no** action buttons
and a visible alert saying so. It does not quietly present a read-only page, because
"you may not do this" and "we could not find out" are different answers and only one
of them is true.

## Appearance

Three independent axes, all resolved on the server and rendered into the first byte
of the page. There is no flash of the wrong theme and no bootstrap script — which is
also why the content security policy needs no `unsafe-inline`.

| Axis | Values | Control |
|---|---|---|
| Skin | Brutalist, Las Ventanas | Sidebar |
| Theme | System, Light, Dark | Sidebar |
| Accessibility | Off, High contrast | Sidebar |

The accessibility mode is a prominent control rather than a buried preference, and it
is never a degraded mode: every feature, column and action stays exactly where it
was. Only the rendering changes.

## Environment and classification banner

An operator about to run automation should know which environment they are in before
they act, not after. Two environment variables put a marking at the top **and** bottom
of every page, including the login page:

```bash
PLEIADES_BANNER_LEVEL=production
PLEIADES_BANNER_TEXT="EU-WEST-1 // CHANGE FREEZE"
```

Levels include the ordinary environments (`development`, `staging`, `production`) and
the US classification markings (`unclassified` through `top-secret`, plus `sci`), which
render in their published colours.

Three properties are deliberate. An unrecognised level **fails startup** rather than
defaulting — an operator who configured a classification marking and silently got none
would believe a marking was displayed when it was not. There is no way for a signed-in
user to dismiss it. And it survives printing, because a marking that only exists on
screen is not on the document somebody carries out of the room.

## On a phone

The most common tasks are usable on a narrow screen: watching a running job's output,
dispatching a runbook, checking dashboard health, looking up a device.

Tables become cards below `48rem`, labelled from the same field declaration that
produced the column headers. The sidebar collapses into a native disclosure. Touch
targets grow to 44 × 44 px on coarse pointers. Pinch zoom is never disabled.

The live log viewer is the one view designed for a phone first rather than adapted to
one, because it is the thing most likely to be opened away from a desk — usually
because something has already gone wrong. Lines wrap rather than scrolling sideways,
and the log uses dynamic viewport units so a mobile browser's collapsing toolbar
cannot hide the most recent output behind it.

## Accessibility

The target is WCAG 2.2 AA.

Much of it is enforced as a build gate rather than checked at review. Every registered
view is rendered through the real templates and asserted against a shared set of
checks: exactly one `h1` and one `main`, a `lang` attribute, a unique non-empty title,
the skip link first in the tab order, a label for every control, an accessible name on
every link and button, `alt` on every image, no positive `tabindex`, no duplicate ids,
and no `aria-describedby` pointing at something that does not exist. Colour contrast is
computed from the stylesheet's own tokens across every skin and theme combination, so a
palette change that drops a pair below its required ratio fails the build.

Because those run over the registry rather than over a list of pages, a view added
later is held to them automatically.

**This covers roughly the mechanical half of WCAG.** Deleting the Node toolchain also
removed the ability to run axe-core or Lighthouse in CI, and a headless-browser job
would need a browser binary, which is hostile to the air-gap premise. The perceptual
half is the script below.

### Manual verification script

Run this before a release, and after any change to the shared templates or the
stylesheet. It exists because a documented, executed manual gate is worth more than
an automated gate that does not exist.

**Keyboard only** — put the mouse away.

1. Load any view. Press `Tab` once. The first stop must be "Skip to main content".
   Activate it; focus must land in the main region.
2. Tab through a full view. Every stop must have a visible focus indicator, and the
   order must match the visual order.
3. Open a delete confirmation with the keyboard. Focus must move into the dialog,
   `Tab` must not escape it, `Esc` must close it, and focus must return to the button
   that opened it.
4. Submit a form with a required field empty. Focus must move to the error summary.
   Each entry must link to its field.
5. Sign out from the keyboard.

**Screen reader** — VoiceOver, NVDA or Orca.

6. Navigate a list view by headings, then by tables. Row and column headers must be
   announced.
7. Trigger an HTMX fragment swap. The change must be announced.
8. Read the dashboard chart. The canvas must be silent and the table beside it must
   carry the same figures.
9. Read a badge. The status word must be spoken — never colour alone.

**Zoom and reflow.**

10. 400% browser zoom at 1280px, and a 320px-wide viewport. No two-dimensional
    scrolling anywhere; nothing clipped or overlapping.
11. Confirm tables have become cards and each cell still states what it is.

**Preferences.**

12. Set the OS to reduced motion. Nothing may animate, including the log's autoscroll.
13. Set the OS to dark, with the theme control on "System". The page must follow.
14. Windows High Contrast: text, borders and focus indicators must all remain visible.

**Content security policy.**

15. With the browser console open, load a page with a chart. There must be no CSP
    violation, and the chart must render.
16. Confirm the page issues no request to any host but the controller's own.

## Adding a view

A view is two files, and neither of them is a template, a route, a handler, a
navigation entry or any CSS:

```bash
pleiades forge new-view access-reviews \
  --title "Access Reviews" \
  --summary "Who approved what, and when."
```

That writes a declared view and a starter test. Implementing it means adapting an
existing port to a reader (and a writer, if it is genuinely writable), writing the two
translation functions between the domain type and the presentation, and setting the
status to implemented.

One step the generator cannot do for you, which it prints as its last output: **add the
view to `internal/ui/resources/registrars.go`.** A view package nothing imports
registers nothing. It compiles, its tests pass, and it is invisible to the running
binary — no navigation entry, no route and no error.

Everything else is inherited: routing, paging, forms, validation, per-field errors,
CSRF, authorization, the mobile card layout, both skins, both themes, the
accessibility mode, and the conformance suite that will hold the new view to the same
standard as every existing one from the moment it is registered.

See [`docs/11-extending-pleiades.md`](11-extending-pleiades.md) for the other three
extension points.
