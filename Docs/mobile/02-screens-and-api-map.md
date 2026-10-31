# Screens and API map

Which API calls each screen of the design needs. Screens are taken from the interactive prototype
(`04-design/ReturnIt_Interactive_Prototype.html`, open it in a browser); requirement ids (US-xx) are from the SRS
(`05-requirements/`). Field names and error codes: `03-openapi.yaml` and `01-mobile-app-guide.md`.

Base URL: `https://api.<domain>/api/v1` (local: `http://localhost:8080/api/v1`). Every call needs
`Authorization: Bearer <access token>` except the login itself.

## Before the main screens

| Screen | What happens |
|---|---|
| Login / register | Not a screen of ours: open the system browser to the login server (OIDC + PKCE), receive tokens. "Register" and "Forgot password" live on that page. See guide, sections 0 and 1. |
| Notification permission | Ask once after login. If granted, get the Firebase token and `POST /devices` (guide, section 4). |

## Main screens

### Home ("Halo, <first name>")
| Needs | Call |
|---|---|
| Greeting with the first word of the name | `GET /account` (`displayName`; the generic greeting while it is empty) |
| Tenggat tab (a list of dates: passed, this week, this month, later) | `GET /deadlines?pastDays=30` (see the mobile guide); tap a row to open `GET /purchases/{purchaseId}` |
| "Garansi aktif", "Segera berakhir", "Total pembelian" counts | `GET /summary`, field `counts` (`active`, `expiring`, `total`) |
| "Nilai barang yang masih bergaransi", next warranty to end, nearest return deadline | the same `GET /summary` (`coveredValue`, `nextExpiring`, `nextReturn`); each date opens its purchase |
| "Pembelian terbaru" | `GET /purchases?pageSize=5` (newest purchase date first) |
| Update invitation | `GET /app-config` (see the guide, section 6) |
| Button "Tambah Pembelian" | opens the Add screen |

### Pembelian (list, search, filters)
| Needs | Call |
|---|---|
| List | `GET /purchases?page=1&pageSize=20` (default size 20, maximum 100; response has `items`, `page`, `pageSize`, `total`) |
| Search box | add `q=<text>` (matches product name and store) |
| Filter chips | add `warrantyStatus=`: Semua = omit, Aktif = `active`, Segera Berakhir = `expiring` (ends within 30 days), Kedaluwarsa = `expired`, Tidak Diketahui = `none` (no end date) |
| Category filter | add `category=<key>`; combines with `warrantyStatus` and `q` |

### Tambah Pembelian (add)
| Field in design | API field |
|---|---|
| Nama produk * | `productName` (required, max 160) |
| Toko | `storeName` |
| Tanggal pembelian * | `purchaseDate` (`YYYY-MM-DD`) |
| Durasi garansi (3/6/12/24/36 months or unknown) | **The API takes dates, not a duration.** Compute in the app: `warrantyStartDate` = purchase date, `warrantyEndDate` = start + N months. "Tidak diketahui" = send neither. |
| Batas pengembalian (optional: none, 7, 14, 30 days) | **The API takes a date:** `returnDeadline` = purchase date + N days (on or after `purchaseDate`). Leave it out for "Tidak ada". It has its own reminders 3 days and 1 day before |
| Kategori (optional icon row under the name) | `category` (key, see OpenAPI; send `null` to clear on edit, leave out to keep) |
| Harga | `price` (number; `currency` defaults to `IDR`) |
| Catatan (if you add it) | `notes` |
| Unggah struk | **Two steps:** `POST /purchases` first, then `POST /purchases/{id}/receipt` (multipart field `file`). If the upload fails, the purchase still exists: show it and offer "try again" from the detail screen. |

Create answers `201` with the purchase.

