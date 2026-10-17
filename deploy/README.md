# Deploying ReturnIt on one server

This folder runs the whole backend on a single Linux server with Docker: HTTPS (Caddy), the API, the
worker, Postgres, Redis, receipt storage (MinIO), the virus scanner (ClamAV) and the login server
(Keycloak). Only ports 80 and 443 are open to the internet.

**Tested:** the full stack was started with these files on a local machine and checked end to end
(real login with PKCE over HTTPS, purchase, receipt upload through the virus scanner, device
registration, account deletion rules, hidden admin pages, e-mail sending, and a backup + restore drill).
**Not tested:** a real server with a real domain and real Let's Encrypt certificates, a real mail
provider, off-site upload to a real remote, and real push delivery.

## 1. What to prepare

| Item | Notes |
|---|---|
| A server | Ubuntu 24.04 (or 22.04), **4 GB RAM or more**, 2+ vCPU, 60+ GB disk, a public IPv4 address. ClamAV alone needs about 2 GB. |
| A domain | Strongly recommended. The mobile app stores the login address, which contains the domain, so changing it later means shipping an app update. Create two DNS "A" records pointing at the server: `api.<domain>` and `auth.<domain>`. |
| No domain yet? | For testing only, use `<ip-with-dashes>.sslip.io`, e.g. `203-0-113-5.sslip.io` for 203.0.113.5. `api.203-0-113-5.sslip.io` and `auth.203-0-113-5.sslip.io` then resolve to your server automatically, no DNS work. Get a real domain **before** you publish the app. |
| A mail service | For sign-up verification, password reset and deletion notices. Brevo, Resend or Amazon SES all give SMTP details (host, port, user, password) and need your sending domain verified. Do not send mail from the server itself. |
| A place for backups | Anything rclone supports (Backblaze B2, Cloudflare R2, Google Drive, another server...). Not the same server. |

## 2. Prepare the server (once)

```sh
ssh root@YOUR_SERVER_IP
apt update && apt -y upgrade
apt -y install ufw openssl git curl unattended-upgrades
ufw allow 22/tcp && ufw allow 80/tcp && ufw allow 443/tcp && ufw allow 443/udp && ufw --force enable
curl -fsSL https://get.docker.com | sh          # installs Docker and the compose plugin
adduser deploy && usermod -aG docker deploy      # work as "deploy" from now on (add your SSH key to it)
```

## 3. Put the code on the server

```sh
su - deploy
git clone <your repository> /home/deploy/returnit      # or copy the folder with scp/rsync
cd returnit/deploy
```

## 4. Configure

```sh
./init.sh                 # creates .env.prod with random passwords (readable only by you)
nano .env.prod            # set DOMAIN, ACME_EMAIL, the SMTP_* lines, MOBILE_REDIRECT_URI
```

* **Save `BACKUP_PASSPHRASE` in a password manager now.** Without it the backups cannot be opened.
* `MOBILE_REDIRECT_URI` must be exactly the address your app's login library returns to.
* Never commit `.env.prod`.

## 5. Start

```sh
./up.sh up -d --build     # the first start builds the app and downloads images (several minutes)
./up.sh ps                # everything should become "healthy"; ClamAV needs a few minutes for its signatures
```

Check from your own computer:

```sh
curl https://api.YOUR-DOMAIN/health/ready                                    # {"status":"ready"}
curl https://auth.YOUR-DOMAIN/realms/returnit/.well-known/openid-configuration   # JSON; "issuer" shows your domain
```

Certificates are requested automatically on the first visit; if it fails, check that both DNS names
point at the server and that ports 80/443 are open (`./up.sh logs caddy`).

The values for the app developer: **issuer** `https://auth.YOUR-DOMAIN/realms/returnit`, **client id**
`returnit-app`, **API base** `https://api.YOUR-DOMAIN/api/v1`. Guide: `Docs/mobile/README.md`.

## 6. Administering the login server (Keycloak)

The admin console is **not** reachable from the internet. Use an SSH tunnel from your computer:

```sh
ssh -L 8180:localhost:8180 deploy@YOUR_SERVER_IP
```

then open http://localhost:8180 and sign in as `admin` with `KEYCLOAK_ADMIN_PASSWORD` from `.env.prod`.
Use it to look at users, change e-mail texts, or reset a user. Create your own admin user and turn the
`admin` account's password into something only you keep.

### Sign in with Google (optional, needs a real domain)

Google only accepts redirect addresses on real domains, so this does not work with a raw IP (and probably not
with `sslip.io`).

1. In Google Cloud Console create a project, set up the **OAuth consent screen** (app name, support e-mail,
   privacy-policy link) and create an **OAuth client ID** of type **Web application**.
2. Add this **Authorized redirect URI**: `https://auth.<your-domain>/realms/returnit/broker/google/endpoint`.
3. Put the client id and secret in `.env.prod` (`GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`; wrap the secret in
   single quotes) and run `./enable-google.sh`. It is safe to run again; `./enable-google.sh --disable` removes it.
