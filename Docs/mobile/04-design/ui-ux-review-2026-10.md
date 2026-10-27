# ReturnIt mobile: UI/UX review and Home proposals

Date: 26 October 2026. App version reviewed: **1.3.2** (branch `release/release`). Method: reading every screen and shared
component in `src/app` and `src/components`, the tokens in `src/constants/theme.ts`, contrast calculations (WCAG 2.x), and the
UI UX Pro Max guidelines (UX, React Native stack, icons, colour). **Not done:** a visual pass on a phone or emulator;
findings about density and feel need a screenshot check. Related: `theme-tokens.md`, `implementation-todo.md`.

Product position stays as decided on 25 October 2026: a **warranty tracker**; receipts are supporting proof.

## 1. What already works

- Light and dark token sets; status text colours are darker variants so small text stays readable.
- Warranty status never relies on colour alone (mark plus text).
- Form labels are always visible; delete asks for confirmation; toasts dismiss by themselves (2.8 s).
- Native tab bar with an unread badge; right-to-left support is clean (`borderStart`, mirrored chevron key).

## 2. Bug found during the review

| Where | Problem | Fix |
|---|---|---|
| `src/app/(tabs)/index.tsx:78` | `split(/s+/)` should be `split(/\s+/)`: the backslash was lost in an earlier patch, so the greeting cuts a name at the letter "s" ("Siska" becomes "Si", "Hasan" becomes "Ha") | One character |

## 3. Findings

### P0: fix first

| # | Finding | Evidence | Fix |
|---|---|---|---|
| 1 | Emoji and text glyphs used as icons | ⚙️ Reminders header, 🔒 and ▲ Settings, ✓ ◷ ! • in `status-badge.tsx`, ▤ in `receipt-section.tsx`, "›" chevrons | One vector icon family (for example `@expo/vector-icons`, check it is installed), sizes as tokens (20, 24) |
| 2 | Touch targets below 44pt (iOS) / 48dp (Android) | `AppButton small` (padding 8, line 18) is about 36; filter chips and `ChoiceChip` about 36 | `minHeight` 44, or `hitSlop` |
| 3 | Secondary text contrast under 4.5:1 on cards | `#64748B` on `#EFF4FA` = 4.30:1 (on the page background 4.55:1) | Darken light `textSecondary` to about `#526071` (about 5.5:1) |
| 4 | Screen readers miss card content | `PurchaseRow` label is only "Buka {name}"; reminder rows, chips and `SettingRow` have no role or state | Label with the summary ("Laptop X, garansi segera berakhir, 12 hari lagi"); `accessibilityRole` and `accessibilityState={{selected}}` on chips |
| 5 | No pressed feedback | Cards, reminder rows and chips are `Pressable` without a pressed style or ripple | Opacity or background change on press, `android_ripple` |

### P1: quality and clarity

| # | Finding | Fix |
|---|---|---|
| 6 | Home has no single main point (full-width "Add" first, three equal counters, a text-only highlights card) | See section 5: a "needs attention" hero; "Add" becomes a header or floating "+" |
| 7 | Purchase card is dense: six lines of equal weight | Name and store left; days left large on the right in the status colour; price secondary; return deadline only as a small chip when near |
| 8 | Detail screen is long and button-heavy: two full-width calendar buttons, four receipt buttons, Edit and Delete | Calendar as a small icon inside each banner; receipt actions in one "⋯" menu; Edit and Delete in the header menu; edit opens as its own screen or modal instead of replacing the content in place |
| 9 | Form: one long scroll, Save at the very end, errors only after Save, focus does not move to the first error | Required fields first (name, date, duration); price, return period, notes and receipt under "More options"; sticky Save; focus the first field with an error |
| 10 | Reminders: `toLocaleString()` ignores the app language; "Remind me again" inside the card competes with the card tap; no grouping | Relative time in the app language; groups Today / This week / Earlier; "Remind me again" as a small text link |
| 11 | Settings: six rows with mixed controls (button, switch, chevron, emoji), no section titles, five-paragraph privacy accordion, 24 gutter vs 16 elsewhere | Sections: Account, Preferences (reminders, language, app lock), Data and privacy (export, policy); chevron or switch only on the right; Sign out set apart; gutter 16 |
| 12 | Empty and loading states are weak: `EmptyState` is text only, loading is a spinner | Illustration, one benefit sentence and an "Add first purchase" button; skeleton rows on Home and lists |

### P2: polish