### Detail Pembelian
| Needs | Call |
|---|---|
| Show product, dates, price, receipt info | `GET /purchases/{id}` |
| Warranty status and days left | **Compute in the app** from `warrantyEndDate` (the response has no status field) |
| Return deadline banner and date | `returnDeadline` (plain date, may be null); calm while there is time, amber in the last 3 days, grey once it has passed |
| Add the warranty end or the return deadline to the calendar | No API: the app opens the phone's own new-event screen (all-day event, alarms 7 days and 1 day before). No calendar permission |
| View receipt | `GET /purchases/{id}/receipts/{receiptId}` returns a signed `url` valid about 5 minutes: open or download it immediately, never store it |
| Add a receipt | `POST /purchases/{id}/receipts` (up to 5; `409 receipt_limit_reached` beyond that). The original `POST /purchases/{id}/receipt` replaces the oldest |
| Remove a receipt | `DELETE /purchases/{id}/receipts/{receiptId}` |
| The receipts of a purchase | `receipts` in `GET /purchases/{id}` and in the list (oldest first, each with `id`, `fileName`, `contentType`, `sizeBytes`, `uploadedAt`); `receipt` is the oldest |
| Edit | `PATCH /purchases/{id}`: **send the complete object** (same fields as create); fields you leave out are cleared, not kept |
| Delete | `DELETE /purchases/{id}` (the receipt file is deleted too) |

### Pengingat (notifications)
The tab is the **history** of reminders; the settings are a separate screen (gear icon on the tab, or Pengaturan, "Preferensi pengingat").

| Needs | Call |
|---|---|
| History with filters "Semua" and "Belum dibaca" | `GET /notifications` (`?unreadOnly=true`, `?limit=`); items have `kind` (`warranty` or `return`) |
| Unread number on the tab | `GET /notifications?unreadOnly=true` (count, capped at 50) |
| Mark as read | `POST /notifications/{id}/read` |
| "Ingatkan lagi" (remind me again) | `POST /notifications/{id}/snooze` with `until` = `tomorrow`, `in_3_days` or `next_week`. Show only the choices in the item's **`snoozeUntil`**, and no button when it is empty |
| Warning "notifications are off" | No API: the phone's permission; shown only when the channel is `push`, reminders are on and the permission is missing |
| Settings screen: reminder switch, days (30 / 14 / 7 / 3 / 1), channel, quiet hours | `GET /reminder-preferences`, `PUT /reminder-preferences` (replaces everything: send `enabled`, `daysBefore` and `channel` every time) |
| Empty state when reminders are off | Offers "Aktifkan pengingat" to the settings screen |

Allowed `daysBefore` values: 1, 3, 7, 14, 30. Channel: `push` (default), `email` or `in_app`. Optional quiet hours
(`quietHoursStart`, `quietHoursEnd`, `HH:MM`) and `timezone` (default `Asia/Jakarta`). Reminders fire at 09:00 in that timezone.
A tapped push carries `data.notificationId`.

### Pengaturan (settings)
| Item in design | Call |
|---|---|
| Preferensi pengingat | opens the reminder settings screen; the row shows the state ("Aktif · 7, 1 hari sebelumnya · Notifikasi HP" or "Mati") from `GET /reminder-preferences` |
| Bahasa | No API: Ikuti bahasa HP, Bahasa Indonesia, English, العربية (stored on the phone; changing between left to right and right to left asks to reopen the app) |
| Kunci aplikasi | No API: fingerprint, face or phone PIN at start and after 30 seconds away (stored on the phone) |
| Privasi & keamanan | static text; see `06-data-and-privacy/` for what to tell users |
| Ekspor data | `GET /account/export` returns **JSON**; the app turns the purchases into a **CSV** file (decided 25 Oct 2026) and opens the share sheet. Receipt files are not inside it; each purchase has a `downloadPath`. |
| Account / profile | `GET /account` |
| Hapus akun | `DELETE /account`; needs a **fresh login** (`401 reauthentication_required` -> log in again with `prompt=login`, then retry). Answer `202` with the erasure date. |
| Batalkan penghapusan | `POST /account/deletion/cancel` (until the date) |
| Keluar (sign out) | `DELETE /devices/{id}` first, then forget the tokens |
| "Reset data demo" | prototype only, not for the real app |

