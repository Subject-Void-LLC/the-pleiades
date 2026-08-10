# Platform notes

The principles in `SKILL.md` are universal. The vocabulary is not. This maps them
onto the places you'll actually be writing code.

---

## React

- **`key` is not an id.** Use real, stable `id`s for `htmlFor`/`aria-describedby`.
  `useId()` generates SSR-safe unique ids — use it in every component that pairs a
  label with a field.

```jsx
function Field({ label, hint, error, ...props }) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errId = `${id}-err`;
  return (
    <div>
      <label htmlFor={id}>{label}</label>
      {hint && <p id={hintId}>{hint}</p>}
      <input
        id={id}
        aria-describedby={[hint && hintId, error && errId].filter(Boolean).join(' ') || undefined}
        aria-invalid={error ? true : undefined}
        {...props}
      />
      {error && <p id={errId}>{error}</p>}
    </div>
  );
}
```

- **Route changes**: React Router / Next.js do not move focus. Add a route
  announcer that, on navigation, sets focus to the new `<h1 tabindex="-1">` and
  writes the page title into a polite live region.
- **`dangerouslySetInnerHTML`** bypasses every check. Sanitize *and* audit
  markup from a CMS — that's where the unlabeled images live.
- **Portals** render outside the DOM hierarchy but stay in the React tree. Focus
  order follows the *DOM*, so a portaled dropdown appended to `<body>` lands at
  the end of the tab order. Use `<dialog>` / the `popover` attribute (both use the
  top layer and get this right), or manage focus explicitly.
- Prefer accessible headless primitives over hand-rolled widgets: **Radix UI**,
  **React Aria / React Spectrum** (Adobe), **Ariakit**, **Headless UI**. They
  implement the APG keyboard models you would otherwise get 80% right.
- Don't spread `{...props}` in a way that lets callers clobber `role`/`aria-*`
  on primitives; merge instead.

## Vue

- `v-show` toggles `display: none` (correctly hidden from AT); `v-if` unmounts.
  Both are fine. Custom "hide" classes using opacity are not.
- `<Transition>`: gate durations on `prefers-reduced-motion` via CSS custom
  properties rather than JS hooks, so the OS setting applies without a re-render.
- `eslint-plugin-vuejs-accessibility`; **Reka UI** (formerly Radix Vue) for
  headless primitives.

## Svelte / SvelteKit

- The compiler emits a11y warnings at build time — treat them as errors, never
  blanket-disable with `<!-- svelte-ignore -->`.
- SvelteKit announces route changes automatically via a built-in live region, but
  does **not** move focus; add focus management yourself.

## Angular

- Use the **Angular CDK a11y** package: `FocusTrap`, `LiveAnnouncer`,
  `cdkMonitorFocus`, `A11yModule`. `LiveAnnouncer.announce()` is the correct way
  to emit status messages.
- Enable `@angular-eslint/template` accessibility rules.

---

## React Native

Props map to the platform APIs, not to ARIA:

```jsx
<Pressable
  accessible
  accessibilityRole="button"
  accessibilityLabel="Delete invoice 1042"
  accessibilityHint="Removes it permanently"
  accessibilityState={{ disabled: false, busy: saving }}
  style={{ minWidth: 44, minHeight: 44 }}
>
  <Icon name="trash" importantForAccessibility="no" />
</Pressable>
```

- `accessible` on a container groups children into one focus stop — use it for
  cards, not for whole screens.
- `AccessibilityInfo.announceForAccessibility()` for status messages.
- `AccessibilityInfo.isReduceMotionEnabled()` / the `reduceMotionChanged` event
  drive your reduced-motion state.
- Respect `Text` scaling — don't set `allowFontScaling={false}`, and don't fix
  container heights around text.

## iOS (UIKit / SwiftUI)

```swift
Button(action: delete) { Image(systemName: "trash") }
  .accessibilityLabel("Delete invoice 1042")
  .accessibilityHint("Removes it permanently")
  .frame(minWidth: 44, minHeight: 44)
```

- **Dynamic Type** is the big one: use text styles (`.body`, `.headline`), not
  fixed point sizes, and test at the largest accessibility sizes. Layouts must
  reflow, not clip.
- `.accessibilityElement(children: .combine)` to group; `.accessibilityHidden(true)`
  for decorative imagery.
- `UIAccessibility.post(notification: .announcement, argument:)` for status;
  `.screenChanged` / `.layoutChanged` when the UI restructures.
- Honor `UIAccessibility.isReduceMotionEnabled`,
  `isReduceTransparencyEnabled`, `isDarkerSystemColorsEnabled`,
  `isVoiceOverRunning`.
- Test with VoiceOver and with the Accessibility Inspector (Xcode → Open
  Developer Tool → Accessibility Inspector → Audit).

## Android

```xml
<ImageButton
    android:contentDescription="@string/delete_invoice"
    android:minWidth="48dp"
    android:minHeight="48dp" />
```

