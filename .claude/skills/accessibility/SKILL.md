---
name: accessibility
description: Accessibility (a11y) engineering standard — WCAG 2.2 AA, keyboard access, screen readers, focus management, contrast, motion, and an explicit in-app accessibility mode treated as first-class build requirements, never retrofits. Load BEFORE writing or reviewing any interface: web (HTML/CSS/React/Vue/Svelte/Angular), mobile (iOS/Android/RN), desktop, CLI/TUI output, email, PDF, or docs. Triggers on building or editing any component (button, link, modal, dialog, menu, dropdown, combobox, tabs, form, input, table, toast, carousel, tooltip, drag-and-drop), any styling work (color, contrast, focus ring, animation, transition, dark mode, theme, spacing), and on the words accessible, accessibility, a11y, WCAG, ARIA, semantic HTML, screen reader, keyboard navigation, tab order, focus trap, alt text, reduced motion, high contrast, color blind, VoiceOver, NVDA, JAWS, TalkBack, axe, Lighthouse, Section 508, ADA, EAA, EN 301 549, VPAT — or any UI audit, review, or "make this accessible" request.
---

# Accessibility as a build requirement

Everyone has a right to use software. Access is a property of the thing you are
building right now, not a phase that happens later. A feature that a keyboard
user, a screen reader user, a low-vision user, or a user with a tremor cannot
operate is an **unfinished feature**, not an accessible-later feature.

Two operating rules follow from that:

1. **Never write inaccessible code and plan to fix it.** The accessible version
   is the first version. It is almost always the same amount of code.
2. **Never ask permission to be accessible.** Semantic markup, labels, focus
   handling, and contrast are not scope additions to negotiate — they are part
   of "build the button." Only flag it to the user when accessibility forces a
   *visible design or product change* (e.g. a color in their brand palette fails
   contrast, or a drag-only interaction needs a non-drag alternative).

Baseline target: **WCAG 2.2 Level AA**. That is the level referenced by the ADA
Title II rule, Section 508, EN 301 549, and the European Accessibility Act. Go to
AAA on specific criteria where it is cheap (target size, contrast on core text).

---

## The non-negotiable baseline

Apply all of these by default, without being asked, in every UI you touch.

### 1. Semantics before ARIA

Use the element that already means the thing. Native elements bring keyboard
behavior, focus, roles, states, and platform conventions for free.

| Intent | Use | Never |
| --- | --- | --- |
| Navigates somewhere | `<a href>` | `<div onclick>` + `router.push` |
| Performs an action | `<button type="button">` | `<div role="button">` |
| Submits a form | `<button type="submit">` | `<div>` + keydown Enter |
| Toggles a boolean | `<input type="checkbox">` or `<button aria-pressed>` | styled `<div>` |
| Modal | `<dialog>` + `showModal()` | absolutely-positioned `<div>` |
| Expandable section | `<details>/<summary>` or `<button aria-expanded>` | click-only header |
| Grouped choice | `<fieldset><legend>` + radios | unlabeled div group |
| Tabular data | `<table>` + `<th scope>` | grid of divs |

**First rule of ARIA: don't use ARIA.** A native element beats `role=` every
time. Bad ARIA is worse than no ARIA — it overrides real semantics with a lie.
Reach for ARIA only for states native HTML has no expression for
(`aria-expanded`, `aria-current`, `aria-live`, `aria-describedby`) or for a
composite widget HTML genuinely lacks (see `references/patterns.md`).

If you must build a custom control, you owe it **all four**: role, accessible
name, state, and the full keyboard interaction model from APG. If you are not
going to implement all four, use the native element instead.

### 2. Everything works from the keyboard

- Every interactive element is reachable by `Tab` and operable by `Enter`
  (and `Space` for buttons/checkboxes).
- Tab order follows visual order. Use DOM order to achieve it.
  **Never `tabindex` > 0.** Use `tabindex="0"` to add, `tabindex="-1"` for
  programmatic-only focus targets.
- No keyboard traps. Anything that captures focus (modal, menu, editor) must
  release it — `Esc` closes, and focus returns to the element that opened it.
- Any hover-triggered content must also work on focus, and must be dismissible
  (`Esc`) without moving the pointer, and hoverable (you can move into it).
