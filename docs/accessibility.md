# Accessibility

Target: WCAG 2.2 level AA, for the Angular app in `frontend/`.

## What is built in

- **Skip link and landmarks.** "Skip to content" is the first tab stop. The page has `header` (toolbar and
  navigation), `main` and nothing outside a landmark.
- **Headings.** One `h1` per page (the page name; on the sign-in pages the card title), `h2` for sections and
  card titles, `h3` below that.
- **Route changes.** After navigating, focus moves to the page `h1` and the tab title is announced through a
  polite live region (`role="status"`). Pages that draw their heading after loading get focus once it appears.
- **Forms.** Every field has a visible label. Errors are shown with `mat-error`, which Material ties to the
  field with `aria-describedby` and `aria-invalid`. Number fields use `inputmode` (decimal for weights and
  prices, numeric for counts) so phone keyboards fit.
- **Tables.** Real `table`s with `scope` on every header, an `aria-label` on each, and "Actions" as hidden text
  for icon-less action columns. Row names are in the buttons (`Edit 22KY`, `Delete 1.10 mm 2HD`).
- **Dialogs.** Material dialogs trap focus, close on Escape and return focus to the button that opened them.
- **Status messages.** Snack bars, busy bars (`aria-label` says what is loading), result counts and import
  checks use live regions.
- **Focus.** A 3 px ring on everything Material does not already outline (links, raw buttons, textareas, file
  inputs), in the cream colour on the maroon toolbar.
- **Motion.** `prefers-reduced-motion` switches off animations and transitions.
- **Size and reflow.** No horizontal page scroll at 320 px (400 % zoom of a 1280 px window) even with 200 %
  text. Controls are at least 24 px (WCAG 2.5.8) and 44 px on phones.
- **Colour.** Light and dark themes are checked by axe for contrast (the maroon/cream theme included).

## How to check again

The checks are Playwright tests in `frontend/e2e/smoke.a11y.e2e.ts` (they run after `smoke.e2e.ts` and
`smoke.mobile.e2e.ts` because they need the designs those create). They run with the other browser tests:

```bash
WEB_PORT=18080 scripts/e2e.sh                 # whole suite, as CI does
WEB_PORT=18080 scripts/e2e.sh smoke.a11y      # only these
```

- `axe: every screen, <light|dark>, <desktop|phone>`: signs in and runs axe-core (tags wcag2a, wcag2aa,
  wcag21a, wcag21aa, wcag22aa and best-practice) over sign-in, forgot/reset password, catalog (filters,
  target-purity list, new-design and import dialogs), design detail (metal list, edit and add-variation
  dialogs, media viewer), the variation editor, master data (all three tabs and all their dialogs), Removed,
  Audit, Backups, Users (new, edit, set-password dialogs), the account menu, the phone menu and the
  change-password dialog. Any serious or critical violation fails the test. The other findings are printed and
  written to `frontend/test-results/a11y-<scheme>-<size>.json`.
- `keyboard: ...` checks the skip link, focus after navigation, dialog focus handling, the focus ring, the
  media gallery, the file drop zones and the seat grid with the keyboard only.
- `reduced motion, 200% text and 320 px reflow ...` checks reflow and reduced motion.
- `smoke.mobile.e2e.ts` covers the phone layout at 320, 375, 768 and 812x375 (landscape).

## Known limits

- **No screen reader was used.** Nothing here was tested with NVDA, JAWS, VoiceOver or TalkBack. axe finds
  about a third of WCAG problems; the rest (is the reading order sensible, are the names helpful) need a person.
  Before relying on this for someone who uses a screen reader, test the sign-in, catalog and design pages with
  one.
- **Table cards on a phone** are made with CSS (`table.plain.stack` and `data-label`). Browsers differ in
  whether a table turned into blocks keeps its table role for screen readers; the cell labels are generated
  text, which current Chrome, Firefox and Safari read.
- **Photos and video** uploaded by staff have no text alternative beyond the file name, and videos have no
  captions. They are product photos for internal use; if a video ever has speech it needs captions.
- **Colour contrast** is checked by axe on the states the tests reach (not on every hover or disabled state;
  disabled controls are exempt from the rule).
- **Native pickers** (file chooser, date input, the browser's own confirm box for deleting a photo) use the
  browser's accessibility, which we did not change.
- Touch targets under 44 px remain for inline text links and chips (all are at least 24 px).

## Minor findings left (not blocking)

See the list printed by the test run; at the time of writing the remaining items are best-practice
suggestions only.
