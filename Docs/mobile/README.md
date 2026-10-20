# ReturnIt: documents for the mobile app developer

Everything you need to build the mobile app, in reading order. You do not need the rest of the repository.

| # | File | What it is | Read it when |
|---|---|---|---|
| 1 | `01-mobile-app-guide.md` | How to get a backend to develop against, how login works (OIDC + PKCE), the rules the app must follow, push notifications, account deletion | **First** |
| 2 | `02-screens-and-api-map.md` | Every screen of the design and the API calls behind it, plus the surprises | While building each screen |
| 3 | `03-openapi.yaml` | The exact API: every endpoint, field, status code. Import it into Postman/Bruno or a code generator | Always at hand |
| 4 | `04-design/ReturnIt_Paket_UIUX_Visual.docx` | Design package: user flow, sitemap, wireframes, mock-ups, design system (colours, type, components) | Before designing screens |
| 4 | `04-design/ReturnIt_Interactive_Prototype.html` | Clickable prototype with demo data. Open in a browser | To see the intended behaviour |
| 5 | `05-requirements/ReturnIt_SRS_Requirements_UseCases_BPMN.docx` | Requirements, user stories with acceptance criteria, use cases | To check what "done" means |
| 6 | `06-data-and-privacy/data-retention-policy.md` | What the backend keeps and deletes, and when (useful for privacy texts and store forms) | Before writing privacy screens |
| 7 | `07-qa/qa-test-plan.md` (English), `07-qa/qa-test-plan.id.md` (Indonesian) | Manual test plan: environments, test accounts, smoke test, about 100 cases (login, purchases, receipts, push, account deletion, errors), bug template, release checklist | Before every release |

The design and requirement documents are in **Indonesian**; the guide, the map and the API file are in English.

## Quick facts

* **API:** JSON over HTTPS, base path `/api/v1`. Dev: `http://localhost:8080/api/v1`. Real server: `https://api.<domain>/api/v1`.
* **Login:** OIDC authorization code + PKCE in the system browser. Client id `returnit-app`, audience `returnit-api`,
  issuer `https://auth.<domain>/realms/returnit` (dev: `http://localhost:8180/realms/returnit`). Test users in dev: `alice` / `alice-dev-pass`.
* **Push:** Firebase Cloud Messaging. The app registers its token with `POST /devices` and creates an Android
  notification channel named `reminders`.
* **Receipts:** PDF, JPEG or PNG up to 10 MiB, uploaded as multipart (`file`), viewed through short-lived signed links.
* **Reminders:** 30/14/7/3/1 days before the warranty ends, 09:00 in the user's time zone (default WIB).
* **Account deletion:** required in-app; erasure happens 7 days after the request and needs a fresh login.

## What the backend owner must give you

1. The login server address and API address for each environment (staging, production).
2. The agreed **redirect URI** of the app (default `com.returnit.app:/oauth2redirect`; it must match exactly on both sides).
3. Access to the Firebase project (or its `google-services.json` / `GoogleService-Info.plist`) for push.
4. A staging backend with real HTTPS, so the app needs no special network settings.

## Decide together before you start

* The app's package name / bundle id (cannot change after publishing).
* Whether the iPhone app will offer "Sign in with Google" (Apple then expects an equivalent such as "Sign in with Apple").
* Whether data export should be offered as JSON (what the API returns) or converted to CSV like the prototype.

## About these files

`03-openapi.yaml`, the design/requirement files and the retention policy are **copies**. The originals stay where the
backend team edits them (`Docs/api/openapi.yaml`, `Docs/*.docx`, `Docs/docs/data-retention-policy.md`). After the
originals change, refresh the copies with `scripts/sync-mobile-docs.sh` (backend team) before handing the folder over.
`01-mobile-app-guide.md`, `02-screens-and-api-map.md` and `07-qa/qa-test-plan.md` (and its Indonesian twin `qa-test-plan.id.md`) live only here: edit them in this folder and keep the two QA files in step.

Not included on purpose (backend internals): architecture, database design, sprint backlog, coding standards, deployment.