- Any drag interaction needs a single-pointer / keyboard alternative — buttons,
  a "move to…" menu, or cut/paste. (WCAG 2.2 SC 2.5.7.)
- Single-character shortcuts must be disableable or require a modifier, or they
  fire while a speech-input user is dictating.

### 3. Focus is always visible and always intentional

```css
/* Never do this without an immediate replacement. */
:focus { outline: none; }

/* Do this. */
:focus-visible {
  outline: 2px solid var(--focus-ring, Highlight);
  outline-offset: 2px;
  border-radius: inherit;
}
```

- The focus indicator needs **3:1 contrast** against the adjacent background
  (SC 1.4.11), and must not be fully hidden by sticky headers/footers
  (SC 2.4.11) — add `scroll-margin-block` to focusable elements under a sticky bar.
- Manage focus on every state change that moves the user:
  - Open a dialog → focus the dialog (its heading or first control).
  - Close it → focus returns to the trigger.
  - Delete a row → focus the next row, or the container, never `<body>`.
  - Client-side route change → focus the new `<h1>` (`tabindex="-1"`) or a
    dedicated route announcer. SPAs silently reset focus to `<body>` otherwise,
    which strands screen reader users at the top of the document.
- Never steal focus without user intent (no autofocus stealing mid-typing, no
  focus grabs on background updates).

### 4. Everything interactive has an accessible name

- Prefer **visible text**. It serves everyone and works with voice control.
- Icon-only controls: prefer visually-hidden real text over `aria-label` — it is
  translatable by page translation tools and survives more contexts.

```html
<button type="button">
  <svg aria-hidden="true" focusable="false">…</svg>
  <span class="visually-hidden">Delete invoice 1042</span>
</button>
```

- If a control has visible text, its accessible name **must contain that text**
  (SC 2.5.3) — otherwise voice users saying "click Save" hit nothing.
- Names must be unique and self-describing in a list: five links all named
  "Read more" are five identical rows in a screen reader's link list.
- `aria-label` on a `<div>` or `<span>` with no role does nothing. Names only
  attach to elements with a role that supports naming.

### 5. Structure is real, not visual

- Exactly one `<h1>` per page/view; never skip heading levels. Headings are the
  primary navigation mechanism for screen reader users — they are an outline,
  not a font size. Style with CSS if you need a smaller-looking `<h2>`.
- Use landmarks: `<header>`, `<nav>`, `<main>` (exactly one), `<aside>`,
  `<footer>`. Label repeated ones: `<nav aria-label="Pagination">`.
- Provide a skip link as the first focusable element:
  `<a href="#main" class="skip-link">Skip to main content</a>` — visible on focus.
- Lists are `<ul>/<ol>`; a list of items rendered as sibling `<div>`s loses its
  count ("list, 8 items").
- `<html lang="en">` always; mark inline language changes with `lang` (SC 3.1.2).
- Page `<title>` is unique and describes the view, most-specific-first.

### 6. Forms are labeled, forgiving, and explain their failures

- Every input has a `<label for>` (or is wrapped by one).
  **Placeholder is not a label** — it disappears on input and usually fails contrast.
- Hints and errors connect via `aria-describedby`; invalid fields get
  `aria-invalid="true"`.
- On submit failure: render an **error summary** at the top of the form, move
  focus to it, and link each message to its field. Do not rely on color or an
  icon alone to signal error.
- Mark required fields in text, not only with a red asterisk.
- Add `autocomplete` tokens (`email`, `given-name`, `street-address`, `one-time-code`)
  — required by SC 1.3.5 and a real quality-of-life win.
- Don't block paste, don't impose arbitrary format rules the user must guess, and
  don't require the user to re-enter information they already gave you (SC 3.3.7).
- Authentication must not depend on a cognitive test with no alternative — allow
  paste of passwords/OTPs, support password managers, offer an alternative to
  puzzle CAPTCHAs (SC 3.3.8).
- Inline validation fires on blur or submit, not on every keystroke.

### 7. Color and contrast carry no unique meaning

