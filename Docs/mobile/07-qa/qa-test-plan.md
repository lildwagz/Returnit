# QA test plan: ReturnIt mobile app

Indonesian version: `qa-test-plan.id.md` (same content; keep the two in step).

Manual test cases for the Android app (iOS later) against the real backend. Run the whole list before every release and
the "Smoke" cases after every new build. Behaviour comes from `01-mobile-app-guide.md`, `02-screens-and-api-map.md`
and the requirements in `05-requirements/`; error codes are in `03-openapi.yaml`.

Mark each case **Pass / Fail / Blocked** and note the build number. A failed case becomes a bug with the steps, the
expected and the actual result, a screenshot, and (if it is a server error) the `requestId` from the response body.

## 1. Test environments

| Environment | API | Login server | Use for |
|---|---|---|---|
| Local dev | `http://localhost:8080/api/v1` (emulator: `adb reverse tcp:8080 tcp:8080`, same for `8180`) | `http://localhost:8180/realms/returnit` | Daily development |
| Staging | `https://api.<domain>/api/v1` | `https://auth.<domain>/realms/returnit` | Release testing, real phones |

Do not use `10.0.2.2` or a LAN IP for the login server: the token issuer would not match and every API call returns 401.

**Test accounts (local dev only):** `alice` / `alice-dev-pass`, `bob` / `bob-dev-pass`. A new account created in
Keycloak must have a first and last name or it cannot log in. On staging, register fresh accounts and use a mailbox you
control. Never test with real people's data.

**Devices to cover:** one low-end and one recent Android phone (Android 10 and the newest version), one small and one
large screen, one phone with Indonesian system language and one with English. Real phone required for push tests.

**Useful tools:** local dev mail inbox at `http://localhost:8025` (Mailpit); MinIO console `http://localhost:9001`
(`returnit` / `returnit_dev_secret`); `docker compose logs web worker --tail 50` for server-side reasons.

**Test files** (prepare once): a JPEG under 1 MiB, a PNG, a PDF, a JPEG of about 9 MiB, a file of about 12 MiB, a `.txt`
file renamed to `.jpg`, an empty (0 byte) file, a file with a very long name.

## 2. Smoke test (about 10 minutes, every build)

| ID | Steps | Expected |
|---|---|---|
| S1 | Install and open the app | Opens without crash or red error screen |
| S2 | Log in with a test account | Browser opens the login page, returns to the app, Home shows |
| S3 | Add a purchase with a warranty end date | It appears in the list |
| S4 | Attach a JPEG receipt to it | Upload succeeds, receipt viewable on the detail screen |
| S5 | Kill the app, open again | Still logged in, data still there |
| S6 | Log out | Back to the logged-out state |

## 3. Full test cases

### 3.1 Login, session, sign-out

