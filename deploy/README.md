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
| A server | Ubuntu 24.04 (or 22.04), public IPv4 address, 2+ vCPU, 60+ GB disk. **4 GB RAM or more with virus scanning** (ClamAV alone needs about 2 GB); a 2 GB server can run everything else if you set `MALWARE_SCAN=off`. |
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

### Virus scanning on or off

`MALWARE_SCAN` in `.env.prod` is the only switch. `off` does not start ClamAV and accepts uploads without scanning
(the API logs a warning at start-up); `on` starts ClamAV and rejects infected files. To change it, edit the line and run
`./up.sh up -d`; the first start with `on` needs a few minutes while ClamAV downloads its signatures. Uploads made while
scanning was off stay unscanned. Use `off` only on a small pilot server; keep `on` once real users upload documents.

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

## 7.1 Publishing the Android app (download page, force update, maintenance)

Until the app is in the Play Store, this server hands out the APK. Caddy serves it at `https://dl.<DOMAIN>/` (a small
download page, the APK files and their SHA-256), and the API tells the app which versions may still be used. Everything is
driven by one script and the file `updates/latest.json` (read live by the API, no restart):

```sh
cd ~/returnit/deploy
# after an EAS build: give the version (as in app.json) and the APK address from the build page
./publish-apk.sh publish 1.4.0 https://expo.dev/artifacts/eas/....apk
./publish-apk.sh publish 1.4.0 <apk> --min 1.2.0 --message "Perbarui aplikasi."   # also lock older apps
./publish-apk.sh min 1.2.0                 # only change the minimum (older apps are locked until they update)
./publish-apk.sh maintenance on "Kembali pukul 10.00"    # every app call answers 503; "off" ends it
./publish-apk.sh publish 1.4.0 <apk> --note "New reminders tab" --note "Faster lists"   # "What's new" on the page (English)
./publish-apk.sh page [--note "..."]       # rebuild the download page without publishing (after editing the template)
./publish-apk.sh status
```

The page is English and comes from `download-page/index.template.html` (logo and fonts in `download-page/assets`, served at `/assets/`). If you change the `Caddyfile`, recreate Caddy (`./up.sh up -d --force-recreate caddy`): after a `git pull` its container can still read the old file.

The script refuses: an APK that is not an APK, a published version whose content would change, and a minimum above the
newest published build (nobody could update). It keeps the newest 10 builds. After raising the minimum, check
`curl https://api.<DOMAIN>/api/v1/app-config?platform=android&version=<an old version>` (it should say `update_required`).
Only the page, its `assets/`, `returnit-*.apk` and `returnit-*.apk.sha256` are public; `latest.json` is not. Builds made before this feature
do not send their version, so they cannot be locked: ask those users to install the new build by hand.

## 7.2 Automatic deploys (GitHub Actions to this server)

A push to `main` runs the tests, builds the image once, smoke-tests it, pushes it to `ghcr.io/lildwagz/returnit`
(tags: the commit and `latest`) and then the `deploy` job runs `release.sh` on this server over SSH. The script
fast-forwards this checkout to that commit (the migrations and the Caddyfile come from it), pulls the image **by
digest**, runs `up -d`, waits until `web` and `worker` are healthy, and goes back to the previous image if they are
not. Migrations are never undone by a rollback: keep them backward compatible. The state is in `deploy/.release/`
(not in git): `env` (the image now running), `history` (one line per release).

One-time setup (nothing here is automated, and nothing secret goes into git or chat):

1. **A key only for deploys.** On your laptop: `ssh-keygen -t ed25519 -N "" -C returnit-deploy -f returnit-deploy`.
2. **Restrict it on the server.** Add one line to `~/.ssh/authorized_keys` of the deploy user, with the public key:
   `restrict,command="/home/azzam/returnit/deploy/release.sh" ssh-ed25519 AAAA... returnit-deploy`
   The key can then run that one script and nothing else (the arguments arrive in `SSH_ORIGINAL_COMMAND`).
3. **Let the server pull the private image.** Create a *classic* GitHub token with only `read:packages` (GHCR does not
   take fine-grained tokens), with an expiry you will remember (a deploy fails at `docker pull` once it expires), then on the
   server: `docker login ghcr.io -u <github user>` (paste the token at the prompt, never on the command line). Check with
   `docker pull ghcr.io/lildwagz/returnit:latest`.
4. **GitHub, repository settings.** Environment `production` (add required reviewers there if you want a click
   before each deploy). Secrets: `DEPLOY_SSH_KEY` (the private key), `DEPLOY_KNOWN_HOSTS` (the output of
   `ssh-keyscan -t ed25519 <host>`, checked against the real fingerprint), `DEPLOY_HOST`, `DEPLOY_USER`.
   Variable `DEPLOY_HEALTH_URL`, for example `https://api.<DOMAIN>/api/v1/app-config`. Delete the private key file
   from your laptop afterwards.
5. **First release by hand** (to see it work before CI does it):
   `ssh <server> 'cd ~/returnit/deploy && ./release.sh ghcr.io/lildwagz/returnit@sha256:<digest> <commit>'`
   The digest is printed by the `publish` job (`published ghcr.io/...@sha256:...`).

Roll back to the previous image by hand: `cat deploy/.release/history`, then `./release.sh <older image> <its commit>`.
The old way still works: without `deploy/.release/env`, `./up.sh up -d --build` builds the image here.

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

## Login page look (Keycloak theme)

The login, register and reset-password pages use the `returnit` theme (`deploy/keycloak/themes/returnit/login`): the app's colours, logo and font, in Indonesian, English and Arabic (right to left), light and dark. It is mounted into the Keycloak container by `docker-compose.prod.yml`. A realm that already exists keeps its old settings, so on a running server:

```bash
cd ~/returnit && git pull
cd deploy && ./up.sh up -d keycloak     # mounts the theme (Keycloak restarts, about a minute)
./apply-login-theme.sh                  # selects the theme and adds Arabic; --reset goes back to Keycloak's own look
```

Changing a theme file later only needs `./up.sh restart keycloak`. To try it locally: `docker compose --profile auth up -d keycloak`.