| What | Minimum ratio |
| --- | --- |
| Body text | **4.5 : 1** |
| Large text (≥ 24px, or ≥ 18.66px bold) | **3 : 1** |
| UI component boundaries, icons, focus rings, chart marks | **3 : 1** |
| Disabled controls | exempt — but still make them legible |

- Never encode information in color alone: add text, icon, pattern, or position.
  Status pills need a word, chart series need direct labels or shapes, required
  fields need text, diffs need `+`/`-`.
- Test against actual rendered backgrounds, including gradients, images,
  and overlay states (hover/active/selected).
- Verify in both themes. Dark mode regularly breaks contrast for muted/secondary
  text; check `--text-muted` and placeholder colors specifically.
- APCA is a WCAG 3 draft — useful as a sanity check, **not** a compliance basis.

### 8. Respect user preferences at the OS level, by default

These are free wins and the *foundation* of a11y mode (see below).

```css
@media (prefers-reduced-motion: reduce) { /* no parallax, no large translations */ }
@media (prefers-contrast: more)         { /* stronger borders, darker text */ }
@media (prefers-reduced-transparency)   { /* solid backgrounds, no blur */ }
@media (prefers-color-scheme: dark)     { /* real dark theme */ }
@media (forced-colors: active)          { /* Windows High Contrast */ }
```

- Reduced motion means *reduce*, not *remove all feedback*: replace slides and
  scale-ups with a short opacity fade, kill parallax/autoplaying video/infinite
  loops. Never remove the state change itself.
- Nothing flashes more than 3 times per second, ever (SC 2.3.1 — seizure risk).
- No autoplaying audio/video with sound. Anything auto-updating or moving for
  more than 5s needs pause/stop/hide.
- In `forced-colors` mode, use system color keywords (`Canvas`, `CanvasText`,
  `Highlight`, `ButtonText`) and don't `forced-color-adjust: none` to preserve
  branding — that's the one place users explicitly overrode you.

### 9. Zoom, reflow, and text spacing don't break the layout

- Content must reflow to **320 CSS px** wide with no two-dimensional scrolling
  (SC 1.4.10) — that's 400% zoom on a 1280px viewport, and it's the same work as
  supporting small phones.
- Text must scale to **200%** without clipping or overlap (SC 1.4.4). Use `rem`
  for type and spacing; never fix a container's height to its text.
- Survive user-injected text spacing (line-height 1.5×, paragraph 2×,
  letter-spacing 0.12em, word-spacing 0.16em) without losing content (SC 1.4.12).
- Never `user-scalable=no` or `maximum-scale=1` in the viewport meta tag.
- Support both orientations unless orientation is essential (SC 1.3.4).

### 10. Targets are big enough to hit

- Minimum **24 × 24 CSS px** for pointer targets (SC 2.5.8 AA); prefer
  **44 × 44** (AAA / iOS HIG; Android is 48 × 48 dp).
- If the visual element must stay small, expand the hit area with padding or a
  pseudo-element — don't shrink the target.
- Don't crowd destructive actions next to routine ones.

### 11. Non-text content has a text equivalent

- Every `<img>` has `alt`. Decorative → `alt=""` (empty, not missing).
  Informative → describe the *information*, not the picture. Functional (inside a
  link/button) → describe the *destination or action*.
- Inline SVG: decorative → `aria-hidden="true" focusable="false"`; meaningful →
  `role="img"` + `aria-label` (or a `<title>` referenced by `aria-labelledby`).
- Charts, diagrams, and data images need a real long description — a caption, a
  data table, or a `<figcaption>` — not a 200-character alt.
- Video: captions for all pre-recorded audio (AA), audio description for
  meaningful visual-only content, transcripts for audio-only. Auto-captions are a
  draft, not a deliverable.
- Never put information only in an image of text.

### 12. Dynamic changes are announced

- Status messages that appear without focus change need a live region
  (SC 4.1.3). The region must **already exist in the DOM** and be empty — screen
  readers do not announce a live region that is inserted with its content.
- `aria-live="polite"` for status/toasts/results counts; `assertive`/`role="alert"`
  only for genuine errors that interrupt.
- Announce async state: "Loading results", "12 results", "Saved". Silent spinners
  are invisible to screen reader users.
- Toasts must be reachable, dismissible, and persist long enough to read — or
  better, also land somewhere permanent.

