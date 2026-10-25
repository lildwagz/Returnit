# ReturnIt backend

Go + Gin + PostgreSQL. Two processes from one codebase:

| Binary | Role |
|---|---|
| `cmd/web` | HTTP API (`/api/v1`). Serves requests only. |
| `cmd/worker` | Background jobs: reminder planning/dispatch, receipt-file retention, push/email delivery. No HTTP. |

They share Postgres and S3-compatible storage (MinIO locally) and talk to each other **only through
Redis** ([Asynq](https://github.com/hibiken/asynq)). Postgres is the source of truth; Redis carries job
triggers, so a Redis outage never fails a web request (the worker's periodic jobs catch up).

```
web  --(RequestPurge)-->  Redis  -->  worker  --> deletes files, drains the outbox
                                       worker  --(cron: plan / dispatch / purge / sweep)--> itself
worker (dispatch) --(notify:push, one per notification)--> Redis --> worker --> Pusher (FCM/APNs/SMTP, TBD)
```

## Local development

**Everything in Docker (simplest):**

```sh
docker compose --profile auth --profile app up -d --build --wait   # first start takes several minutes
curl http://localhost:8080/health/ready
```

This starts Postgres, Redis, MinIO, ClamAV, Mailpit, Keycloak, the API (`localhost:8080`) and the worker. After code
changes run the same command again. Logs: `docker compose logs -f web worker`. Stop: `docker compose --profile auth --profile app stop`.

**Or: services in Docker, API and worker on your computer** (faster edit/run cycle):


```sh
docker compose --profile auth up -d --wait postgres redis minio clamav mailpit keycloak   # clamav needs a few minutes on first start
docker compose run --rm migrate
cp .env.example .env   # then export the variables (or use your shell/IDE); set OIDC_* to a real issuer
go run ./cmd/web
go run ./cmd/worker
```

Use `MALWARE_SCAN=off` only for local development when ClamAV is not running.

## Tests

```sh
go test ./...                                     # unit + handler tests, no services needed
# Integration tests need the compose services up and migrated. Run them serially (-p 1): they share one DB.
TEST_DATABASE_URL=postgres://returnit:returnit_dev@localhost:5432/returnit?sslmode=disable \
TEST_S3_ENDPOINT=localhost:9000 TEST_S3_ACCESS_KEY=returnit TEST_S3_SECRET_KEY=returnit_dev_secret \
TEST_CLAMAV_ADDR=localhost:3310 TEST_REDIS_ADDR=localhost:6379 \
go test -tags integration -p 1 ./internal/...
```

Design docs live in `Docs/` (start with `Docs/README.md`).

## Deploying

`deploy/` contains everything to run the backend on one Linux server (HTTPS, database, login server,
backups with a tested restore): see `deploy/README.md`.

## Login (OIDC) and fresh login

Any OIDC provider works. For local development there is a Keycloak with a ready-made realm
(`deploy/keycloak/returnit-realm.json`, users `alice` / `alice-dev-pass` and `bob` / `bob-dev-pass`,
**dev only**):

```
docker compose --profile auth up -d keycloak
OIDC_ISSUER=http://localhost:8180/realms/returnit go run ./cmd/web     # run the web app on the host
TEST_OIDC_ISSUER=http://localhost:8180/realms/returnit TEST_DATABASE_URL=...   go test -tags integration -run TestRealOIDCAndFreshLogin ./internal/httpapi/
```

If the API cannot reach the issuer's public address (for example in a container next to Keycloak), set
`OIDC_DISCOVERY_URL` to an internal address (e.g. `http://keycloak:8080/realms/returnit`): metadata is read from
there while tokens must still carry the public `OIDC_ISSUER`. `deploy/` does this.

`DELETE /account` requires a **fresh login**: the token's `auth_time` must be at most
`FRESH_LOGIN_MAX_AGE` (default 5m) old, else `401 reauthentication_required`. Refreshing a token or
reusing the browser's SSO session does not count; the app must send the user through the login page
again (`prompt=login`) and retry. A token without `auth_time` is refused. **Your identity provider must
put `auth_time` in the access token** (the dev realm does this with a mapper). Note: Keycloak's
password grant never sets it, so only the browser (authorization-code) flow can pass this check.

## Mobile push notifications (FCM)

The mobile app registers each phone with `POST /api/v1/devices` (`{"platform":"android"|"ios","pushToken":"..."}`;
call it at every app start and whenever the token changes) and removes it on sign-out with
`DELETE /api/v1/devices/{id}`. The worker then sends reminders for users whose reminder channel is `push`
through Firebase Cloud Messaging; dead tokens are removed automatically. Reminders also always appear in the
in-app inbox. Users on the `email` channel get the reminder by e-mail instead (needs SMTP).

Setup: create a Firebase project, add your Android and iOS apps (for iOS upload your Apple push key in
Firebase), create a service account with permission to send FCM messages, download its JSON key, and set
`FCM_PROJECT_ID` and `FCM_CREDENTIALS_FILE` on the **worker**. In the app, create an Android notification
channel with id `reminders`. Without these settings the worker warns at start-up and sends no pushes.
**Not verified against real Firebase** (no project available during development): the tests use a local fake
of Google's server. Do a test push with your first real device before launch.

## Deleting the login identity (Keycloak)

When an account is erased, the worker also deletes the user at Keycloak, so no profile lingers there and
the old login cannot be used to start over. Create a confidential client with *service accounts* enabled
and give its service account only the `realm-management` role `manage-users`, then set `KEYCLOAK_URL`,
`KEYCLOAK_REALM`, `KEYCLOAK_ADMIN_CLIENT_ID` and `KEYCLOAK_ADMIN_CLIENT_SECRET` on the **worker** (all four
or none). Without them the worker warns and the deletions stay queued. The dev realm already has such a
client (`returnit-worker`, secret `dev-worker-secret`: **dev only**). The identity is kept during the grace
period so the user can still log in to cancel. Test: `TEST_KEYCLOAK_URL=http://localhost:8180 go test -tags
integration -run TestIdentityErasedWithAccount ./internal/account`. Other providers need their own
`identity.Provider` implementation.

## E-mail (account deletion notices)

The worker e-mails the user when account deletion is requested, 24 hours before erasure, and after
erasure (plain text; dates in the user's own time zone, WIB by default). Set `SMTP_ADDR` (`host:port`),
`SMTP_FROM` and, for a real provider, `SMTP_USERNAME` / `SMTP_PASSWORD`; `SMTP_TLS` is `starttls`
(default), `tls` (port 465) or `none` (local development only; refused together with a password).
Without `SMTP_ADDR` the worker starts, warns, and the notices stay queued until e-mail is configured.
Failed sends are retried with backoff for up to 7 days. Locally, `docker compose up -d mailpit` catches
every message at http://localhost:8025. Test: `TEST_SMTP_ADDR=localhost:1025
TEST_MAILPIT_API=http://localhost:8025 go test -tags integration -run TestDeletionEmails ./internal/account`.

## Health and metrics

| Process | Endpoint | Meaning |
|---|---|---|
| web (`:8080`) | `GET /health/live`, `GET /health/ready` | alive / database reachable |
| worker (`WORKER_HEALTH_ADDR`, default `:8081`) | `GET /health/live` | process is up |
| worker | `GET /health/ready` | database **and** Redis reachable, and the per-minute reminder jobs (plan, dispatch) have succeeded within the last 3 minutes (a short grace period applies after start) — a stuck worker reports `503` with the failing checks |
| worker | `GET /metrics` | Prometheus text format, see below |

The worker port is for your orchestrator and monitoring only: **do not publish it to the internet**.
The distroless image has no shell or `curl`, so container health checks use the binary itself
(`/worker -healthcheck`, `/web -healthcheck`; wired into `docker-compose.yml`).

Worker metrics (no personal data): `returnit_object_deletions_pending`, `…_oldest_age_seconds`,
`…_failing`, `returnit_notifications_overdue`, `returnit_accounts_overdue_erasure`,
`returnit_queue_tasks{queue,state}` (`archived` = a job gave up after all retries),
`returnit_queue_latency_seconds{queue}`, `returnit_job_last_success_timestamp_seconds{job}`.

Suggested alerts (any monitoring system): worker `/health/ready` failing for > 5 min;
`returnit_object_deletions_oldest_age_seconds > 3600`; `returnit_object_deletions_failing > 0`;
`returnit_accounts_overdue_erasure > 0`; `returnit_queue_tasks{state="archived"} > 0`;
`returnit_notifications_overdue > 0` for > 10 min. These use the thresholds in
`Docs/docs/data-retention-policy.md`.

## Rate limiting

Sliding-window limits, counted in Redis so they hold across several web instances:

| Bucket | Default | Counted per | Env var |
|---|---|---|---|
| All authenticated API calls | 60 / minute | user | `RATE_LIMIT_USER_PER_MIN` |
| Receipt uploads (extra, stricter) | 10 / minute | user | `RATE_LIMIT_UPLOAD_PER_MIN` |
| `DELETE /account` (extra) | 3 / hour | user | `RATE_LIMIT_ACCOUNT_DELETE_PER_HOUR` |
| `GET /account/export` (extra) | 3 / hour | user | `RATE_LIMIT_EXPORT_PER_HOUR` |
| Requests that fail authentication | 30 / minute | client IP | `RATE_LIMIT_UNAUTH_PER_MIN_PER_IP` |

Over a limit the API answers `429` with `Retry-After` (seconds) and the usual error envelope
(`rate_limited`). Successful API calls report `X-RateLimit-Limit` / `X-RateLimit-Remaining`. `/health/*`
is never limited, and cancelling an account deletion is not subject to the deletion bucket.

- **The IP limit only charges failed authentication.** Many mobile users share one public IP behind carrier
  NAT, so authenticated traffic is never charged to an IP.
- **Client IP comes from the socket**, not from `X-Forwarded-For`, unless the sender is listed in
  `TRUSTED_PROXIES` (comma-separated IPs/CIDRs). Set it when the API runs behind a load balancer;
  otherwise every client would look like the balancer. IPs are hashed before they reach Redis and are
  never logged.
- **If Redis is unreachable** each web instance switches to its own in-memory limits (so protection
  stays on, though it is no longer shared) and probes Redis again every few seconds. A limiter
  error never takes the API down.

## App version check (force update) and maintenance

The API can lock outdated builds of the mobile app, invite users to update, and announce maintenance. Everything comes from
one file, `APP_CONFIG_FILE` (`latest.json`), read live every 30 seconds; with no file nothing is restricted. Contract: `GET /api/v1/app-config`
(public) and the answers `426 app_update_required` / `503 maintenance` on every other call that carries the header `X-App-Version`
(see `Docs/api/openapi.yaml`). The app side is described in `Docs/mobile/01-mobile-app-guide.md`, section 6.

**Try it on your computer** (the dev stack mounts `deploy/updates` into the API container):

```sh
cp deploy/updates/latest.json.example deploy/updates/latest.json    # minimum 1.1.0, latest 1.2.0
curl "http://localhost:8080/api/v1/app-config?platform=android&version=1.0.0"   # "status":"update_required"
curl "http://localhost:8080/api/v1/app-config?platform=android&version=1.1.5"   # "update_available"
curl "http://localhost:8080/api/v1/app-config?platform=android&version=1.2.0"   # "ok"
curl -i -H "X-App-Version: 1.0.0" http://localhost:8080/api/v1/purchases        # 426 (before any login check)
# edit the file (for example "maintenance": true) and repeat after at most 30 seconds; delete it to lift every limit
```

**On the server:** use `deploy/publish-apk.sh` (publishes the APK, raises the minimum, maintenance on/off); see `deploy/README.md`, section 7.1.
Tests: `go test ./internal/appconfig ./internal/httpapi`.

## Data export

`GET /api/v1/account/export` downloads everything stored about the caller as one JSON document
(`returnit-export/1`): profile, reminder preferences, purchases with receipt metadata, and delivered
notifications. It never includes other users' data or internal identifiers (storage keys, identity
subject). Receipt **files** are not embedded; each purchase carries a `downloadPath` to fetch its file.
The document is written incrementally and only terminated on success, so a truncated download is
invalid JSON rather than a plausible-looking partial export.

## Account deletion

`DELETE /api/v1/account` schedules erasure after a grace period (default 7 days,
`ACCOUNT_DELETION_GRACE`); `POST /api/v1/account/deletion/cancel` restores the account until then. The
worker erases due accounts every 15 minutes (data, receipt files, reminders); see
`Docs/docs/data-retention-policy.md`.

## Logging

Both processes write one JSON object per line to stdout (`LOG_LEVEL` = `debug`, `info` default, `warn`,
`error`). Every line carries `service` (`web` or `worker`).

**web** logs one `request` line per request, with a `request_id` that is also returned in the
`X-Request-ID` response header and in error bodies (an inbound `X-Request-ID` is kept only if it is
1-64 characters of `A-Za-z0-9._-`):

```json
{"level":"INFO","msg":"request","service":"web","request_id":"4f0c…","method":"GET","route":"/api/v1/purchases/:id","status":200,"latency_ms":3.2,"resp_bytes":412,"user_id":"9b1e…"}
```

Levels: 5xx = `ERROR` (with `err`, the real cause), 4xx = `WARN` (401s record why, e.g. expired token),
2xx/3xx = `INFO`, `/health/*` = `DEBUG`. Panics are recovered, logged with a stack trace (`panic
recovered`), and returned to the client as the standard 500 envelope only.

**worker** logs lifecycle and outcomes: `job failed` (job type, task id, attempt, max retries, error),
planning/dispatch/purge counts, and `job done` at `debug`.

**Never logged**: tokens or the `Authorization` header, query strings, raw URL paths (only the route
pattern), request/response bodies, e-mail addresses, filenames, product names, receipt contents,
signed URLs, client IPs. People are identified by internal user UUID only. As a safety net the logger
also redacts attributes named `authorization`, `token`, `password`, `secret`, `signed_url`, `url`,
`email`, `file_name` and similar. Tests assert both the fields that must be present and that nothing
sensitive appears.

## Docker

One image holds both programs; pick the process with the command (default `/web`).

```sh
docker build -t returnit .
docker run --rm --env-file .env -p 8080:8080 returnit /web
docker run --rm --env-file .env returnit /worker
# or via compose (builds the image; web needs a real OIDC issuer):
OIDC_ISSUER=https://your-issuer docker compose --profile app up -d --wait
```

The image is distroless (no shell), runs as a non-root user, and is ~18 MB. Both programs embed
timezone data so `Asia/Jakarta` (WIB) always resolves.

## CI

`.github/workflows/ci.yml` runs on pull requests and pushes to `main`:

- **checks**: `gofmt`, `go mod tidy` is clean, `go vet`, `go test -race`, `govulncheck`
- **integration**: Postgres + Redis + MinIO + ClamAV; migrations up → down → up; integration tests (`-p 1`)
- **image**: the Docker image builds and both programs start (and refuse to run without config)

It could not be run from this machine (not a Git repository yet); each step was reproduced locally
and the workflow passes `actionlint`.
