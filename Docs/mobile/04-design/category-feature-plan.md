# Plan: purchase categories (not started)

Status: **built locally, not released** (30 October 2026): backend migration 12, API, tests and docs done; app screens done and bundling; not yet run on a phone or emulator, not committed, not deployed. Planned for app version **1.5.0**. Replaces item H-8
("category icons") in `ui-ux-review-2026-10.md`. Product position unchanged: a warranty tracker; the category helps find and
fill in warranties faster.

## 1. Goal

- Scan the list faster (an icon per purchase).
- Filter the list by category.
- Suggest the usual warranty duration when a category is chosen, so adding a purchase is quicker.

## 2. Categories (proposed, to be confirmed)

A fixed list, not user-defined: it keeps filters, icons and translations consistent. The server stores only the key; the app
shows the name in Indonesian, English and Arabic.

| Key | Examples | Icon (Feather) | Suggested duration |
|---|---|---|---|
| `phone` | phone, tablet | smartphone | 12 months |
| `computer` | laptop, PC, monitor | monitor | 12 months |
| `tv_audio` | TV, speaker, headphones | tv | 12 months |
| `camera` | camera, drone | camera | 12 months |
| `appliance` | fridge, washing machine, AC | home | 24 months |
| `kitchen` | air fryer, blender | coffee | 12 months |
| `vehicle` | motorbike, tyre, battery | truck | to decide |
| `tools` | drill, equipment | tool | 12 months |
| `furniture` | bed, sofa | box | 12 months |
| `wearable` | watch, jewellery | watch | 12 months |
| `other` | anything else | package | none |

Durations are suggestions only (the person can change them) and must be reviewed before release.

## 3. Backend

- Migration 12: nullable `category` text column on `purchases` with a `CHECK` on the list of keys. Existing rows stay empty and show as "no category".
- Create, update and read carry `category`. `GET /purchases` accepts `category=<key>`.
- **Old apps must not wipe it.** The edit form sends the whole record. On update, a `category` that is *absent* in the request means "keep it"; an explicit `null` clears it. Raise the minimum app version to 1.5.0 as a second safeguard.
- OpenAPI (`Docs/api/openapi.yaml` and its copy `Docs/mobile/03-openapi.yaml`), unit and integration tests, SRS change log (next CR number), QA cases, API documentation.
- Deploy the backend before the app is published.

## 4. App

1. **Form:** a horizontally scrollable row of icons right under the name (not under "More options"), optional. Choosing one can pre-select the suggested duration (only when the person has not chosen a duration yet).
2. **Purchase card and detail:** small category icon on the card; the category name on the detail screen.
3. **Purchases list:** a category filter (chips or a "Category" sheet, whichever fits a 375 px screen with Arabic labels), combined with the existing status filter.
4. **Texts:** all new strings in id/en/ar; icons from the existing `Icon` component.
5. **Home:** unchanged for now (counts per category can come later).

## 5. Before release

- Run the app on the Android emulator (`returnit_test`, already created) and open every new screen before building; check `adb logcat` for crashes. Do not use native values (roles, enums) that were not checked against the platform.
- Check on a phone: large system font, light and dark, Arabic (right to left), 375 px width.
- Release order: backend deploy, then publish the app as 1.5.0 with the minimum raised to 1.5.0.

## 6. Decisions (taken with the recommended defaults, change if you disagree)

1. The list of 11 categories as in section 2. Suggested warranty: 12 months, 24 for home appliances, none for vehicle and other.
2. The duration is suggested automatically on a new purchase, only while the person has not chosen a duration, and can be changed.
3. Optional.

Estimate: backend half to one day, app about one day, documents and tests about half a day.
