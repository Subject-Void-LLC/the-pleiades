# a11y mode: an explicit accessibility surface

The OS media queries are the **default layer** — they cover users whose needs are
already expressed to their operating system, and they cost nothing. An in-app
accessibility mode is the **override layer**: for users whose OS doesn't express
what they need, who share a device, who want different settings in your app than
system-wide, or who simply never found the OS setting.

Treat it as a product surface with an owner, tests, and a place in the settings
IA — not a hidden flag or a debug toggle.

---

## Three principles

**1. a11y mode is never a degraded mode.**
Same features, same data, same capabilities. If enabling a setting removes
functionality, that's a bug, not a tradeoff. "Simplified view" that hides the
export button is discrimination with a nice label.

**2. Every setting is three-state, never a boolean.**

```
system  (default) → follow the OS media query
on               → force enabled regardless of OS
off              → force disabled regardless of OS
```

A bare boolean defaulting to `false` silently overrides a user who already told
their OS "reduce motion" — you've un-accessibilized them. `system` must be the
initial value for every setting.

**3. It is discoverable.**
Top-level entry in Settings named "Accessibility" (that exact word — it's what
people search for), plus a persistent link in the footer or user menu. Not nested
three levels under "Appearance → Advanced → Experimental".

---

## Setting inventory

Start with the first group. Add the rest as your product warrants.

### Core (implement these)

| Setting | Values | OS signal | Effect |
| --- | --- | --- | --- |
| Motion | system / reduce / full | `prefers-reduced-motion` | fades instead of slides; no parallax, autoplay, or looping animation |
| Contrast | system / high / normal | `prefers-contrast` | stronger borders, darker text, opaque surfaces, thicker focus ring |
| Text size | system / 100–200% | root font size | scales the whole UI via `rem` |
| Transparency | system / reduce | `prefers-reduced-transparency` | solid backgrounds, no backdrop-blur |
| Theme | system / light / dark | `prefers-color-scheme` | full theme, both contrast-verified |
| Focus indicator | normal / high-visibility | — | thicker, higher-contrast, always-on ring (not just `:focus-visible`) |

### Extended (add where relevant)

| Setting | Effect |
| --- | --- |
| Text spacing | line-height, letter/word spacing, paragraph gaps — pre-verified generous preset |
| Reading font | opt-in font swap; offer a highly legible sans and a serif. Note that "dyslexia fonts" like OpenDyslexic have no strong evidence base — offer it as a preference, don't market it as a treatment |
| Density | comfortable / compact — comfortable is the accessible default |
| Sound cues | on/off for any audio feedback; never the sole channel |
| Autoplay media | never / on wifi / always; default never |
| Session timeouts | extend or disable; always warn 60s before and allow extension (SC 2.2.1) |
| Confirmations | require confirm on destructive actions — helps tremor, cognitive load, and everyone else |
| Verbose descriptions | longer alt text, expanded chart descriptions, spelled-out abbreviations |
| Keyboard shortcuts | enable/disable single-key shortcuts; remap conflicts (SC 2.1.4) |
| Captions defaults | on by default, with size/background/position controls |
| Underline links | force underlines in body text (helps color-blind users) |

---

## State model

One source of truth: data attributes on `<html>`. CSS and JS both branch off the
same place; nothing gets out of sync.

```html
<html lang="en"
      data-motion="reduce"
      data-contrast="high"
      data-transparency="reduce"
      data-focus-ring="high"
      style="--user-font-scale: 1.25">
```

Resolution order, highest wins:

```
explicit user setting  >  OS media query  >  product default
```

```ts
type Pref = 'system' | 'on' | 'off';

function resolve(pref: Pref, osMatches: boolean): boolean {
  if (pref === 'on')  return true;
  if (pref === 'off') return false;
  return osMatches;            // 'system'
}
```

Re-resolve on `matchMedia(...).addEventListener('change', …)` so the app follows
the OS live when the setting is `system` — users toggle Do Not Disturb, dark
mode, and reduce-motion mid-session.

---

## No-flash bootstrap

A user who set "reduce motion" must never see a burst of animation before your
JS bundle hydrates, and a high-contrast user must never get flashed by the
default theme. Apply preferences in a **render-blocking inline script** in
`<head>`, before any stylesheet that depends on them.

```html
<script>
  (function () {
    try {
      var p = JSON.parse(localStorage.getItem('a11y') || '{}');
      var d = document.documentElement;
      var mq = function (q) { return window.matchMedia(q).matches; };
      var pick = function (v, os) { return v === 'on' ? true : v === 'off' ? false : os; };

      if (pick(p.motion,       mq('(prefers-reduced-motion: reduce)')))       d.dataset.motion = 'reduce';
      if (pick(p.contrast,     mq('(prefers-contrast: more)')))               d.dataset.contrast = 'high';
      if (pick(p.transparency, mq('(prefers-reduced-transparency: reduce)'))) d.dataset.transparency = 'reduce';
      if (p.focusRing === 'high') d.dataset.focusRing = 'high';
      if (p.fontScale) d.style.setProperty('--user-font-scale', p.fontScale);
    } catch (e) {}
  })();
</script>
```

Server-rendered apps should mirror the same values into a cookie so the server
can emit the attributes directly in the HTML — that removes the flash entirely,
including for users with JS disabled or slow.