- **Consistency.** `Spacing` tokens exist but screens use 10, 12, 14, 16; radii 12, 14, 16; font sizes 13, 14, 16, 18, 20, 22, 24, 28. Use a fixed scale (12, 14, 16, 20, 28) and spacing 4/8/16/24.
- **Cards blend into the background** in light mode: `#EFF4FA` on `#F8FAFC` is 1.06:1, divider 1.18:1. Add a thin border or soft shadow.
- **Toast** `#17324D` on dark background `#0B1220` is barely separated: add a border or a lighter fill. On iOS `accessibilityLiveRegion` does nothing; call `AccessibilityInfo.announceForAccessibility`.
- **Large system font:** fixed `lineHeight` (24, 30, 34) can clip text; test at 130% and 200%.
- **Login:** logo, one sentence and three buttons. Add three short benefit lines; make "Sign in" clearly primary.
- **Wide screens:** `MaxContentWidth` (800) exists but is not used by Home, Purchases, Reminders or Settings.
- **Purchases list:** add sort "warranty ends soonest" and counts on filter chips.
- **Haptics** (light) on save and delete; respect reduce motion if animation is added.

Colour checks (WCAG contrast): white on primary `#2563EB` 5.17; `textSecondary` dark `#94A3B8` on `#0B1220` 7.30; `warningText` on card 4.54;
success text on active tint 4.57; primary text on page 4.94; dark primary on dark page 7.36; white on toast navy 13.13.

## 4. Suggested design direction

Calm utility with one focus per screen, soft cards, coloured status. Keep the brand (blue and navy, Plus Jakarta Sans); the skill's
generic design-system output (landing-page pattern, green organic style) did not fit an app and was not adopted.

```
HOME                                   PURCHASE CARD
+---------------------------+          +------------------------------+
| Halo, Azzam           [+] |          | [icon] Laptop ASUS     12    |
| +-----------------------+ |          |        Erafone . 3 Mei days  |
| | Ending soon           | |          |        (o) Ending soon       |
| | Laptop ASUS           | |          +------------------------------+
| |   12 days left        | |          left: icon, name, store.date, badge
| | [Open]  [Calendar]    | |          right: days left, large, status colour
| +-----------------------+ |
| Active 8 . Soon 2 . 12    |          DETAIL
| Latest             All    |          +---------------------------+
| [card][card][card]        |          | <- Laptop ASUS        ... |
+---------------------------+          | Erafone                   |
                                       | [Warranty: 12 days left]  |
                                       | Bought | Duration | Price |
                                       | Receipt (preview)     ... |
                                       | Notes                     |
                                       +---------------------------+
                                       (menu ... = Edit, Delete)
```

Principles: one primary action per screen; status on the right with a large number; secondary actions in a menu or as icons;
one consistent vector icon family.

## 5. Modern layout system

Basis: the skill's style data for "Bento Box Grid" (modular cards of varied size, 16-24 px radius, soft shadows, generous space),
"Minimalism" (clear hierarchy, few elements) and "Glassmorphism" (used sparingly). Searches for "collapsing large title" and for
home-screen app patterns returned no match in the skill; those two points below are general practice, not database results.

| Element | Today | Modern version |
|---|---|---|
| Page structure | Title 28 + subtitle, then a stack of full-width blocks | Large title that shrinks on scroll (native header with `headerLargeTitle`), content in a **bento grid**: one wide hero tile, then 2-column tiles |
| Cards | Flat fill, radius 12-16, almost no edge | Radius 20, white (dark: `#131E33`) with a 1 px border and a very soft shadow (`0 4 12 rgba(23,50,77,0.06)`); tiles of different sizes |
| Numbers | Small counters (28 pt) in equal boxes | One big number per tile (36-44 pt, bold), small label under it, status colour only on the number or a thin ring |
| Status | Pill with a glyph | Pill with a vector icon, plus a left colour edge or a ring on the card; days left as the largest element on the card |
| Actions | Full-width buttons everywhere | One floating "+" (bottom right, above the tab bar); everything else as icon buttons or text links; "⋯" menus; swipe on a list row for Edit / Delete with undo |
| Filters | Row of chips | Segmented control for the main split (All / Active / Ending soon / Expired), chips only for extras |
| Sheets | Full-width buttons in a plain modal | Bottom sheet with a drag handle, rounded top 24, large tap rows with an icon on the left |
| Tab bar | Native tabs | Keep native tabs (they already adopt the platform look, including the floating bar on newer iOS); use a frosted look only on the floating "+" and sheet handles, never behind body text |
| Loading | Spinner | Skeleton tiles with the same shape as the real ones; short fade-in (150-250 ms); respect reduce motion |
| Feedback | Toast only | Toast with an icon and an "Undo" action; light haptic on save and delete |

