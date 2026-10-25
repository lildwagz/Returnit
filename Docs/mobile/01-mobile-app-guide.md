# ReturnIt: guide for the mobile app developer

> Part of the mobile developer folder. Start at `README.md` in this folder; `02-screens-and-api-map.md` lists the API calls per screen.

The backend is a JSON API under `/api/v1`. The exact request and response shapes are in
`03-openapi.yaml` (in this folder); this guide explains how the pieces fit together and the rules the app must follow.

## 0. Getting started

### What you need from the backend owner
Ask for these values (they differ per environment):

| Value | Dev (local) | Real server |
|---|---|---|
| Issuer (login server) | `http://localhost:8180/realms/returnit` | `https://auth.<domain>/realms/returnit` |
| API base URL | `http://localhost:8080/api/v1` | `https://api.<domain>/api/v1` |
| Client id | `returnit-app` | `returnit-app` |
| Audience | `returnit-api` | `returnit-api` |
| Redirect URI | `com.returnit.app:/oauth2redirect` | whatever you and the backend owner agreed (it must match on both sides) |

The redirect URI uses your app's own URL scheme; register that scheme in the app (Android intent filter /
iOS URL type). Tell the backend owner if you change it.

### Easiest: develop against a staging server
Ask the backend owner to deploy a staging server (the backend team follows `deploy/README.md` in the backend repository; plan a few hours the first time). It has real HTTPS, so
the app needs no special settings and works on real phones. This is the recommended way.

### Alternative: run the backend on your own computer
This needs the full backend repository and Docker; the paths below are inside that repository. If you only have this folder, use a staging server.

```
docker compose --profile auth up -d --wait postgres redis minio clamav mailpit keycloak
docker compose run --rm migrate
# PowerShell, in two windows:  . .\scripts\dev-env.ps1   then   go run ./cmd/web    /    go run ./cmd/worker
```
Test users: `alice` / `alice-dev-pass` and `bob` / `bob-dev-pass`. E-mails the backend sends can be read at
http://localhost:8025.

* The login server insists on the address `http://localhost:8180`, so the app must reach it as `localhost`.
  * **iOS simulator:** works as is.
  * **Android emulator or phone over USB:** run `adb reverse tcp:8180 tcp:8180` and `adb reverse tcp:8080 tcp:8080`
    so `localhost` on the device means your computer. Do not use `10.0.2.2`: the issuer in the token would not match.
* These dev addresses are plain `http`. Android blocks that by default, so allow cleartext for `localhost` in a
  **debug-only** network security config; iOS needs an App Transport Security exception for `localhost`. Never ship either.
* The dev server has the password login enabled for automated tests. Do not use it in the app.

### Signing up and logging in
There is no sign-up API: on a real server the **login page** (Keycloak, shown in the system browser) has
"Register" (self-registration is switched off in the local dev setup: use the test users there).
New users enter an e-mail address and password (at least 10 characters), then must click the link in the
verification e-mail before they can finish. The same page handles "Forgot password". The app only starts
the login (`prompt=login` when it needs a fresh one) and receives the tokens; it never sees the password.

After the first successful API call the backend creates the user's profile automatically from the token
(no "create account" call is needed).

If the backend owner enables **Sign in with Google**, the login page simply shows an extra Google button; the app does
not change (it still only opens the browser login). Tokens look the same. A first Google login creates the account
(e-mail already verified); if the same e-mail already has a password account, the login page asks the person to link them.

Rules for the app when Google is on:
* Open the login in the **system browser / Chrome Custom Tabs** (what `expo-auth-session` and `expo-web-browser` do), never in an
  embedded WebView: Google refuses to sign in inside a WebView (`disallowed_useragent`).
* Optional "Continue with Google" button in the app that skips the Keycloak page: add the authorization parameter
  `kc_idp_hint=google` (in expo-auth-session: `extraParams: { kc_idp_hint: 'google' }`). The flow is otherwise identical
  (same redirect URI, PKCE, tokens). Without the parameter the Keycloak login page is shown, with the Google button.
* While the Google consent screen is in "Testing", only the listed test users can sign in and Google shows an
  "unverified app" warning. Both go away after the screen is published (needs a real domain and a privacy-policy link).
* On iPhone, Apple's rules may require "Sign in with Apple" next to other social logins: check before submitting.

### Suggested order of work
1. Login with the browser flow and call `GET /account` (proves tokens, issuer and audience are right).
2. Purchases list, add, edit, delete.
3. Receipt upload and viewing.
4. Reminder settings and the notification inbox.
5. Push registration (`POST /devices`), then a real test push (the backend owner needs your Firebase project).
6. Account screen: export, delete (fresh login), cancel.

