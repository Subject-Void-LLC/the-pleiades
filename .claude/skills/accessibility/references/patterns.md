# Component patterns

Correct implementations for the controls people most often get wrong. Each shows
the anti-pattern it replaces. Keyboard models follow the ARIA Authoring Practices
Guide (APG) — when in doubt, match the platform convention users already know.

Utility used throughout:

```css
.visually-hidden:not(:focus):not(:active) {
  position: absolute;
  width: 1px; height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
```

(`clip-path` over the legacy `clip`; keep `white-space: nowrap` or long strings
collapse to one character per line and some readers mangle them.)

---

## Buttons and links

The single most common defect in modern codebases.

```html
<!-- ✗ Not focusable, no role, no Enter/Space, no disabled semantics -->
<div class="btn" onclick="save()">Save</div>

<!-- ✓ -->
<button type="button" onclick="save()">Save</button>
```

- `type="button"` is required inside a `<form>` or the button submits it.
- Navigates → `<a href>`. Mutates state → `<button>`. "Looks like a button" is a
  CSS problem, not a semantics problem.
- `<a>` without `href` is not focusable and has no link role.
- Avoid `disabled` on buttons users need to understand: a disabled button is
  removed from the tab order and gives no explanation. Prefer an enabled button
  that, when activated, explains what's missing — or `aria-disabled="true"` plus
  a no-op handler, which stays focusable and announceable.
- Loading state: keep the accessible name stable and announce the change.
  `<button aria-busy="true">` plus a live region beats swapping the label to a
  bare spinner.

```jsx
<button type="button" onClick={save} aria-busy={saving} aria-disabled={saving}>
  {saving ? 'Saving…' : 'Save'}
</button>
```

---

## Toggle buttons vs. checkboxes vs. switches

```html
<!-- Two-state control that acts immediately (mute, bold, favorite) -->
<button type="button" aria-pressed="false">Mute</button>

<!-- Form field that is submitted (terms, options) -->
<input type="checkbox" id="terms"><label for="terms">I agree</label>

<!-- On/off setting applied immediately -->
<button type="button" role="switch" aria-checked="false">
  <span>Email notifications</span>
</button>
```

Do not change the *label* to reflect state ("Mute" → "Unmute") **and** set
`aria-pressed` — that double-announces. Pick one: a stable label + state, or a
changing label + no state attribute. Stable label + state is better.

---

## Modal dialog

Use the platform. `<dialog>` gives you the top layer, a focus trap, `Esc` to
close, background inertness, and focus restore — all things people write 200
lines of buggy JS to reproduce.

```html
<button type="button" id="open">Edit profile</button>

<dialog id="dlg" aria-labelledby="dlg-title">
  <h2 id="dlg-title">Edit profile</h2>
  <form method="dialog">
    <label for="name">Display name</label>
    <input id="name" name="name" autofocus>
    <button value="cancel">Cancel</button>
    <button value="save">Save</button>
  </form>
</dialog>

<script>
  const dlg = document.getElementById('dlg');
  document.getElementById('open').addEventListener('click', () => dlg.showModal());
</script>
```

- `showModal()` — not `show()`, and not `dlg.open = true`. Only `showModal()`
  traps focus and makes the rest of the page inert.
- Give it an accessible name via `aria-labelledby` pointing at the heading.
- The heading inside the dialog should be the focus target if there is no
  obvious first field.
- Non-modal panels/drawers that overlay content: add `inert` to the background
  container yourself, and restore focus on close.
- Do not put `role="dialog"` on a `<dialog>`; it already has it.
- Style the scrim with `#dlg::backdrop`.

**Never**: a `<div class="modal">` with a click-outside handler, no focus
management, and no `Esc` — background content stays in the tab order and screen
reader users can wander behind the modal without knowing it exists.

---

## Disclosure (accordion, show more, FAQ)

```html
<h3>
  <button type="button" aria-expanded="false" aria-controls="sect1">
    Shipping options
  </button>
</h3>
<div id="sect1" hidden>…</div>
```

- Toggle both `aria-expanded` and the `hidden` attribute.
- The button goes *inside* the heading, so heading navigation still works.
- `<details>/<summary>` is fine and simpler when you don't need custom animation
  or "only one open at a time".