| ID | Case | Steps | Expected |
|---|---|---|---|
| A1 | Login works | Tap login, enter valid credentials | Returns to app, Home loaded |
| A2 | Wrong password | Enter a wrong password | Error shown on the login page, no crash; after 5 wrong tries the account is temporarily locked |
| A3 | Cancel login | Close the browser tab or tap back | App stays on the logged-out screen, can retry |
| A4 | Register | Use "Register" on the login page with a new e-mail | Account created; (staging) verification e-mail arrives; can log in after verifying |
| A5 | Forgot password | Use "Forgot password" | Reset e-mail arrives and the new password works |
| A6 | Silent token refresh | Stay in the app for more than 6 minutes (token lives about 5), then open a list | No login prompt, data loads |
| A7 | Session survives restart | Force-stop the app, reopen | Still logged in |
| A8 | Expired session | Log in on the phone. Then end the user's sessions on the server: `./kc-user.sh logout <email>` (backend team; or Keycloak console, Users, Sessions, Sign out). Keep using the app. | For up to about 5 minutes the app keeps working (the access token already issued is still valid). Once it expires, the silent refresh is refused (`invalid_grant`) and the app must show the login screen: no crash, no endless spinner, no raw error. After logging in again the user's data is there. To skip the wait, leave the app in the background for 6 minutes before using it. |
| A9 | Sign-out | Settings, sign out | Back to logged-out; the device is removed from the server (`DELETE /devices/{id}`); the next login starts clean |
| A10 | Two users on one phone | Log out alice, log in as bob | Bob sees none of alice's data (no cached list) |
| A11 | Different login, same e-mail | Log in with an identity whose e-mail already belongs to another account | App shows a clear message built from `409 identity_conflict` ("contact support"), not a blank screen |
| A12 | No network at login | Airplane mode, tap login | Friendly error, can retry later |
| A13 | App lock: turn on | Settings, switch "Kunci aplikasi" on (the phone has a screen lock or fingerprint) | The system asks for fingerprint, face or PIN; after success the switch stays on |
| A14 | App lock: phone without a screen lock | Remove the phone's screen lock, then try to switch it on | Message asking to set a screen lock first; the switch stays off |
| A15 | App lock at start | With the lock on, force-stop the app and open it | The "ReturnIt terkunci" screen with the system prompt; the content is not visible before unlocking |
| A16 | App lock after absence | Leave the app for more than 30 seconds, return | Locked again, and after unlocking you are on the same screen as before |
| A17 | App lock short absence | Leave the app for under 30 seconds (answer a message), return | Not locked |
| A18 | App lock: cancel | On the lock screen, cancel the system prompt | Stays locked; "Buka kunci" tries again; the screens underneath cannot be touched |
| A19 | App lock: turn off | Switch it off in Settings | Asks for fingerprint, face or PIN first; afterwards the app opens without a prompt |
| A20 | App lock: sign out from the lock screen | Tap "Keluar dari akun" on the lock screen | Back to the login screen; no data of the account visible |
| A21 | App lock and the system prompt | Unlock, and open the system prompt again by switching the lock off | Opening and closing the system dialog never locks the app by itself |

### 3.2 Purchases: add, list, search, filter

| ID | Case | Steps | Expected |
|---|---|---|---|
| P1 | Add with all fields | Fill name, store, date, duration 12 months, price, notes | Created; detail shows correct warranty end (start + 12 months) |
| P2 | Add with required fields only | Name and purchase date only | Created; warranty shows "unknown" |
| P3 | Missing required field | Leave name empty | Inline validation message, nothing sent |
| P4 | Name at limit | 160 characters, then 161 | 160 accepted; 161 blocked by the app or shown as a validation error |
| P5 | Special characters | Name with emoji, quotes, `<script>`, Indonesian text | Saved and shown exactly as typed, no layout break |
| P6 | Price edge cases | 0, 1500000, 1500000.50, very large, negative | 0 and valid numbers accepted; negative rejected |
| P7 | Date edge cases | Today, a past date, 29 Feb on a leap year, end of month + 1 month | Accepted; month arithmetic gives a valid date (31 Jan + 1 month = end of Feb) |
| P8 | List shows newest first | Add three purchases with different dates | Ordered by purchase date, newest first |
| P9 | Pagination | Create 25+ purchases, scroll | Next page loads, no duplicates or gaps |
| P10 | Search | Type part of a name or store | Only matching items; clearing the box restores the list |
| P11 | Filter Aktif | Choose the chip | Only purchases with warranty still running |
| P12 | Filter Segera Berakhir | Add a purchase ending in 10 days | It appears; one ending in 60 days does not |
| P13 | Filter Kedaluwarsa | Add a purchase whose warranty ended yesterday | It appears |
| P14 | Filter Tidak Diketahui | Add a purchase without warranty dates | It appears |
| P15 | Home counters | Compare the three counters with the list filters | Numbers match |
| P16 | Empty state | New account, no purchases | Friendly empty state with an "add" button, not an error |
| P17 | Pull to refresh | Add a purchase from a second device or Postman, pull down | New item appears |
| P18 | Late-night status | Between 00:00 and 07:00 WIB, check an item ending today/yesterday | Known limitation: the server may count it one day late; "days left" shown on screen is computed on the phone and must be right |