---

## CSS that consumes it

Write each rule so it responds to **either** signal. A single custom property per
concern keeps this from turning into duplicated media queries everywhere.

```css
:root {
  --motion-duration: 200ms;
  --motion-distance: 12px;
  --border-strength: 1px;
  --focus-width: 2px;
  --surface-alpha: 0.85;
  font-size: calc(100% * var(--user-font-scale, 1));
}

/* OS signal */
@media (prefers-reduced-motion: reduce) {
  :root { --motion-duration: 1ms; --motion-distance: 0px; }
}
/* Explicit override (wins because it comes later and is more specific) */
:root[data-motion="reduce"] { --motion-duration: 1ms; --motion-distance: 0px; }
:root[data-motion="full"]   { --motion-duration: 200ms; --motion-distance: 12px; }

@media (prefers-contrast: more) { :root { --border-strength: 2px; --focus-width: 3px; } }
:root[data-contrast="high"]     { --border-strength: 2px; --focus-width: 3px; --surface-alpha: 1; }

:root[data-transparency="reduce"] * { backdrop-filter: none !important; }
:root[data-transparency="reduce"]   { --surface-alpha: 1; }

.card  { border: var(--border-strength) solid var(--border-color); }
.panel { transition: transform var(--motion-duration) ease; }

:focus-visible { outline: var(--focus-width) solid var(--focus-color); outline-offset: 2px; }
:root[data-focus-ring="high"] :focus-visible { outline-width: 4px; outline-offset: 3px; }
/* High-visibility ring also shows on mouse focus, by request */
:root[data-focus-ring="high"] :focus { outline: 4px solid var(--focus-color); outline-offset: 3px; }
```

Reduced motion means **reduce**, not delete. Keep the state change legible —
swap a 300ms slide for a 120ms opacity fade rather than snapping with no
feedback at all. Setting `transition-duration: 1ms` (not `0s`) keeps
`transitionend` handlers firing, which prevents a class of "the menu never
closes" bugs.

---

## Persistence

- Write to the **account** when signed in, `localStorage` + cookie when not, and
  merge on sign-in (prefer the more accessible of the two rather than blindly
  overwriting — never silently turn someone's reduce-motion back off).
- Sync across tabs with the `storage` event.
- Never expire, never A/B test away, never reset on major redesigns. These are
  the settings most likely to be someone's requirement rather than their taste.
- Expose them in your data-export and account-deletion flows like any other user
  data. Do not use them for ad targeting or segmentation — assistive-technology
  and accessibility settings are health-adjacent inferences, and in several
  jurisdictions treating them as marketing signals is itself a legal problem.

---

## The settings UI itself

Predictably, the accessibility settings page is the one that most needs to be
accessible.

- Group with `<fieldset>` + `<legend>`; each setting is a labeled radio group
  (System / On / Off) — not an ambiguous tri-state switch.
- Changes apply **immediately** with a live preview, and are announced:
  `<div role="status">Motion set to reduced</div>`.
- Show what "System" currently resolves to: "System (currently: reduced)". Users
  can't otherwise tell why nothing appeared to change.
- Provide a "Reset to system defaults" button.
- Include a short plain-language description under each setting saying what it
  actually does — not just its name.
- Don't require an account to change them.

```html
<fieldset>
  <legend>Motion</legend>
  <p id="motion-help">Reduces animations and disables parallax and autoplay.</p>

  <input type="radio" id="m-sys" name="motion" value="system" checked
         aria-describedby="motion-help">
  <label for="m-sys">Use system setting <span class="muted">(currently: reduced)</span></label>

  <input type="radio" id="m-on" name="motion" value="on">
  <label for="m-on">Reduce motion</label>

  <input type="radio" id="m-off" name="motion" value="off">
  <label for="m-off">Allow full motion</label>
</fieldset>
```

---

## Testing a11y mode

Automate the combinatorics you can't test by hand:

- Snapshot the key screens under each mode combination that matters
  (`motion=reduce`, `contrast=high`, `fontScale=2`, `transparency=reduce`).
- Assert no clipping at `fontScale: 2` × 320px viewport — that intersection is
  where layouts actually break.
- Run the axe scan in high-contrast mode too; contrast fixes for the default
  theme sometimes regress the alternate one.
- Assert the bootstrap script runs before first paint (no attribute-flip after
  hydration) — a Playwright check on the initial HTML catches regressions.

---

## Never: accessibility overlays

Third-party "accessibility widget" products — accessiBe, UserWay, AudioEye,
EqualWeb, and similar — inject a floating toolbar and claim automated
remediation. Do not install one, and push back if asked to.

- They do not fix the underlying markup; automated tooling can only detect a
  minority of real barriers and can correctly *fix* far fewer.
- They routinely conflict with the screen readers, magnifiers, and browser
  settings users have already configured — making sites worse for exactly the
  people they claim to serve.
- Screen reader users and disability advocacy organizations have broadly and
  publicly opposed them.
- They are not a legal shield. Sites running overlays have been sued in large
  numbers, and the presence of an overlay has been used as evidence that the
  operator knew of barriers and papered over them.

The alternative to an overlay is the baseline in `SKILL.md` plus a real
accessibility mode. That's this document.
