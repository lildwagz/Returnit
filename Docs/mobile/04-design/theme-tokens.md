# ReturnIt theme tokens

Source: `ReturnIt_Paket_UIUX_Visual.docx` (section 6, Design System) and `ReturnIt_Interactive_Prototype.html`.
The mobile app implements them in `src/constants/theme.ts` (`Brand`, `StatusStyle`, `Colors`).

## Colors

| Token | Value | Use |
|---|---|---|
| `Brand.primary` | `#2563EB` | primary buttons, selected chips, links, accents |
| `Brand.primaryDark` | `#1D4ED8` | pressed state, text on `primarySoft` |
| `Brand.primarySoft` | `#DBEAFE` | selected chip background, focus ring |
| `Brand.navy` | `#17324D` | body text (light theme), toast background |
| `Brand.secondary` | `#64748B` | secondary text, placeholders, inactive icons |
| `Brand.line` | `#E2E8F0` | borders, dividers |
| `Brand.success` / `successText` | `#2CC35E` / `#15803D` | fill / text |
| `Brand.warning` / `warningText` | `#F59E0B` / `#B45309` | fill / text |
| `Brand.error` / `errorText` | `#EF4444` / `#B91C1C` | fill / text |

Light theme: background `#F8FAFC`, cards `#EFF4FA`. The brand fill colors on a white
background are too light for small text (for example `#EF4444` is 3.8:1), so text and
white-on-color labels use the `*Text` variants (4.5:1 or better). Use the plain fill colors only for
icons, bars and large shapes.

## Warranty status pill (`StatusBadge`)

Soft background, dark text, fully rounded. Same component in the list, the home screen and the detail screen.

| Status | Background | Text |
|---|---|---|
| active | `#DCFCE7` | `#166534` |
| expiring | `#FEF3C7` | `#92400E` |
| expired | `#FEE2E2` | `#991B1B` |
| unknown | `#F1F5F9` | `#475569` |

## Shape and spacing (from the prototype)

- Buttons and inputs: radius 12, padding 12 x 16. Cards: radius 16. Pills and chips: fully rounded.
- Primary button: `primary` fill, white text, weight 700. Secondary: white fill, `line` border, `navy` text. Destructive: `#FFF1F2` fill, `#BE123C` text, `#FECDD3` border.
- Input focus: border `#93C5FD` and a 3 px `primarySoft` ring.
- Summary cards on Home: green tint (`#F0FDF4`, border `#BBF7D0`) for active, amber tint (`#FFFBEB`, border `#FDE68A`) for expiring.

## Implemented in the app

- Font: **Plus Jakarta Sans** (Regular, Medium, Bold), loaded in `src/app/_layout.tsx`; `ThemedText` picks the family from the weight (600 and up = Bold). Headings are 28 px (`title`) and 22 px (`subtitle`).
- Buttons: `src/components/app-button.tsx` (`primary`, `secondary`, `danger`) replaces React Native's `Button` on every screen.
- Status pill: `src/components/status-badge.tsx`.
- Splash color: `#2563EB` in `app.json` and `animated-icon.tsx` (needs a new native build to show).
- Login: before login only the login screen exists; the tab bar and purchase screens are protected routes (`Stack.Protected` in `src/app/_layout.tsx`).

- Dark theme (not in the design, derived from it): background `#0B1220`, cards `#131E33`, text `#F1F5F9`, secondary `#94A3B8`, border `#263552`; text colors are lighter so they stay readable (primary `#60A5FA`, error `#F87171`, success `#4ADE80`, warning `#FBBF24`). Status pills have a dark set (`StatusStyleDark`). Screens read these through `useTheme()` and `themeColor="errorText"` style props, never hard-coded colors.
- Text fields: `src/components/app-text-input.tsx` (font, 1 px border, radius 12). Cards (`ThemedView type="backgroundElement"`) have a 1 px border.
- Tab icons: SF Symbols on iOS (`house`, `receipt`, `bell`, `gearshape`) and Material icons on Android (`home`, `receipt_long`, `notifications`, `settings`); the template images are no longer used by the tab bar. The selected tab uses the primary color.

## Not implemented yet

- Nothing from the design package is left open except what the package itself does not specify (spacing scale, animations). Everything above is untested on a device.