### 3.3 Purchase detail, edit, delete

| ID | Case | Steps | Expected |
|---|---|---|---|
| D1 | Detail content | Open a purchase | All fields shown; days left and status correct |
| D2 | Edit | Change the store and save | Change shown in detail and list; **other fields unchanged** (the app sends the complete object) |
| D3 | Edit keeps notes and price | Edit only the name | Price, notes and dates are still there after saving |
| D4 | Clear a field on purpose | Remove the notes and save | Notes gone |
| D5 | Delete | Delete a purchase and confirm | Gone from list, counters drop; receipt file removed |
| D6 | Cancel delete | Tap delete, then cancel | Nothing deleted |
| D7 | Open another user's purchase | Using Postman, request alice's purchase id with bob's token | `404`, never the data |
| D8 | Deleted item opened from old screen | Delete on another device, open on this one | Clean "not found" handling, no crash |

### 3.4 Receipts

| ID | Case | Steps | Expected |
|---|---|---|---|
| R1 | Upload JPEG | Pick a JPEG | Success, thumbnail or file name shown |
| R2 | Upload PNG and PDF | Repeat | Success for both |
| R3 | View receipt | Open the receipt | File opens/downloads from the signed link; works right after tapping |
| R4 | Replace | Upload another file to the same purchase | Old file replaced, only the new one is viewable |
| R5 | Remove | Delete the receipt | Detail shows no receipt; viewing returns "none" state |
| R6 | 9 MiB file | Upload | Succeeds (with a visible progress indicator) |
| R7 | 12 MiB file | Upload | Rejected: `413 file_too_large`; app shows "file too large (max 10 MB)" and ideally checks size before sending |
| R8 | Wrong type | Upload the `.txt` renamed to `.jpg` | Rejected with a clear message (server checks the real content) |
| R9 | Empty file | Upload 0 bytes | Rejected with a clear message |
| R10 | Long file name | Upload the long-named file | Accepted or cleanly rejected, no crash |
| R11 | Upload fails midway | Switch to airplane mode during upload | Error with "try again"; **the purchase still exists** and can be retried from the detail screen |
| R12 | Cancel the picker | Open the picker and back out | Nothing happens, no error |
| R13 | Permission denied | Deny file/photo access | Explanation shown, app does not crash |
| R14 | Old link not reused | View receipt, wait more than 5 minutes, tap the old link or screen again | App requests a new link; it never stores the old URL |
| R15 | Another user's receipt | Request bob's receipt with alice's token | `404` |
| R16 | Virus file (staging with `MALWARE_SCAN=on` only) | Upload the EICAR test string saved as a PDF-like file | Rejected as infected; clean files still pass |

### 3.5 Reminders, preferences, push notifications

Needs a real phone with the development/production build that includes `google-services.json`, and a backend with
FCM configured (see `deploy/README.md` / dev `.env`).