### Generating a client
`03-openapi.yaml` is a standard OpenAPI 3 file; tools such as OpenAPI Generator can create typed
Dart / Kotlin / Swift / TypeScript clients from it. Check the generated code against the rules below
(token handling, errors, signed URLs) rather than trusting it blindly.

## 1. Login (OIDC + PKCE)

The backend does not handle passwords. Log in with the identity provider (Keycloak) using the standard
**authorization code flow with PKCE** in the system browser (use an AppAuth library: AppAuth-Android /
AppAuth-iOS, or `flutter_appauth`, `react-native-app-auth`). Never use the password grant or an embedded web view.

- Scopes: `openid profile email`.
- Send the **access token** on every API call: `Authorization: Bearer <token>`.
- The token must have the audience `returnit-api` (the provider is configured to add it).
- On `401`, refresh the token with the refresh token; if that fails, log in again.
- Redirect address: a custom scheme or app link of your app (the provider must list it).
- Dev provider and test users: see section 0.

## 2. Standard behaviour of the API

- **Errors** always look like `{"error":{"code":"...","message":"...","request_id":"..."}}`. Show the user a
  friendly message and keep `request_id` for support.
- **`429 Too Many Requests`** has a `Retry-After` header (seconds). Wait, then retry. Do not retry in a tight loop.
- **`409` with code `identity_conflict`** on any call means the user's e-mail address belongs to an account with a different login identity (for example the login was deleted and re-created). The backend never merges these automatically. Show "please contact support" and do not retry.
- **Other users' data** looks like it does not exist (`404`), never `403`.
- Dates are `YYYY-MM-DD`; times are ISO-8601 UTC. Default currency is `IDR`.
- Reminder times are shown in the user's timezone (default `Asia/Jakarta`, WIB).

## 3. Core screens and the calls behind them

| Screen | Call |
|---|---|
| List / search purchases | `GET /purchases` (`?q=` to search, `?warrantyStatus=`, paging with `page` and `pageSize`) |
| Add purchase | `POST /purchases` (required: `productName`, `purchaseDate`; optional store, price, currency, warranty dates, notes) |
| Purchase detail / edit / delete | `GET`, `PATCH`, `DELETE /purchases/{id}`. **`PATCH` replaces the whole record**: send every field again (like create); anything you leave out is cleared |
| Attach receipt (camera or file) | `POST /purchases/{id}/receipt`, multipart form, field name **`file`** |
| View receipt | `GET /purchases/{id}/receipt` returns a **signed URL valid about 5 minutes**; open it right away, never store or log it |
| Remove receipt | `DELETE /purchases/{id}/receipt` |
| Home summary | `GET /summary`: `counts` (`active`, `expiring`, `expired`, `unknown`, `total`), `coveredValue` per currency, `nextExpiring` and `nextReturn` (the nearest warranty end and return deadline, each with the purchase id; null when there is none) |
| Return deadline | Optional field `returnDeadline` (`YYYY-MM-DD`, on or after `purchaseDate`) on create and `PATCH`; it has its own reminders 3 days and 1 day before (notifications of kind `return`) |
| Reminder settings | `GET` / `PUT /reminder-preferences` (PUT replaces everything; allowed days before: 1, 3, 7, 14, 30; channel `push`, `email` or `in_app`) |
| Notification inbox | `GET /notifications` (`?unreadOnly=true`, `?limit=`), `POST /notifications/{id}/read`. Each item has `kind` (`warranty` or `return`) and `snoozeUntil` |
| Remind me again | `POST /notifications/{id}/snooze` with `{"until":"tomorrow"|"in_3_days"|"next_week"}` returns `{scheduledAt}`. Offer **only** the choices listed in the item's `snoozeUntil` (those that still fall on or before the date the reminder is about); show no button when the list is empty. `422 snooze_not_possible` if the server refuses anyway |

**Receipts:** PDF, JPEG or PNG only, at most 10 MiB. A new upload replaces the old receipt. The server scans files for
malware: `422` means the file was rejected as infected, `503` means the scanner is temporarily down (ask the user to
try again later), `413` too large, `415` wrong type. Compress photos before upload.

## 4. Push notifications

1. Ask the user for notification permission (Android 13+ and iOS require it).
2. Get the Firebase (FCM) token (`firebase_messaging` or the native SDK).
3. Send it: `POST /devices` with `{"platform":"android"|"ios","pushToken":"..."}`. Do this **after every login, at every
   app start, and whenever the token refreshes**. Keep the returned `id`.
