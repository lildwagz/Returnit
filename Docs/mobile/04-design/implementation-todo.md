# Mobile implementation map and to-do list

Purpose: one place that says where each design screen lives in the app and what is still different from the design,
so nobody re-reads the prototype and the whole codebase to find out. Update the status column when an item is done.

Sources: `ReturnIt_Interactive_Prototype.html` (read in full, 2026-10-25), `ReturnIt_Paket_UIUX_Visual.docx` (poster),
`02-screens-and-api-map.md`, `theme-tokens.md`. App repo: `lildwagzz/ReturnitMobile`, work branch `local-prod-build`.
Status: done / todo / decision (needs the product owner) / skip (on purpose).

## Where things are (screen to file)

| Design screen | File | Shared parts |
|---|---|---|
| Login | `src/app/login.tsx` (+ `loading.tsx`) | `components/login-gate.tsx`, `lib/auth.ts` |
| Route guard (tabs only after login) | `src/app/_layout.tsx` | `lib/use-signed-in.ts` |
| Home | `src/app/(tabs)/index.tsx` | `components/purchase-row.tsx`, `status-badge.tsx` |
| Pembelian (list) | `src/app/(tabs)/purchases.tsx` | `status-badge.tsx`, `app-text-input.tsx` |
| Tambah Pembelian | `src/app/purchase/new.tsx` | `components/purchase-form.tsx`, `lib/receipt.ts` |
| Detail / Edit | `src/app/purchase/[id].tsx` | `purchase-form.tsx`, `receipt-section.tsx`, `lib/warranty.ts` |
| Pengingat | `src/app/(tabs)/reminders.tsx` | `lib/push.ts` |
| Pengaturan / Akun | `src/app/(tabs)/explore.tsx` | |
| Tab bar | `src/components/app-tabs.tsx` | |
| Tokens, fonts, buttons | `constants/theme.ts`, `themed-text.tsx`, `app-button.tsx` | see `theme-tokens.md` |

## Done

| Id | Item |
|---|---|
| D-01 | Colors, dark palette, Plus Jakarta Sans, buttons, inputs, card borders, tab icons |
| D-02 | Status pill (soft background, dark text), light and dark |
| D-03 | Tab bar and purchase screens exist only after login |
| D-04 | Receipt picker in the Add form (two steps; a failed upload opens the new purchase) |
| D-05 | "Lanjutkan dengan Google" on the login screen (`kc_idp_hint=google`, also registers new people); login errors show the server message; closing the browser is not an error |

## To do (differences from the prototype)

| Id | Screen | Change | Size | Status |
|---|---|---|---|---|
| U-01 | Detail | Coloured banner by status: "Garansi masih aktif" / "segera berakhir" / "Masa garansi telah berakhir" / "Tanggal garansi belum diketahui" + "Tanggal akhir: <date>" (tints in `theme-tokens.md`) | S | done |
| U-02 | Detail | Rows: Tanggal pembelian, Durasi garansi, Harga, Tanggal akhir garansi in a 2-column grid; receipt as a card with "Lihat" / "Tambah" | M | done |
| U-03 | All | Dates as `15 Okt 2025`, money as `Rp14.999.000` (`lib/warranty.ts`: `formatDate`, `formatIDR`) | S | done |
| U-04 | Pengaturan | Rows: "Preferensi pengingat" (button "Atur" opens the Pengingat tab), "Privasi & keamanan" (lock icon, opens a text screen), "Ekspor data" (the export row comes with U-14; the privacy text expands in place instead of a separate screen) | M | done (without the export row) |
| U-05 | Pengaturan | Remove the "Server: ..." line (debug text) | XS | done |
| U-06 | Home | Subtitle "Semua bukti pembelian dan garansi dalam satu tempat."; add button beside the heading; "Lihat semua" small button beside "Pembelian terbaru"; green / amber tinted stat cards | S | done |
| U-07 | Pembelian | Heading "Pembelian Saya" + subtitle "Cari produk dan cek status garansinya."; "Tambah" button at the top | S | done |
| U-08 | Row card | Status icon in the pill (✓ ◷ ! •), chevron on the right, product icon on the left (the product icon is not done: see X-01) | S | done (without the product icon) |
| U-09 | Add/Edit | Default duration 12 months (prototype); date picker instead of typing `YYYY-MM-DD`; labels "Toko / Store", "Harga (opsional)"; subtitle "Isi informasi dasar. Struk dapat ditambahkan sekarang atau nanti."; "Batal" button; title "Edit Pembelian" when editing | M | done |
| U-10 | Feedback | Short toast after save / update / delete ("Pembelian berhasil disimpan." / "diperbarui." / "dihapus.") | S | done |
| U-11 | Empty states | Title + one line of help, as in the prototype ("Belum ada pembelian" / "Tambahkan pembelian pertama Anda.") | S | done |
| U-12 | Pengingat | One line of help under each day switch; keep the notification inbox and also show the "needs attention" purchases (expiring / expired) as the prototype does | M | todo |
| U-13 | Akun | Account screen: e-mail, name, "Member since" (`createdAt`), export, delete, sign out (see `02-screens-and-api-map.md`) | M | todo |
| U-14 | Ekspor data | `GET /account/export` returns JSON; offer share / save (CSV only if the product owner wants it, convert in the app) | M | todo |
| U-15 | Privasi | Link to the privacy policy (Play Store / App Store need it); text not written yet | S | decision |
| U-16 | Repo docs | `KNOWN-ISSUES.md` (receipt link problem is fixed on the server) and the template `README.md` | XS | todo |

## Decisions needed

| Id | Question |
|---|---|
| X-01 | Product photo (poster shows one on the list and the form; the prototype uses emoji). The API has one receipt per purchase and no product photo. Options: emoji / category icon, or a backend change (count, formats, thumbnails). |
| X-02 | Poster extras not in the prototype or the API doc: bell icon on Home, avatar on Settings, "Help & Support", "About ReturnIt". Keep or drop? |
| X-03 | Export format: JSON as the API gives, or CSV as the prototype shows. |
| X-04 | Recent purchases on Home: prototype shows 3, the API doc says 5 (the app uses 5). |

## On purpose different (skip)

"Reset data demo", the "Demo data only" banner, demo data, and the side menu (the app uses a bottom tab bar).

## Suggested order

U-04, U-05, U-01, U-02, U-03 (Settings and Detail) then U-06, U-07, U-08, U-09, U-10, U-11, U-12, then U-13, U-14, U-15, U-16.
Each item is independent; run `npx tsc --noEmit` before finishing (the 6 errors in the web files are old).