- Content hidden with `hidden`/`display:none` is correctly removed from the a11y
  tree. Content hidden with `opacity: 0` / `height: 0` / off-screen positioning is
  **not** — it stays focusable and readable. This is the classic "invisible menu
  eats 30 tab stops" bug.

---

## Tabs

```html
<div role="tablist" aria-label="Account settings">
  <button role="tab" id="t1" aria-selected="true"  aria-controls="p1" tabindex="0">Profile</button>
  <button role="tab" id="t2" aria-selected="false" aria-controls="p2" tabindex="-1">Billing</button>
</div>
<div role="tabpanel" id="p1" aria-labelledby="t1" tabindex="0">…</div>
<div role="tabpanel" id="p2" aria-labelledby="t2" tabindex="0" hidden>…</div>
```

Keyboard model (this is the part people skip):

- `Tab` moves into the tablist **once** — the selected tab only. This is roving
  tabindex: the active tab is `tabindex="0"`, all others `tabindex="-1"`.
- `←`/`→` move between tabs. `Home`/`End` jump to first/last.
- `Tab` from the tab moves into the panel, not to the next tab.

If your tabs are really navigation (each changes the URL to a different page),
use a `<nav>` with links and `aria-current="page"` instead — don't fake tabs.

---

## Menu button (application menu)

`role="menu"` is for *application* menus of commands, not for navigation lists
and not for a listbox of options. Using it wrongly makes links stop announcing as
links.

- Navigation dropdown → `<button aria-expanded>` + `<ul>` of `<a>` elements.
- Select-one-value → native `<select>`, or a listbox/combobox pattern.
- Actual command menu (Cut/Copy/Paste, row actions) → `role="menu"` with
  `role="menuitem"`, arrow-key navigation, `Esc` to close, focus back to trigger.

```html
<button type="button" aria-haspopup="true" aria-expanded="false" aria-controls="m">
  Row actions
</button>
<ul id="m" role="menu" hidden>
  <li role="none"><button role="menuitem" type="button">Duplicate</button></li>
  <li role="none"><button role="menuitem" type="button">Delete</button></li>
</ul>
```

`<li>` gets `role="none"` because `role="menu"` only allows `menuitem` children.

---

## Combobox / autocomplete

The highest-difficulty common widget. Prefer a well-tested library
(Downshift, Headless UI, Radix, Ariakit, `<datalist>` for simple cases) over
hand-rolling. If you hand-roll:

```html
<label for="city">City</label>
<input id="city" role="combobox" aria-expanded="false"
       aria-controls="city-list" aria-autocomplete="list" autocomplete="off">
<ul id="city-list" role="listbox" hidden>
  <li role="option" id="opt-1">Lisbon</li>
</ul>
<div aria-live="polite" class="visually-hidden">12 results available</div>
```

- Focus stays in the `<input>` at all times. The "focused" option is indicated by
  `aria-activedescendant="opt-1"` on the input, not by moving DOM focus.
- Selected option: `aria-selected="true"`.
- `↓`/`↑` move the active option, `Enter` selects, `Esc` closes then clears.
- Announce the result count in a polite live region on every filter change,
  debounced — otherwise the list silently changes under the user.
- Never `aria-live="assertive"` here; it interrupts their own typing.

---

## Tooltips and popovers

```html
<button type="button" aria-describedby="tip">Export</button>
<div role="tooltip" id="tip">Downloads a CSV of the current filter</div>
```

- Tooltips appear on **focus and hover**, dismiss on `Esc`, and stay visible
  while the pointer moves into them (SC 1.4.13).
- A tooltip is supplemental. If the content is essential (the only label, an
  error, an instruction), it belongs in the page, not a tooltip.
- Tooltips must not contain interactive content — that's a popover; use the
  `popover` attribute or a disclosure pattern with real focus management.
- Never put a tooltip on a non-focusable element: keyboard users can never see it.

---

## Toasts and status messages

```html
<!-- Present in the DOM from first render, empty. -->
<div id="status" role="status" aria-live="polite" class="visually-hidden"></div>
```

```js
document.getElementById('status').textContent = 'Invoice saved';
```

