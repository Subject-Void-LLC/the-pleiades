# Testing accessibility

Automated tools reliably catch roughly **30–40%** of real accessibility barriers
— missing names, bad contrast, invalid ARIA, duplicate ids. They cannot tell you
whether the focus order makes sense, whether alt text is *correct*, or whether an
announcement is comprehensible. A clean axe run is the floor, not the goal.

Layer the four: lint → unit → e2e → human.

---

## 1. Lint (catches it before it's written)

**React** — `eslint-plugin-jsx-a11y`:

```bash
npm i -D eslint-plugin-jsx-a11y
```

```js
// eslint.config.js
import jsxA11y from 'eslint-plugin-jsx-a11y';

export default [
  jsxA11y.flatConfigs.strict,
  {
    rules: {
      // Map your design-system wrappers so rules actually apply to them.
      'jsx-a11y/no-static-element-interactions': 'error',
      'jsx-a11y/anchor-is-valid': 'error',
    },
    settings: {
      'jsx-a11y': {
        components: { Button: 'button', Link: 'a', Input: 'input', Img: 'img' },
      },
    },
  },
];
```

That `components` mapping is the step most teams miss — without it the plugin
sees `<Button>` as an unknown component and checks nothing.

**Vue** — `eslint-plugin-vuejs-accessibility`.
**Svelte** — the compiler emits a11y warnings by default; do not silence them.
**Angular** — `@angular-eslint/template` accessibility rules.

---

## 2. Unit / component tests

```bash
npm i -D jest-axe @testing-library/react @testing-library/user-event
```

```js
import { render } from '@testing-library/react';
import { axe, toHaveNoViolations } from 'jest-axe';
expect.extend(toHaveNoViolations);

test('invoice card has no a11y violations', async () => {
  const { container } = render(<InvoiceCard invoice={fixture} />);
  expect(await axe(container)).toHaveNoViolations();
});
```

Add this to the shared test setup for every component in your design system —
primitives are where a single fix protects every consumer.

**Query by role, not by test id.** This is the highest-leverage habit in the
whole document: if `getByRole('button', { name: 'Save invoice' })` fails, the
control genuinely has no accessible name, and the test caught a real defect.

```js
// ✓ tests the accessibility tree as a side effect
await user.click(screen.getByRole('button', { name: 'Save invoice' }));
expect(screen.getByRole('alert')).toHaveTextContent('Amount is required');

// ✗ passes even when the control is an unlabeled div
await user.click(screen.getByTestId('save-btn'));
```

Test keyboard paths explicitly with `user.tab()` and `user.keyboard('{Escape}')`
for anything with focus management (dialogs, menus, comboboxes).

---

## 3. End-to-end scans

```bash
npm i -D @axe-core/playwright
```

```ts
import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const PAGES = ['/', '/signup', '/dashboard', '/settings/accessibility'];

for (const path of PAGES) {
  test(`a11y: ${path}`, async ({ page }) => {
    await page.goto(path);
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'])
      .analyze();
    expect(results.violations).toEqual([]);
  });
}
```

Scan **states**, not just routes: modal open, form in its error state, menu
expanded, table sorted, empty state, loading state. Most violations live in
states a page-load scan never reaches.

Cheap high-value e2e assertions beyond the scanner:

```ts
// Focus is never lost to <body> after closing a dialog
await page.getByRole('button', { name: 'Edit profile' }).click();
await page.keyboard.press('Escape');
await expect(page.getByRole('button', { name: 'Edit profile' })).toBeFocused();

// No horizontal scroll at 320px
await page.setViewportSize({ width: 320, height: 800 });
expect(await page.evaluate(() =>
  document.documentElement.scrollWidth <= document.documentElement.clientWidth
)).toBe(true);

// Reduced motion honored
test.use({ colorScheme: 'dark', reducedMotion: 'reduce' });
```

Playwright also exposes `page.accessibility.snapshot()` — snapshot-testing the
a11y tree of key components catches silent regressions in names and roles.

---

## 4. CI wiring

Make it blocking on changed views. A warning that nobody reads is not a test.

```yaml
- name: Accessibility
  run: |
    npm run lint
    npm run test -- --testPathPattern=a11y
    npx playwright test tests/a11y
```

Add Lighthouse CI for a per-PR score if you want a trend line, but treat axe
violations — not the Lighthouse score — as the gate. The score is a weighted
heuristic; violations are facts.

Track a `known-issues.json` allowlist if you're retrofitting a large codebase:
freeze the current violations, block *new* ones, and burn the list down. Never
disable the rule.

---

## 5. Manual checks (5 minutes, catches what tools can't)

Do these on every UI change, in this order:

1. **Unplug the mouse.** Tab the full flow. Watch for: invisible focus, focus
   jumping backward, tab stops on nothing, `Esc` not working, focus lost after
   closing something.
2. **Zoom to 400%** (Ctrl/Cmd `+`). Or set the viewport to 320px. Look for
   horizontal scroll, clipped text, overlapping elements, unreachable buttons.
3. **Open DevTools → Accessibility pane** and read the tree for your component.
   Every interactive node should have a sensible name and correct role. Anything
   named "button", "link", "image", or blank is a defect.
4. **Toggle reduce-motion at the OS level** and re-run the interaction.
5. **Grayscale the page** (DevTools → Rendering → Emulate vision deficiencies →
   Achromatopsia). Any information you can no longer perceive was color-only.

