# ReturnIt mobile app: where it stands

As of **25 October 2026**, app version **1.3.2** (minimum allowed version **1.3.1**). Written for anyone who picks the
project up: what the app does today, how it is built and shipped, what was tested, and what is still open. For the screen
by screen API calls see `02-screens-and-api-map.md`; for the exact API see `03-openapi.yaml`.

ReturnIt is a **warranty tracker**. Receipts are supporting proof for a warranty, not a general receipt vault (decision of
25 October 2026; see the SRS change log, section 8).

## 1. What the app does

| Area | What works |
|---|---|
| Sign-in | Keycloak in the system browser (OIDC + PKCE), e-mail and password or Google, registration on the sign-in page, silent token refresh. The sign-in pages use the ReturnIt look and open in the app's language |
| Purchases | Add, edit, delete, search, filter by warranty status, paging; duration 3, 6, 12, 24, 36 months or unknown; price, store, notes |
| Warranty status | Computed on the phone: active, ending soon (30 days), expired, unknown; coloured banner and days left |
| Return deadline | Optional last day the shop takes the product back (none, 7, 14, 30 days after purchase); its own banner, a line on the card within 14 days, reminders 3 days and 1 day before |
| Receipts | Camera or file (PDF, JPEG, PNG up to 10 MB), one per purchase, preview of images and PDF in the app, malware scan on the server, private signed links, cached on the phone |
| Home | Counters, "value still under warranty", next warranty to end, nearest return deadline, five newest purchases, update invitation |
| Reminders | Tab "Pengingat": history with filters All and Unread, unread badge on the tab, warning when phone notifications are off, "Remind me again" (snooze) with only the choices that fall before the end date. Settings on their own screen (days, channel push, e-mail or in-app, quiet hours, time zone, summary sentence) |
| Calendar | One tap adds the warranty end or the return deadline to the phone's calendar (system screen, no calendar permission) |
| Languages | Indonesian (default), English, Arabic (right to left). Choose in Settings or on the sign-in screen, or follow the phone |
| Account | Name, e-mail, member since, export (CSV made in the app from the JSON export), delete account with the 7 day waiting period and cancel, sign out |
| Privacy | App lock (fingerprint, face or phone PIN) at start and after 30 seconds away; data cache only in memory and cleared at sign-out |
| Updates | The server can invite, require an update (lock screen), or announce maintenance; the app never locks anyone out when offline |
| App log | One line per request on the console (method, path, status, time), no secrets; bodies only in debug builds. No way to share it from the app |
| Greeting | Home greets by first name; the name follows the login account at every sign-in |

Not in the app yet: more than one receipt per purchase (planned, up to 5 files), a "Tenggat" (deadlines) tab, automatic
reading of receipts, family sharing, an iPhone version.

## 2. Versions shipped

| Version | Date | Main content |
|---|---|---|
| 1.1.0 | 25 Oct 2026 | First published APK; force update, maintenance and the download host |
| 1.2.0 | 25 Oct 2026 | App lock |
| 1.3.0 | 25 Oct 2026 | Return deadline, Home summary, calendar, snooze, three languages, app log |
| 1.3.1 | 25 Oct 2026 | Arabic to English direction fix, snooze choices limited to before the end date, greeting by first name, simpler sign-in screen, new sign-in pages. Minimum raised to 1.3.1 |
| 1.3.2 | 25 Oct 2026 | Reminders tab redesign (history first, filters, gear for settings, badge, warning, status line in Settings) |

The product owner reports that everything released through 1.3.2 was tried on a phone and works.

## 3. How it is built

- **Stack:** Expo SDK 58, React Native 0.88, Expo Router, TanStack Query (memory only, 30 s fresh, cleared at sign-out).
  Repository `lildwagz/ReturnitMobile`, work branch **`release/release`** (the `main` branch belongs to the mobile developer and is not touched).