| ID | Case | Steps | Expected |
|---|---|---|---|
| N1 | Permission prompt | First login on Android 13+ | Asked once; app works if denied (reminders still appear in the in-app list) |
| N2 | Token registered | Allow notifications | Server has the device (`POST /devices` returned 200); registering again after reinstall does not create duplicates |
| N3 | Test push from Firebase | Firebase Console, "Send test message" with the token | Notification appears (also with the app closed) |
| N4 | Real reminder | Create a purchase whose warranty ends tomorrow; wait 1-2 minutes | Push "Garansi segera berakhir ... besok" arrives; also in the reminders list |
| N5 | Tap the push | Tap the notification | App opens on the matching purchase (uses `data.notificationId`) |
| N6 | App in foreground | Trigger N4 with the app open | Shown as in-app banner or in the list, no crash |
| N7 | Mark as read | Open a reminder, mark read | Unread badge decreases; stays read after restart |
| N8 | Unread filter | Use the unread-only view | Only unread items |
| N9 | Preferences save | Turn off the 30-day reminder, save, reopen | Setting kept; saving sends `enabled`, `daysBefore` and `channel` together |
| N10 | Disable reminders | Switch reminders off, trigger N4 | No notification for new purchases |
| N11 | Quiet hours | Set quiet hours covering "now", trigger N4 | Notification arrives only after the quiet window ends |
| N12 | Change the end date | After a reminder is planned, edit the warranty end date | The old reminder does not fire; a new one matches the new date |
| N13 | Delete purchase | Delete it before the reminder time | No reminder arrives |
| N14 | Sign out stops push | Sign out, then trigger a reminder for that user | The phone gets nothing |
| N15 | Two users, one phone | Alice logs out, bob logs in on the same phone | Alice's reminders never show for bob; the token now belongs to bob |
| N16 | Android channel | System settings, app notifications | A channel named `reminders` exists with sound/importance as designed |
| N17 | Notifications blocked in system | Disable notifications for the app | App still lists reminders in-app; no crash |
| N18 | Switch channel after a reminder arrived | Receive a reminder (inbox), open it so it is read. In reminder settings switch the channel push <-> in_app (and back), wait 2 minutes, refresh the inbox | Still ONE entry for that reminder, still marked read. No new copy appears. A reminder that was not yet due is sent once, through the channel selected when it is due |

### 3.6 Account: export, delete, cancel deletion

| ID | Case | Steps | Expected |
|---|---|---|---|
| C1 | Profile | Open account/settings | Name/e-mail shown correctly |
| C2 | Export | Tap export data | JSON (or CSV if converted) saved/shared; contains purchases, preferences, devices; **not** the receipt files |
| C3 | Delete account, stale login | Log in, wait more than 5 minutes, request deletion | App sends the user to log in again (`reauthentication_required`), then the retry succeeds |
| C4 | Delete account, fresh login | Log in just now, request deletion | `202`; app shows the erasure date (7 days ahead); confirmation e-mail arrives |
| C5 | Warning text | Open the delete screen | Clearly says what is deleted and that it can be cancelled until the date |
| C6 | During the grace period | After C4, use the app | Account shows "deletion scheduled" with a cancel option; no reminders are sent |
| C7 | Cancel deletion | Tap cancel | Deletion cancelled; e-mail confirms; data intact; reminders resume |
| C8 | Reminder e-mail | (Staging) day 6 of the grace period | Reminder e-mail arrives |
| C9 | Erasure happens | (Staging, use a throwaway account; or ask the backend team to advance the date) After the date | Data, receipts and the login identity are gone; logging in again creates an empty new account |
| C10 | Double request | Request deletion twice | No crash; the date does not move |

### 3.7 Network, errors, security