---

## 6. Screen reader testing (if you've never done it)

You do not need to be proficient. You need to answer one question: *does the
announcement tell me what this is, what state it's in, and what I can do?*

Market share skews toward NVDA and JAWS on Windows and VoiceOver on iOS, so test
at least one desktop reader and one mobile.

### VoiceOver — macOS (built in)

| Action | Keys |
| --- | --- |
| Toggle VoiceOver | `Cmd + F5` |
| Next / previous item | `Ctrl+Opt + →` / `←` |
| Activate | `Ctrl+Opt + Space` |
| Rotor (headings, links, form controls) | `Ctrl+Opt + U`, then `←`/`→` |
| Read from here | `Ctrl+Opt + A` |
| Stop talking | `Ctrl` |

Use Safari — VoiceOver is best supported there.

### VoiceOver — iOS (built in)

Settings → Accessibility → VoiceOver (bind it to triple-click the side button).
Swipe right/left to move, double-tap to activate, two-finger swipe up to read all,
rotor = two-finger rotate.

### NVDA — Windows (free, [nvaccess.org](https://www.nvaccess.org))

| Action | Keys |
| --- | --- |
| Start | `Ctrl+Alt+N` |
| Stop speech | `Ctrl` |
| Next heading / landmark / form field | `H` / `D` / `F` |
| Elements list (links, headings) | `NVDA+F7` |
| Toggle browse/focus mode | `NVDA+Space` |

NVDA key is `Insert` (or `CapsLock` if configured). Use with Firefox or Chrome.

### TalkBack — Android

Settings → Accessibility → TalkBack. Swipe right/left to move, double-tap to
activate, swipe up-then-right for the menu.

### What to actually check

- **Read the page top to bottom.** Does the order match what you see?
- **Jump by heading** (`H` / rotor). Does the outline describe the page?
- **List the links/buttons.** Are the names unique and meaningful out of context?
- **Fill in the form wrong on purpose.** Is the error announced? Can you find the
  field it belongs to?
- **Open and close a dialog.** Is the dialog announced by name? Can you escape it?
  Where does focus land?
- **Trigger an async update.** Is anything announced, or is it silent?

Turn the screen off (or `Ctrl+Opt+Shift+F11` for VoiceOver's screen curtain) for
one pass. That's the actual experience.

---

## 7. Manual audit checklist

Reusable pass for a full review:

- [ ] Keyboard: reach, operate, escape everything; visible focus throughout
- [ ] Tab order matches visual order; no positive tabindex
- [ ] One `<h1>`; no skipped levels; headings describe sections
- [ ] Landmarks present; `<main>` exists; skip link works
- [ ] All controls have unique, meaningful accessible names
- [ ] Visible label text is contained in the accessible name (voice control)
- [ ] Form fields labeled; errors announced and linked; autocomplete set
- [ ] Images: informative described, decorative `alt=""`, complex have long descriptions
- [ ] No color-only meaning (check in grayscale)
- [ ] Contrast: 4.5:1 text, 3:1 large text and UI, in both themes, all states
- [ ] Focus indicator ≥ 3:1 against adjacent colors, never obscured
- [ ] 320px reflow / 400% zoom clean; 200% text scale clean
- [ ] Targets ≥ 24×24 CSS px (44×44 preferred)
- [ ] Motion respects `prefers-reduced-motion`; nothing flashes > 3×/sec
- [ ] Dynamic updates announced via a pre-existing live region
- [ ] Dialogs: named, focus trapped, `Esc` closes, focus restored
- [ ] `lang` on `<html>`; unique descriptive `<title>` per view
- [ ] Media: captions, transcripts, audio description; no autoplay with sound
- [ ] Timeouts warn and can be extended; no unrecoverable data loss
- [ ] Drag interactions have a click/keyboard alternative
- [ ] Works with browser zoom, custom stylesheets, and Windows High Contrast

---

## Tools worth having installed

| Tool | Use |
| --- | --- |
| axe DevTools (browser extension) | fastest per-page scan, best signal-to-noise |
| ARC Toolkit | detailed manual-inspection aid |
| WAVE | quick visual overlay, good for demoing issues to non-engineers |
| Accessibility Insights (Microsoft) | guided manual assessment, tab-order visualizer |
| Chrome DevTools → Accessibility pane | read the real a11y tree |
| Chrome DevTools → Rendering | emulate vision deficiencies, forced-colors, reduced motion |
| Polypane / Responsively | multi-viewport + a11y overlays at once |
| Colour Contrast Analyser (TPGi) | eyedropper contrast on anything, including images |
| `pa11y-ci` | CLI crawl of a sitemap for broad regression coverage |
| VS Code "axe Accessibility Linter" | inline feedback while writing markup |

---

## Reporting findings

For each issue give: **what breaks → who it breaks for → WCAG SC → the fix**.

> **Delete button has no accessible name** — `InvoiceRow.tsx:42`
> Screen reader users hear only "button" and cannot tell which row it deletes, or
> that it's destructive. Voice-control users have nothing to say.
> WCAG 2.2 SC 4.1.2 (Name, Role, Value), Level A.
> Fix: add `<span class="visually-hidden">Delete invoice {invoice.number}</span>`
> inside the button next to the `aria-hidden` icon.

Fix in shared primitives, not at call sites. One broken `<Button>` is one fix.