- Inserting a live region **and** its content in the same tick usually announces
  nothing. Mount the region empty; write text into it later.
- `role="status"` (polite) for success/info. `role="alert"` (assertive) only for
  errors that must interrupt. Assertive toasts on every save is hostile.
- Visible toasts must be dismissible and must not auto-hide before someone using
  a screen magnifier or reader can consume them — WCAG's guidance is that
  auto-hiding content the user must read violates SC 2.2.1. Prefer persistent
  until dismissed, or mirror to a durable location.
- If a toast contains an action ("Undo"), it must be keyboard reachable — which
  in practice means it cannot auto-dismiss on a short timer.

---

## Data tables

```html
<table>
  <caption>Open invoices, sorted by due date</caption>
  <thead>
    <tr>
      <th scope="col" aria-sort="ascending">
        <button type="button">Due date</button>
      </th>
      <th scope="col">Amount</th>
    </tr>
  </thead>
  <tbody>
    <tr><th scope="row">INV-1042</th><td>$1,200.00</td></tr>
  </tbody>
</table>
```

- `<caption>` names the table. `scope` on every header cell.
- Sortable columns: a real `<button>` in the `<th>`, plus `aria-sort` on the
  `<th>` (`ascending` | `descending` | `none`), on **one** column at a time.
- Row selection checkboxes need per-row accessible names ("Select invoice 1042"),
  not five hundred checkboxes named "Select".
- Do not use `role="grid"` unless you are implementing full grid keyboard
  navigation (arrow keys between cells). A plain table is usually correct.
- Responsive tables: don't strip `display: table` at small widths without
  restoring semantics — `display: block` on `<tr>`/`<td>` destroys the table in
  some AT. Prefer horizontal scroll in a labeled, focusable region:
  `<div role="region" aria-label="Invoices" tabindex="0" style="overflow:auto">`.

---

## Pagination and infinite scroll

- Pagination: `<nav aria-label="Pagination">`, current page marked
  `aria-current="page"`, and links say "Page 3" not "3".
- Infinite scroll traps keyboard users before the footer forever. Provide a
  "Load more" button, announce "20 more results loaded, 60 total" politely, and
  keep focus on the button.

---

## Carousels

Mostly a bad idea; if required:

- Pause/stop control that is the first thing in the carousel, and no autoplay by
  default under `prefers-reduced-motion`.
- Slides not currently shown are `hidden` — not merely translated off-screen.
- Previous/Next are `<button>`s with real names, and dots say "Go to slide 3 of 5".
- All content must also be reachable without operating the carousel.

---

## Icons

```html
<!-- Decorative: adjacent to text -->
<svg aria-hidden="true" focusable="false">…</svg>

<!-- Meaningful: the only content -->
<svg role="img" aria-label="Verified account" focusable="false">…</svg>
```

`focusable="false"` matters: legacy IE/Edge made SVG focusable, and some tooling
still trips on it. `aria-hidden` on a focusable element is an accessibility
violation (a focus stop that announces nothing) — always pair them.

---

## Quick anti-pattern index

| Anti-pattern | Why it fails | Fix |
| --- | --- | --- |
| `<div onclick>` | no focus, no role, no keyboard | `<button>` |
| `outline: none` | focus invisible | `:focus-visible` ring |
| `tabindex="5"` | breaks global tab order | DOM order + `tabindex="0"` |
| placeholder as label | vanishes, low contrast | `<label for>` |
| `aria-label` on a `<div>` | no role, name ignored | give it a role or use a real element |
| `role="button"` on a link | lies about behavior | use the right element |
| Red border = error | color-only meaning | text message + `aria-describedby` |
| Live region injected with text | never announced | mount empty, then fill |
| `opacity: 0` menu | still focusable | `hidden` / `display: none` |
| Modal without focus trap | user wanders behind it | `<dialog>.showModal()` |
| `aria-hidden` on a focusable node | silent focus stop | also remove from tab order |
| Auto-dismissing toast with an action | unreachable by keyboard | persist until dismissed |
| Icon button, no name | announces "button" | visually-hidden text |
| Skipped heading levels | broken outline | fix levels, style with CSS |