- `contentDescription` for meaningful images; `android:importantForAccessibility="no"`
  for decorative ones.
- Compose: `Modifier.semantics { contentDescription = "…" ; role = Role.Button }`,
  `Modifier.clearAndSetSemantics {}` to group.
- Touch targets ≥ **48 × 48 dp**.
- Respect `Settings.Global.TRANSITION_ANIMATION_SCALE == 0` (reduce motion) and
  system font scale.
- Test with TalkBack and Accessibility Scanner (Play Store).

## Electron / desktop

- It's a browser: everything in `SKILL.md` applies.
- Native menus get accelerators and are accessible for free — prefer them to
  in-window custom menus.
- Respect OS high contrast: `nativeTheme.shouldUseHighContrastColors` and the
  `forced-colors` media query.
- Custom title bars break window management for screen reader and magnifier
  users; if you build one, keep real min/max/close controls with names.

---

## CLI and TUI output

Terminals are read by screen readers too, and by people with low vision using
large fonts and high-contrast schemes.

- **Never encode meaning in color alone.** `✓ passed` / `✗ failed` / `! warning`
  — prefix with a word or symbol, then color.
- Respect **`NO_COLOR`** (any value ⇒ no color) and **`FORCE_COLOR`**, and
  auto-disable color when stdout is not a TTY.
- Spinners and progress bars emit thousands of lines to a screen reader. Detect
  non-TTY and fall back to plain periodic status lines. Consider honoring
  `ACCESSIBLE=1` / an `--accessible` flag for a simplified, announcement-friendly
  mode (Clack, Inquirer, and Bubble Tea ecosystems have conventions for this).
- Don't rely on box-drawing characters or emoji to carry information — they read
  as noise or nothing.
- Keep line lengths reasonable; hard-wrapping at 80 breaks reflow for large fonts.
  Wrap to the actual terminal width, or don't wrap at all.
- Prompts must state their options in text (`[y/N]`), and single-key prompts must
  have a typed alternative.
- Error messages: say what failed, why, and the next action — in that order.
- Provide `--json` output. Machine-readable is also assistive-tech-readable.

## Terminal UIs specifically

Full-screen TUIs are largely opaque to screen readers. If you ship one, always
provide a non-interactive path to the same functionality (flags, config file,
`--no-tui`). That path is not a fallback — it's the accessible interface.

---

## Email

The most constrained environment; the rules are stricter, not looser.

- Real semantic HTML where clients allow it; `role="presentation"` on layout
  tables so they aren't announced as data.
- `<html lang>` and a meaningful `<title>`.
- Alt text on every image — many clients block images by default, so alt text
  *is* the content for a large share of readers.
- Never put essential information only in an image (including "view in browser"
  banners and coupon codes).
- Minimum 14–16px body text; 4.5:1 contrast; dark-mode-safe colors (clients
  invert aggressively — test in Outlook and Apple Mail dark mode).
- Descriptive link text, not "click here". Buttons: bulletproof HTML/CSS buttons,
  not images of buttons.
- Include a plain-text alternative part.

## PDFs

A PDF is accessible only if it is **tagged**.

- Export from the source tool with tagging on (Word/InDesign "Export as tagged
  PDF"), never "Print to PDF" — that produces an untagged image of text.
- Set document language and title; set the title to display in the window bar.
- Verify reading order and tags in Acrobat's Accessibility checker (or PAC 2024).
- Scanned documents need OCR before they contain any text at all.
- Forms need labeled, tab-ordered fields.
- If a PDF is the primary way to get important information, publish an HTML
  version too. HTML reflows; PDF does not.

## Documentation and content

- Descriptive link text — screen reader users navigate by link list, and
  "click here" ×40 is unusable.
- Real heading hierarchy; real lists; real tables with headers.
- Alt text on every screenshot, describing *the information the screenshot
  conveys*, not "screenshot of the settings page".
- Code blocks: don't rely on syntax-highlight color alone to explain a concept;
  annotate in prose. Highlighted colors need 4.5:1 too.
- Videos need captions and, for anything procedural, a written equivalent.
- Plain language: short sentences, expand acronyms on first use, define jargon.
  This is a cognitive accessibility requirement, not a style preference.
- Don't write instructions that depend on sensory characteristics — "the button
  on the right", "the green box" (SC 1.3.3). Name the control.

---

## Design handoff

If you're producing or reviewing designs, the cheapest accessibility wins happen
here, before any code exists. Ask for:

- Contrast-verified color tokens (including disabled, hover, focus states).
- The **focus state** for every interactive component — it's the most commonly
  missing artifact in a design system.
- Error, empty, and loading states — the states that get invented at 5pm.
- Tab order annotated on complex screens.
- Accessible names for icon-only controls, written by the designer or copywriter,
  not guessed by the implementer.
- Text at 200% and layout at 320px for at least the primary flows.
- A named alternative for any drag, hover, or gesture-only interaction.
