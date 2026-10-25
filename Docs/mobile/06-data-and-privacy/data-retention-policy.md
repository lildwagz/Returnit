# ReturnIt — Receipt File Retention & Malware Scanning Policy
Version 1.0 | Status: **Draft — values marked ⚠ need product-owner / legal confirmation before public release**

This document describes what the backend enforces today and the decisions still open. It is an engineering baseline, not legal advice; confirm obligations (e.g. Indonesia's PDP Law, Law No. 27/2022) with qualified counsel.

## 1. Malware scanning (implemented)
| Rule | Behaviour |
|---|---|
| When | Every receipt upload is scanned by ClamAV (`clamd` INSTREAM) **before** it is written to object storage. |
| Fail closed | Scanner unreachable, timed out, or returned an error → upload rejected with `503 scan_unavailable` (`Retry-After: 30`). A file that was not verified clean is never stored. |
| Infected | `422 malware_detected`. Nothing is stored; no metadata row is created. The server logs the signature name only (no file content, no filename). |
| Order of checks | Ownership → size cap → content-type sniff (PDF/JPEG/PNG) → malware scan → store → commit metadata. |
| Dev switch | `MALWARE_SCAN=off` disables scanning and logs a warning at startup. Startup refuses to run with scanning on but `CLAMAV_ADDR` unset. Never use `off` in production. |
| Signatures | The ClamAV container updates signatures itself (`freshclam`). Monitor that the scanner is healthy and signatures are fresh. |

Known limits: files already stored are not re-scanned when signatures update (see §5), and scanning cannot catch every threat — the other controls (type allow-list, private bucket, `attachment` download disposition, short-lived URLs) remain required.

## 2. Retention rules for receipt files
| Situation | Rule | Enforced by |
|---|---|---|
| Active receipt | Kept until the user deletes it, replaces it, deletes the purchase, or deletes the account. There is no automatic expiry while the account is active. ⚠ | — |
| User deletes receipt / purchase | The web process never deletes the file itself. A DB trigger queues the key (`object_deletions`), web nudges the worker through Redis, and the worker deletes it, normally within seconds. If Redis is down the worker's periodic purge (every 30 s) does it; if storage is down it retries with exponential backoff (1 min doubling, capped at 1 h) until it succeeds. | DB trigger → `object_deletions` → `cmd/worker` (`retention.Worker`) |
| User replaces receipt | Previous file queued and removed the same way. | trigger on `receipts.storage_key` update |
| Account deletion | `DELETE /account` only *schedules* erasure after a **grace period (default 7 days, `ACCOUNT_DELETION_GRACE`, 1 h to 90 days)**. The user can cancel until then (`POST /account/deletion/cancel`); repeated requests keep the original date. During the grace period reminders stop and the rest of the API still works. When it ends, the worker (checks every 15 min) hard-deletes the user; the cascade removes purchases, receipts, reminder preferences and notifications, a trigger queues every receipt file, and the worker deletes them immediately after. | `account.Service` → worker `account:purge` → FK cascade + trigger |
| Deletion notice e-mails | Three plain-text e-mails: when deletion is requested, 24 h before erasure (only if the grace period is longer than 25 h) and after erasure. They are queued in the same transaction as the action (`account_emails`). The row holds the e-mail address only until the message is sent, then it is deleted; unsent rows are dropped when the user cancels, and abandoned after **7 days** of failed attempts. The after-erasure message is the only thing that outlives the user row, and it is sent from a copy of the address kept just until then. E-mail text and logs never contain the address. | `account.EmailSender` → worker `account:emails` (every minute) |
| Login identity at the identity provider | When an account is erased, its identity (Keycloak user) is deleted too, by the worker (`account:identities`, every minute) through the provider's admin API. The subject is queued in `identity_deletions` in the same transaction as the erasure and removed from our database once the provider confirms (an already-missing identity counts as deleted). Failures retry with backoff; after **30 days** the row is dropped and an ERROR is logged so an operator deletes the user by hand. Not done during the grace period: the identity stays so the user can still log in and cancel. | `account.IdentityEraser` → `identity.Keycloak` |
| Push tokens (`devices`) | One row per registered phone: platform, push token and two dates. Deleted when the user signs out in the app, when Firebase reports the token dead, when the same phone registers for another user (the token moves), when a user has more than 10 phones (oldest dropped), and with the account at erasure (cascade). Tokens are never returned by the API, written to logs or included in the data export (which lists platform and dates only). | `devices` table, `ON DELETE CASCADE` |
| Deletion log | Erasure writes the internal user UUID and the request/deletion timestamps to `account_deletion_log` (no e-mail, name or identity subject). It exists so deletions can be re-applied after a backup restore. Retention of the log itself: ⚠ to be decided (suggest keeping it at least as long as backups). | `account_deletion_log` |
| Return deadline, snoozed reminders and the display name | `purchases.return_deadline` is a plain date that goes with the purchase. A reminder about it is an ordinary notification (kind `return`); "remind me again" adds one more notification row (its idempotency key starts with `snooze:`) and marks the original read. All of them are removed with the purchase or the account by the same cascade. The display name is copied from the login token at **every** sign-in (so a changed name follows); it is erased with the account and never written to logs or e-mail text. | `purchases`, `notifications` (FK cascade), `users.display_name` |
| Orphaned object (in storage, no DB row, not queued — e.g. crash between upload and commit) | Removed by the worker's hourly sweep once older than the grace period (default **24 h**, `RETENTION_ORPHAN_GRACE`, minimum 1 h). The grace period protects uploads still in flight. | `retention.Worker.SweepOrphans` |
| Failed deletions | Stay queued with `attempts` / `last_error` for operators. ⚠ Add an alert when `attempts` exceeds a threshold (e.g. 10). | `object_deletions` table |

The queue is a transactional outbox: the key is recorded in the **same transaction** that removes the database row, so a deleted user's file cannot be forgotten by a crash.

## 3. Backups, logs and derived data — decisions needed
These are **not enforced by code** and must be decided and operated by the team.
| Item | Proposed baseline | Status |
|---|---|---|
| Database backups | Encrypted; retain 35 days, then expire. Deleted accounts age out of backups within that window. After any restore, delete every user listed in `account_deletion_log` again and run the file purge and orphan sweep, so erased people do not reappear. | ⚠ Proposed |
| Object-storage versioning | **Disabled** (versioning would keep "deleted" receipts). If enabled later, add a lifecycle rule that purges noncurrent versions within 7 days. | Current: disabled |
| Application logs | Request ID, status, latency only; no tokens, filenames, receipt content or signed URLs. Retain 30 days. Docker keeps at most 5 x 10 MB per service, so in practice only a few days. **Exception, off by default:** with `LOG_REQUEST_BODY=on` the log also holds the endpoint path and the JSON request and response bodies (product names, shops, notes, prices; tokens, passwords, receipt links and file names replaced; uploads and the data export never; each cut to 2 KB). Use it only in development or for a short debugging session on production, never leave it on, and treat such logs as personal data (same access rules and deletion on request). The server logs a warning at start-up while it is on. Two further debugging switches, also off by default: `LOG_SQL=on` (every SQL statement with its text, the number of arguments, duration and result; **argument values are never logged**) and `LOG_REDIS=on` (each job written to Redis for the worker with its type, queue and id payload, each job run, and the rate limiter's Redis command names; keys and arguments are not logged). | ⚠ Proposed |
| Log on the phone (mobile app) | The app writes one line per request to the system console (method, path, status, time; sign-in and sign-out events) and keeps the last 300 lines in memory. Access tokens, passwords, push tokens and receipt links are never written. Request and response bodies (personal data) are written **only** in a development build or one made with `EXPO_PUBLIC_LOG_BODY=true`, with secrets replaced and cut to 2000 characters; a build handed to users must not have it. The in-memory lines are lost when the app closes and are cleared at sign-out. The app has no way to share or upload the log. | Implemented (`src/lib/log.ts`) |
| Notifications (when built) | Delete read notifications after 90 days. | ⚠ Proposed |
| Legal / dispute hold | None defined. If one is needed, it must suspend the worker for the affected user. | ⚠ Open |
| Data export before deletion | `GET /account/export` (FR-16): JSON of all data + receipt metadata; files fetched separately. Bundled file export not built. | Partial |

### Backups as built (`deploy/backup.sh`, `deploy/restore.sh`)
Nightly encrypted dumps of the app and login databases, kept `BACKUP_KEEP_DAYS` (default **35**, ⚠ owner/legal
to confirm), plus a mirror of the receipt files that follows deletions. The list of erased account ids
(`deletion-log-*.csv`: ids and dates only) is kept longer than the dumps. A restore re-erases every account on
that list (and those found in the live database) before the app starts again; this was rehearsed.

## 4. Operations checklist
- Alert if the ClamAV health check fails, or on a spike of `scan_unavailable` / `malware_detected`.
- Alert if `SELECT count(*) FROM object_deletions WHERE attempts >= 10` is non-zero, or the oldest row is older than 1 h. The worker exports these as `returnit_object_deletions_failing` and `returnit_object_deletions_oldest_age_seconds` on `/metrics` (see README, "Health and metrics"); you still need a monitoring system to scrape and alert on them.
- Alert on `returnit_account_emails_failing > 0` or `returnit_account_emails_oldest_due_seconds > 900` (deletion notices are not going out: check SMTP).
- Alert on `returnit_identity_deletions_failing > 0` (erased users are still present at the identity provider: check the worker's Keycloak credentials).
- Alert on `returnit_accounts_overdue_erasure > 0` (an account is more than an hour past its erasure date: the worker's `account:purge` job is failing or stopped).
- After restoring a database backup, run the purge and orphan sweep so restored rows do not resurrect deleted users' files, and confirm deleted accounts stay deleted (keep a deletion log, without PII, to re-apply).
- Review `RETENTION_ORPHAN_GRACE` if uploads can legitimately take longer than the default 24 h (they should not).

## 5. Future work
- Re-scan stored receipts periodically or when signatures update (quarantine on detection).
- Quarantine bucket for infected uploads (currently rejected and discarded).
- Per-plan storage quotas (relevant to the freemium model).
- Require recent authentication (OIDC `auth_time` / `max_age`) before accepting `DELETE /account`, and e-mail the user when deletion is scheduled and shortly before it executes. Both need decisions (identity provider settings, e-mail provider).
- Delete the person's identity at the OIDC provider (needs the provider's admin API). Today, signing in again after erasure creates a fresh empty account.
- Export currently covers data and receipt metadata only; bundling the receipt files (e.g. a ZIP built by the worker and delivered by signed URL) is not built. A client can fetch each file via the `downloadPath` in the export before the grace period ends.