4. On **sign-out**, call `DELETE /devices/{id}` (otherwise the next person using that phone could receive these reminders).
5. Create an Android notification channel with the id **`reminders`**.
6. The push carries `data.notificationId`; when the user taps it, open that item from `GET /notifications`.
7. Reminders always also appear in the inbox, so the inbox is the source of truth if a push is missed.

Reminders are sent at 09:00 in the user's timezone, at the chosen days before the warranty ends. Users who set quiet
hours get them afterwards.

## 5. Account screen (required by the app stores)

- `GET /account`: profile; `deletionScheduledFor` is not null while deletion is pending.
- **Delete account:** `DELETE /account`. The server requires a **fresh login**: if it answers
  `401` with code `reauthentication_required`, send the user through the login page again with `prompt=login`
  (and `max_age=0`), then repeat the call with the new token. Refreshing a token does not count.
  The answer `202` includes the erasure date (7 days later). Tell the user clearly that it is permanent after that date.
- **Cancel deletion:** `POST /account/deletion/cancel` (until the date; afterwards `409`).
- **Download my data:** `GET /account/export` returns a JSON file; save or share it with the system share sheet.
  It does not contain the receipt files (each purchase has a `downloadPath` to fetch its file).
- The user also receives e-mails when deletion is requested, 24 hours before, and after erasure.

## 6. App version, force update and maintenance

The server can lock an outdated build of the app, invite users to update, or announce maintenance. The app does not
decide this itself: it sends its version and obeys the answer.

- **Ask at start and on return to the foreground:** `GET /app-config?platform=android&version=<app version>` (public, no login). The
  answer has a `status`: `ok` (carry on), `update_available` (show an invitation that can be closed), `update_required` (show a
  screen that cannot be closed, with the `downloadUrl`) or `maintenance` (maintenance screen, with a `message`). If the call fails,
  carry on: being offline must never lock anyone out.
- **Send the version with every API call:** headers `X-App-Version` (the `version` in app.json, for example `1.4.0`) and
  `X-App-Platform` (`android` or `ios`). A call from a version below the minimum is answered `426` with error code
  `app_update_required` (the error carries `minVersion` and `downloadUrl`); during maintenance every call is answered `503` with code
  `maintenance`. Treat both like the start-up answer and show the same screens. This also stops an old build that was missed by the start-up check.
- **Versions** are compared as numbers (`1.10.0` is newer than `1.9.0`). Raise `version` in app.json for every build you hand out.
- The header can be forged, so this protects against a broken old release; it is not a security control.
- Implemented in the app: `src/lib/app-gate.ts`, `src/lib/app-config.ts`, `src/components/update-gate.tsx` (lock screen) and
  `src/components/update-banner.tsx` (invitation on Home). Contract: `03-openapi.yaml`.

## 7. Languages and right to left

The app is in Indonesian (default), English and Arabic. Texts live in `src/i18n/dict/*.ts`, one entry per key with the three languages (`t('key')`; `tn('key', n)` for words that depend on a count, with the six Arabic plural forms in `dict/plurals.ts`). The choice is stored on the phone (`rt_lang`; "system" follows the phone, any language other than Indonesian or Arabic shows English).

- Arabic runs right to left. The system applies the direction when the app starts, so changing between a left-to-right and a right-to-left language shows "reopen the app". The saved direction must follow every choice (including going back to English), or the layout stays right to left after a restart.
- Use start and end instead of left and right in styles (`paddingStart`, `borderStartWidth`) and a translated text for arrows and chevrons.
- Do not keep translated strings in module-level constants: they are fixed at load and will not follow a language change. Use a function.
- Pass `ui_locales` to the login page (the app does) so Keycloak opens in the same language. The sign-in theme has Indonesian, English and Arabic texts.
- Texts that come from the server (reminders, e-mails) are Indonesian until the backend stores a language per user.

## 8. Good practice

- Store tokens only in the platform secure storage (Keychain / Android Keystore).
- Never log tokens, signed receipt URLs or push tokens.
- Treat the server as the source of truth; refresh lists when the app returns to the foreground.
- Support Indonesian, English and Arabic text; amounts in Rupiah are whole numbers shown with thousand separators.
- The app log (`src/lib/log.ts`) writes method, path, status and time of every request and never tokens, passwords, push tokens or receipt links. Request and response bodies are written only in a development build or one made with `EXPO_PUBLIC_LOG_BODY=true`.