- **Texts:** an own small translation layer in `src/i18n` (`t()` and `tn()` for plural forms, one `{id, en, ar}` entry per key in `src/i18n/dict/*.ts`; Arabic has all six plural forms). Right to left is applied by the system when the app starts, so a change between left to right and right to left asks the user to reopen the app. Arabic uses the Noto Sans Arabic font. Texts that come from the server (reminders, e-mails) are still Indonesian.
- **Dates and money:** plain dates `YYYY-MM-DD`, month names per language, Western digits, "." as thousands separator in Indonesian and "," in English and Arabic.
- **Screens:** `src/app` (routes), `src/components`, `src/lib` (API calls, auth, cache, log, calendar, export), `src/i18n`.
- **Builds:** EAS (team `lildwagzs-team`). Profile `preview-arm64` makes a 64-bit APK of about 41 MB in about 11 minutes; profile `production` makes a universal APK of about 100 MB. Release builds also bundle the web build, so native-only libraries must be imported only from native files.
- **iOS:** not built. It needs an Apple Developer account, a bundle identifier in `app.json`, an APNs key for push, and TestFlight or ad-hoc installation.

## 4. How a release is made

1. Raise `version` in `app.json` and commit on `release/release`.
2. Push both repositories (backend `main`, app `release/release`).
3. On the server, in `~/returnit/deploy`: `git pull`, then `./up.sh up -d --build web worker caddy`. The database migration runs by itself. **Deploy the backend before publishing an app that needs it.** If `Caddyfile` changed, recreate Caddy (`./up.sh up -d --force-recreate caddy`): after a `git pull` the container can still see the old file.
4. Build: `npx eas-cli build --profile preview-arm64 --platform android`.
5. Publish: `./publish-apk.sh publish <version> <apk url> [--min <version>] --note "..."`. `--min` locks older apps until they update; leave it out for changes that only touch the screens. The script refuses a minimum above the newest build.
6. `./publish-apk.sh page` rebuilds the download page; `status` shows what the app is being told.
7. The login pages use the `returnit` theme (`deploy/keycloak/themes`); on a running server apply it once with `./apply-login-theme.sh`.

The download page `https://dl.<domain>/` is English, generated from `deploy/download-page/index.template.html`, and shows version, size, date, "What's new" (the `--note` lines), features, install steps and the file's SHA-256.

## 5. Quality and tests

- **Backend:** `go test ./...` and `go test -tags integration ./...` pass (including return deadline, snooze, summary, name refresh, version gate).
- **App:** `npx tsc --noEmit` shows only five old errors (web files, `global.css`, `ListEmptyComponent`); web and Android exports and `expo-doctor` pass. There are no automated app tests; the manual plan is `07-qa/qa-test-plan.md` (sections 3.1 to 3.9; Indonesian copy `qa-test-plan.id.md`).
- **Not yet checked:** Arabic wording by a native speaker, usability test (NFR-08), performance targets (NFR-04, NFR-05), accessibility audit.

## 6. Known limits and open work

| Item | Note |
|---|---|
| Server texts in three languages | Reminder and e-mail texts are Indonesian; needs a language per user in the backend |
| One receipt per purchase | A long receipt or a receipt plus warranty card needs several files (design: up to 5 files of 10 MB) |
| Backups | `backup.sh` and `monitor.sh` exist but are **not scheduled on the server**; there are no backups yet |
| Debug logging on the server | `LOG_REQUEST_BODY`, `LOG_SQL`, `LOG_REDIS` must be off before real users (the app's own build never logs bodies) |
| Privacy policy and terms | Not written (needed by Google Play and the App Store) |
| Own domain, SPF and DKIM | The server still runs on `*.sslip.io` |
| Unverified accounts | No clean-up of accounts whose e-mail was never verified |
| Header-based version gate | The version header can be forged: it protects against a broken old release, not against abuse |
| Name | The first word of the account name is used for the greeting; first and last name are not stored separately |
| Product decisions | Product photo (X-01), extra poster items (X-02), tab "Tenggat" and the differentiators discussed on 25 October |