### Account and Privacy screens (what to put behind the two buttons)
The prototype only shows "Privasi & keamanan" as an information row (a lock icon, no screen behind it) and has no "Account"
entry; these two screens are our recommendation, built only from what the backend offers today.

**Account** (needs login; data from `GET /account`)
| Show / do | Source or call |
|---|---|
| E-mail (read only) | `email` from `GET /account`. There is no API to change it. |
| Name (read only) | `displayName` (may be null: then show nothing or the e-mail) |
| Member since | `createdAt` |
| Change password | Not in the app. Sign out and use "Forgot password" on the login page. |
| Sign out | `DELETE /devices/{id}` (if a push token was registered), then delete the stored tokens |
| Export my data | `GET /account/export` (JSON; receipt files are not inside, each purchase has a `downloadPath`) |
| Delete my account | `DELETE /account`; needs a fresh login (`401 reauthentication_required` -> log in again with `prompt=login`, retry). Answer `202` with `deletionScheduledFor`. |
| Deletion pending | When `deletionScheduledFor` is not null show a banner "account will be erased on <date>" with a "Cancel deletion" button (`POST /account/deletion/cancel`). Reminders are paused meanwhile. |

**Privacy & security** (static text; no API). Suggested content, in plain words:
1. What we store: your purchases (product, shop, dates, price, notes), receipt files, reminder settings, the push token of your phone, your e-mail address.
2. Receipts are private: only you can open them, through links that stop working after about 5 minutes. Every file is checked for viruses before it is stored.
3. Reminders: 30, 14, 7, 3 and 1 day before a warranty ends, at 09:00 in your time zone; you can change or switch them off.
4. Your control: export your data, delete a receipt or a purchase at any time, delete your account (erased after 7 days; you can cancel until then).
5. What happens on deletion: purchases, receipts, settings and notifications are erased; only an anonymous record that an account existed is kept so a restored backup cannot bring it back.
6. A link to the **privacy policy** page (needed by Google Play and the App Store). The policy text is not written yet; the product owner must provide it. The factual basis is `06-data-and-privacy/data-retention-policy.md`.

## Requirements to API (from the SRS)

| Story | Where it is covered |
|---|---|
| US-01 Add purchase | `POST /purchases` |
| US-02 Upload receipt | `POST /purchases/{id}/receipt` |
| US-03 View detail | `GET /purchases/{id}`, `GET .../receipt` |
| US-04 Warranty calculation | app computes from `warrantyEndDate`; server filter `warrantyStatus` |
| US-05 Search | `GET /purchases?q=` |
| US-06 Reminders | `/reminder-preferences`, `/notifications`, push via `/devices` |
| US-07 Edit | `PATCH /purchases/{id}` |
| US-08 Delete | `DELETE /purchases/{id}` |
| US-09 Data privacy | private receipt storage, signed links, `GET /account/export`, `DELETE /account` |

## Things that will surprise you

* **Edit replaces everything** (see Detail). Re-send all fields.
* **Status values depend on the server's date.** `expiring`, `expired` and the counts use the database's calendar
  date (UTC). Between 00:00 and 07:00 WIB the server is still on the previous day, so an item can look one day
  "later" than on the phone. For the displayed "days left", calculate on the phone; use the server filters for lists.
* **Dates are plain dates** (`YYYY-MM-DD`), not timestamps. Do not convert them through time zones.
* **Money:** `price` is a number in the purchase's `currency` (default IDR, whole rupiah in practice).
* **The access token lasts about 5 minutes.** Refresh it silently; do not make the user log in again.
* **Receipt limits:** PDF, JPEG or PNG, 10 MiB.
* **Rate limits:** about 60 calls per minute per user, 10 uploads per minute. A `429` has `Retry-After`.