---

## a11y mode: an explicit, first-class accessibility surface

The OS preference queries above are the **default layer**. An in-app
accessibility mode is the **override layer**, for users whose needs their OS
doesn't express or who want different settings in your app than system-wide.

Build it as product surface, not a hidden flag. Full spec, state model, and
implementation in **`references/a11y-mode.md`**. The short version:

- Three-state per setting: `system` (default) → `on` / `off`. Never a bare
  boolean that ignores the OS.
- One source of truth on `<html>` (`data-motion="reduce" data-contrast="high"`),
  applied by a render-blocking inline script so there is no flash of the wrong mode.
- Persist to the **account**, not just the device, so it follows the user.
- Discoverable: in the main settings nav, and linked from the footer — not buried.
- **a11y mode is never a degraded mode.** Same features, same data, same
  functionality. If a setting removes capability, it's a bug.

**Never install a third-party accessibility overlay/widget** (accessiBe,
UserWay, AudioEye, and similar). They do not fix underlying defects, they
frequently break assistive technology that users already have configured, they
are opposed by essentially the entire disability community and by screen reader
users specifically, and they have been named in a large and growing number of
ADA lawsuits. If someone proposes one, say plainly that it increases legal
exposure rather than reducing it, and fix the actual markup.

---

## Definition of done

A UI change is not done until all of these pass. Run them before you report
completion, not after review.

- [ ] **Unplug the mouse.** Tab through the whole flow. Everything reachable,
      everything operable, focus always visible, order matches the visuals,
      `Esc` escapes everything, focus returns where it should.
- [ ] **Automated scan is clean** — `axe` (or `jest-axe`/`@axe-core/playwright`)
      on the changed views. Zero violations. Note: automated tools catch roughly
      a third of real issues; passing is the floor, not the goal.
- [ ] **Zoom to 400%** (or resize to 320px). No horizontal scrolling, no clipping,
      no overlap.
- [ ] **Contrast checked** on the actual rendered colors, in light and dark, in
      every interactive state.
- [ ] **Reduced motion honored** — toggle the OS setting and confirm.
- [ ] **Accessible names present and sensible** — read the accessibility tree
      (DevTools → Accessibility pane). Any control named "button", "link", or ""
      is a defect.
- [ ] **Screen reader smoke test** on anything new or interactive — VoiceOver
      (macOS/iOS), NVDA (Windows), or TalkBack (Android). Does the announcement
      actually tell you what the thing is and what state it's in?
- [ ] **Forms**: labels, errors announced, error summary focused, autocomplete set.

If you cannot run one of these in the environment, say so explicitly in your
report rather than silently skipping it.

---

## When auditing existing code

Triage in this order — this is roughly descending order of how badly each one
blocks someone:

1. **Blocks completion of a task** — keyboard traps, unreachable controls,
   unlabeled form fields in a checkout/signup, missing error announcements.
2. **Blocks understanding** — missing/incorrect names and roles, no headings, no
   alt on informative images, color-only meaning.
3. **Blocks comfort** — contrast just under threshold, small targets, motion,
   focus indicator weak, reflow issues.
4. **Polish** — redundant ARIA, verbose announcements, non-ideal alt phrasing.

Report findings as: what breaks, **who it breaks for**, the WCAG SC, and the
concrete fix. Fix the causes in shared components rather than patching each
call site — one bad `<Button>` primitive is one fix, not four hundred.

---

## Reference files

Read the relevant one when you need depth; don't load all of them.

- **`references/patterns.md`** — correct implementations for dialog, menu, tabs,
  combobox, disclosure, tooltip, toast, data table, pagination, and the
  anti-patterns each replaces.
- **`references/a11y-mode.md`** — full design and implementation of an in-app
  accessibility mode: settings inventory, state model, no-flash bootstrap,
  persistence, and the settings UI itself.
- **`references/testing.md`** — automated tooling, CI wiring, linting, and
  step-by-step screen reader test scripts for people who have never used one.
- **`references/platforms.md`** — beyond the browser: React/Vue/Svelte specifics,
  React Native, iOS (UIKit/SwiftUI), Android, Electron/desktop, CLI and TUI
  output, email, PDFs, and documentation.