| ID | Case | Steps | Expected |
|---|---|---|---|
| E1 | Offline start | Airplane mode, open the app | Clear "no connection" state with retry; no infinite spinner |
| E2 | Connection lost mid-action | Cut the network while saving a purchase | Error and retry, no duplicate purchase after retry |
| E3 | Slow network | Throttle to 2G/3G | Spinners shown, no freeze, timeouts give a message |
| E4 | Server down | Stop the backend | Friendly error, not a stack trace or raw JSON |
| E5 | Rate limit | Trigger many requests quickly (about 60 per minute) | `429` is handled with a wait and retry, not an error loop |
| E6 | Server error 500 | (Backend team simulates) | Generic message; the `requestId` is available for support |
| E7 | Tokens not in logs | Check device logs (`adb logcat`) during a session | No tokens or passwords printed |
| E8 | Tokens stored safely | Inspect app storage | Tokens only in secure storage (Keystore), not plain preferences |
| E9 | Screenshots/recents | Open the app switcher on a receipt screen | Matches the design decision (blur or not); at least no crash |
| E10 | HTTPS only (staging) | Check network traffic | Everything over HTTPS; no HTTP calls |
| E11 | Rotate the screen | Rotate during a form and an upload | Typed data not lost; upload not duplicated |
| E12 | App backgrounded | Background the app during the login flow, return | Flow completes or restarts cleanly |
| E13 | Update required (start) | Backend team raises the minimum above your build (`./publish-apk.sh min <version>`), then open the app | Full screen "Perbarui aplikasi" with your version and the minimum; "Perbarui sekarang" and "Saya sudah memperbarui"; no other screen can be reached |
| E14 | Update required (app already open) | With the app open on Home, raise the minimum, then pull to refresh | The same lock screen appears after that API call (`426`); nothing else works until updated |
| E15 | Update invitation | Set the latest version above your build, the minimum below it | Banner "Versi X tersedia" on Home; "Nanti" hides it until the app is closed and it does not return for the same version; the app stays usable |
| E16 | Maintenance | `./publish-apk.sh maintenance on "message"` | Screen "Sedang dalam perbaikan" with the message and "Coba lagi"; after `maintenance off`, "Coba lagi" returns to normal |
| E17 | Offline never locks | Airplane mode, then open the app | The app opens normally (offline state), no update or maintenance screen |
| E18 | Download from the lock screen | Tap "Perbarui sekarang" | The browser opens `https://dl.<domain>/`, the APK downloads, and its SHA-256 equals the one on the page |

### 3.8 Language, accessibility, devices

| ID | Case | Steps | Expected |
|---|---|---|---|
| L1 | Indonesian and English | Switch the phone language | Texts follow the language (or Indonesian is consistently used as designed); no cut-off labels |
| L2 | Large font | System font scale to maximum | Screens remain usable, buttons reachable |
| L3 | Dark mode | Switch the system theme | Matches the design or at least stays readable |
| L4 | Screen reader | TalkBack on | Buttons and fields have meaningful labels |
| L5 | Small screen | 5-inch phone | No overlapping or hidden buttons |
| L6 | Dates and money | Check formats | Dates readable (e.g. 21 Okt 2026), prices as Rupiah with thousands separators |
| L7 | Back button | Use the Android back button on every screen | Goes back logically, never exits mid-form without a warning |

## 4. Server behaviour to know while testing

* **Edit replaces everything.** If a field disappears after editing, the app forgot to send it.
* **Status filters use the server's date (UTC).** Differences of one day between 00:00 and 07:00 WIB are a known backend limitation.
* **Reminder timing:** 09:00 in the user's timezone (default WIB), at 30/14/7/3/1 days before the end date. A reminder is planned
  up to 48 hours ahead and a missed one is still sent if it is less than 24 hours late. Each reminder is sent once.
* **Receipts:** PDF, JPEG or PNG up to 10 MiB. The server checks the real file content, not the extension.
* **Account erasure** happens 7 days after the request and needs a login from the last 5 minutes.
* Server-side reasons for a failed call are in `docker compose logs web worker` (local) or the server logs (staging).

## 5. Bug report template

```
Title:        short and specific
Build / device / Android version:
Environment:  local / staging
Test case ID:
Steps:        1. ... 2. ... 3. ...
Expected:
Actual:
requestId:    (from the error body, if any)
Attachments:  screenshot or screen recording; `adb logcat` excerpt without tokens
Severity:     blocker (cannot use app) / major (feature broken) / minor / cosmetic
```

## 6. Release checklist

- [ ] Smoke test passes on a real phone
- [ ] All cases in sections 3.1 to 3.7 pass or have an accepted, documented exception
- [ ] Push reminder (N4) verified on a real device against staging
- [ ] Account deletion with a throwaway account verified end to end (C3, C4, C7, C9)
- [ ] No tokens in logs (E7), HTTPS only (E10)
- [ ] Privacy texts and store forms match `06-data-and-privacy/data-retention-policy.md`
- [ ] Version number and changelog updated