4. The login page now shows a Google button. The app needs no change.

How it behaves: someone who already registered with the same e-mail address is asked to link the Google login to
the existing account (one person stays one account). Google's account chooser is always shown, so a re-login
before deleting an account needs a tap; if the user is already signed in to Google, no password is asked, so the
"fresh login" proof is weaker than with a password. If the Google consent screen is left in "Testing" mode only
listed test users can sign in; publish it before launch. **Tested** up to the redirect to Google (button,
client id, redirect address, scopes); the actual Google round trip has not been tested without real credentials.
If your iPhone app offers Google login, check Apple's current rule about also offering "Sign in with Apple".

## 7. Updating

```sh
cd ~/returnit && git pull
cd deploy && ./up.sh up -d --build
```

Database changes are applied automatically (the `migrate` service runs before the app starts). Watch
`./up.sh logs -f web worker` for a minute afterwards.

## 8. Backups (do this on day one)

```sh
crontab -e
# every night at 02:00 server time:
0 2 * * * /home/deploy/returnit/deploy/backup.sh >> /home/deploy/backup.log 2>&1
```

What a backup contains: the app database and the Keycloak database (encrypted with
`BACKUP_PASSPHRASE`), a list of erased accounts (`deletion-log-*.csv`, no personal data, kept forever), and
a copy of the receipt files. Dumps older than `BACKUP_KEEP_DAYS` (35) are removed.

**Off-site copy (required for real protection):** create an rclone config (`rclone config`, on any
computer) for your storage, ideally with a `crypt` remote so the receipt files are encrypted there too,
copy it to the server, and set in `.env.prod`:

```
BACKUP_RCLONE_CONF=/home/deploy/rclone.conf
BACKUP_RCLONE_DEST=offsite:returnit-backups
```

The script warns every night until this is set.

**Restoring** (this replaces the current data):

```sh
./restore.sh --app backups/returnit-<time>.dump.enc --keycloak backups/keycloak-<time>.dump.enc
```

It restores, updates the database to the current version, **erases again every account that was erased
since the backup** (required by the retention policy; it combines the live database, the backup and all
saved deletion logs), queues those people's login identities and receipt files for deletion, and restarts
the app. Accounts created after the backup are lost; that is the nature of a restore.

**Practise the restore** on a spare server once before launch, and every few months after. A backup you
have never restored is a hope, not a backup. Receipt files are restored by copying `backups/receipts`
back into the storage volume (or the hosted bucket); the worker's hourly sweep removes any file
without a database row.

## 9. Monitoring

* From outside: an uptime check (UptimeRobot, Better Stack, or your own Uptime Kuma) on
  `https://api.YOUR-DOMAIN/health/ready` and the Keycloak discovery URL above.
* From inside: the worker publishes Prometheus metrics at `http://worker:8081/metrics` on the private
  network only. Suggested alerts are in the main README ("Health and metrics"). Until something scrapes
  them, check by hand: `./up.sh exec worker /worker -healthcheck` and `./up.sh logs --since 1h worker | grep -i error`.
* Disk space: `df -h` weekly (the database, receipts and ClamAV signatures grow).

## 10. Security checklist

* Only ports 22, 80, 443 open. Log in with SSH keys only (`PasswordAuthentication no`).
* `.env.prod` and `rclone.conf` are mode 600 and never in git.
* Push key (optional): put the Firebase key at `/home/deploy/secrets/fcm.json`, run
  `sudo chown 65532 /home/deploy/secrets/fcm.json && sudo chmod 400 /home/deploy/secrets/fcm.json`, and set
  `FCM_PROJECT_ID` and `FCM_KEY_FILE` in `.env.prod`.
* Keycloak and Postgres are patched by pulling newer images: `./up.sh pull && ./up.sh up -d` monthly.
  The MinIO image (`cgr.dev/chainguard/minio:latest`) is not version-pinned yet; pin it by digest once
  you have a version you trust.
* Review the data-retention policy (`Docs/docs/data-retention-policy.md`): several values need an owner or
  legal decision before launch.

## 11. If something is wrong

| Symptom | Look at |
|---|---|
| `https://` does not work | `./up.sh logs caddy`: DNS not pointing here yet, or ports 80/443 blocked. |
| Web stays "starting" | `./up.sh logs web`: usually Keycloak not healthy yet or ClamAV still downloading signatures. |
| Login page shows the wrong address or loops | `DOMAIN` in `.env.prod` is wrong or was changed after the first start; run `./up.sh up -d --force-recreate keycloak web`. |
| App says "invalid token" | The app's issuer/audience differ from the server's (`https://auth.<domain>/realms/returnit`, audience `returnit-api`). |
| Out of memory, containers restart | Server too small, or add swap as a stop-gap; ClamAV is the biggest user. |
