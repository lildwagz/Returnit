# Download page: UI/UX review

Date: 27 October 2026. Page reviewed: `https://dl.<domain>/` as published with app version **1.3.2**; source
`deploy/download-page/index.template.html` (+ `assets/`), filled in by `deploy/publish-apk.sh page`.
Method: reading the template, screenshots at 390 px (inside a 390 px iframe) and 1280 px, WCAG contrast calculations, header and
size checks on the live server, and the UI UX Pro Max priorities (accessibility, touch, no emoji as icons, contrast, mobile
layout). No extra database searches were run. Related: `ui-ux-review-2026-10.md` (the app), `theme-tokens.md`.

Note on method: a first screenshot at 390 px looked cut off on the right. That was a Chrome headless artifact (it will not make a
window narrower than about 500 px). Rendered in a 390 px iframe the page does not overflow sideways.

## 1. What already works

- Heading order is right (h1, then h2 per section, h3 per card).
- Light button contrast is 5.17:1 (white on `#2563EB`); `prefers-reduced-motion` is respected.
- Version, size, date and SHA-256 are shown; the APK is served as a download with the right type (`Content-Disposition: attachment`,
  `application/vnd.android.package-archive`); security headers are present (`nosniff`, HSTS).
- The desktop layout is balanced and the page does not scroll sideways on a phone.

## 2. Findings

### P0

| # | Finding | Evidence | Fix |
|---|---|---|---|
| 1 | **Dark mode: white text on the blue button fails contrast.** In dark mode `--primary` becomes `#60A5FA`, but the button text stays white: 2.54:1 (needs 4.5). On hover the colour is `#93C5FD`: 1.80:1. The step numbers and the gradient of the closing call to action fail the same way (2.54 to 1.80). | contrast calculation; `.btn`, `.steps li::before`, `.cta` | Split the tokens: `--primary` for text and links, `--btn-bg` (`#2563EB`, hover `#1D4ED8`) for button, step number and call-to-action backgrounds in both themes |
| 2 | **On a phone the download button is below the fold.** The phone mockup comes first (`order:-1`), so on a 390x844 screen the button appears at about y=1040. Most visitors already hold an Android phone. | 390 px screenshot | On mobile the order becomes: heading, sentence, button, security line, then the mockup (smaller, or hidden) |

### P1

| # | Finding | Fix |
|---|---|---|
| 3 | **No link preview.** The link is mostly shared in chat apps, but there are no `og:title`, `og:description`, `og:image` or `twitter:card` tags | Add them, with a 1200x630 image |
| 4 | **Desktop visitors cannot install an APK.** On a laptop the "Download APK" button is of no use | On wide screens with a mouse, show a QR code "Scan with your phone". Encode the page address (not the APK), so the phone opens the page and the person taps the button. Static SVG, no script |
| 5 | **Reassurance is hidden in a closed FAQ.** The biggest drop-off when installing outside Google Play is the "Install unknown apps" and Play Protect warning, explained only in the last accordion | One line right under the button: "The file is checked. A Play Protect warning is normal for apps outside Google Play. [How to install]", linking to the steps and the SHA-256 |
| 6 | **Emoji used as icons in the eight feature cards** (shield, return arrow, bell, receipt, calendar, lock, globe, outbox). They differ per device and are pale on light backgrounds (the receipt almost vanishes). This breaks the skill's rule | Inline SVG line icons from one family (the same Feather style as the app), colours from tokens |
| 7 | **The phone mockup is out of date.** It imitates the old Home (three counters in a row, "Hello, Alex 👋") while 1.4.0 has the bento Home with a "needs attention" card and a floating "+" | Redraw it as the new Home. After 1.4.0 is tested on a phone, replace the HTML imitation with real screenshots (WebP) in a phone frame |
| 8 | **"What's new" pushes the features down** and reads like internal notes ("Reminders tab redesigned: history first, ...") | Move it below the features or make it a `<details>`; write the notes for users |

### P2

- **Small-text contrast:** grey `#64748B` on `--soft` (statistic labels, the SHA box) is 4.30:1; the "Ending soon" tag is 4.51:1. Darken light `--muted` to about `#526071`, as in the app.
- **Performance:** the logo PNG is 92 KB (dark 105 KB) for a 190x51 image; two TTF fonts are about 95 KB each. Use SVG or a small WebP for the logo and WOFF2 for the fonts (about 30-40% smaller), and preload the bold font. `/assets/*` has no `Cache-Control`, so every visit revalidates; give it a `max-age` in the Caddyfile (a Caddyfile change needs `--force-recreate caddy` on the server).
- **Keyboard focus:** no clear `:focus-visible` style on the button, the FAQ summaries and the links. Add a branded focus ring.
- **Footer:** no contact address or privacy policy link. A privacy policy is also needed for Google Play (item U-15, not yet written).
- **Mockup text** is 10-11 px and "Ending soon" wraps to two lines in the statistic box. It is `aria-hidden`, so not an accessibility issue, but it looks cramped.
- **Language:** English only, as requested, while the app's main language is Indonesian. If most users are Indonesian, consider a language switch (needs a little script; the page is script-free today).

## 3. Order of work

1. **Quick, no new files except two small assets:** dark-mode button tokens (1), mobile order (2), Open Graph tags (3), reassurance line under the button (5), `--muted`, focus ring.
2. **Medium:** SVG icons (6), QR code on wide screens (4), "What's new" moved and rewritten (8), new mockup (7), cache headers and font preload.
3. **After 1.4.0 is tested on a phone:** real screenshots (7) and the `og:image` from them.

All of it changes only `deploy/download-page/index.template.html` and `deploy/download-page/assets/` (and `deploy/Caddyfile` for caching).
It reaches users only after a deploy and `./publish-apk.sh page` on the server.