Home as a bento grid (portrait phone, 16 px gutters, 12 px gaps):

```
+------------------------------------------+
| Home                         (search) (bell)
| Good morning, Azzam                      |
|                                          |
| +--------------------------------------+ |
| | NEEDS ATTENTION                      | |
| | Laptop ASUS                          | |
| | 12 days left        [ring 12/365]    | |
| | [Open]  [Add to calendar]            | |
| +--------------------------------------+ |
| +------------------+ +-----------------+ |
| | 8                | | 2               | |
| | under warranty   | | ending soon     | |
| +------------------+ +-----------------+ |
| +------------------+ +-----------------+ |
| | Rp 24.500.000    | | Return by       | |
| | still covered    | | 3 Nov . Shoes   | |
| +------------------+ +-----------------+ |
| +--------------------------------------+ |
| | 3 purchases have no duration  [Fix]  | |
| +--------------------------------------+ |
| Latest purchases                    All  |
| [card] [card] [card]                     |
|                                    (+)   |
| Home   Purchases   Reminders   Settings  |
+------------------------------------------+
```

Rules for the grid: a hero tile is full width; small tiles are exactly half width (never three across, which is what makes the
present counters cramped); at most five tiles above the list; a tile that has no data is hidden, not shown empty; on wide
screens limit the content to `MaxContentWidth` and go to three columns.

Implementation notes (React Native): tiles are one shared `Tile` component (radius, border, shadow, padding as tokens); shadow on
Android via `elevation` plus the border; icons from one vector family; a `Skeleton` component built on `Animated` opacity; the
floating "+" respects the bottom inset (`BottomTabInset`) so it never sits under the tab bar.

## 6. Home: feature proposals

Everything stays inside the warranty-tracker position. "Backend" says what the server would need.

| # | Proposal | What the person gets | Data / backend | Effort |
|---|---|---|---|---|
| H-1 | **Needs-attention hero** | The one warranty or return deadline that comes first, with a large countdown, "Open" and "Add to calendar". Replaces the plain highlights card. | `GET /summary` already returns `nextExpiring` and `nextReturn`; none | S |
| H-2 | **Quick add: camera first** | A floating "+" with two choices: "Photograph receipt" and "Enter by hand". The photo starts the form with the receipt attached. | None (the form already takes a receipt) | S |
| H-3 | **Unread reminder strip** | "2 new reminders" with the latest title, one tap to the Reminders tab. | `GET /notifications` (already used for the badge) | S |
| H-4 | **"Check before it ends"** | For items ending within 30 days: "Anything wrong with it? Claim now". Opens a claim checklist: receipt, serial number, store, what to say. | None for the checklist; a `serialNumber` field later | M |
| H-5 | **Next 90 days timeline** | A horizontal strip of upcoming warranty ends and return deadlines, most urgent first; tap to open. | A list sorted by end date (`sort=warrantyEnd`) or a `GET /summary` addition | M |
| H-6 | **Complete your records** | "3 purchases have no warranty duration. Add them." Raises data quality, which makes the reminders useful. | A count of unknown-duration purchases in `GET /summary` | S |
| H-7 | **Coverage ring** | A ring "8 of 12 under warranty" plus the value still covered, replacing three equal counters. | Counts and `coveredValue` already exist | S |
| H-8 | **Category icons** | Each card shows a phone, laptop, appliance or other icon, which makes the list faster to scan. | A `category` field on purchases (migration, API, OpenAPI, form chip) | M |

**Recommended first three:** H-1 (the purpose of the app in one card), H-2 (fastest way to add, the main friction today) and
H-7 (clear at a glance). Then H-6 and H-4 as differentiators; H-5 and H-8 need backend work.

Not recommended now: streaks, badges or savings counters (they distract from the warranty purpose), and a Home search bar
(the Purchases tab already has search).

## 7. Order of work

1. **Quick (about half a day):** the `\s` bug, `textSecondary` contrast, minimum touch size, pressed feedback, accessibility labels, vector icons instead of emoji.
2. **Medium (1-2 days):** new purchase card, Home hero (H-1) and quick add (H-2), "+" in the header, grouping in Reminders and Settings, empty states with an action.
3. **Larger (2-3 days):** "⋯" menu on the detail screen, form with "More options" and sticky Save, skeletons, login refinement, then the backend-dependent Home items (H-5, H-6, H-8).

Check before delivery (from the skill): 375px and landscape; reduce motion; largest system text; light and dark checked separately;
touch targets 44pt; nothing hidden behind safe areas or the tab bar.
