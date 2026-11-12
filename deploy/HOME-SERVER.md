# Hosting ReturnIt on a home machine (Cloudflare Tunnel)

How to run the production stack on a machine without a public IP address (a laptop or mini PC at home), reachable at
`api.`, `auth.`, `dl.` and `files.` of your own domain through a Cloudflare Tunnel, and how to move an existing server
there. Everything else (backups, releases, Keycloak, the APK page) works as described in [README.md](README.md).

```
phone ──https──> Cloudflare ──tunnel (outgoing from home)──> cloudflared ──> Caddy :8081-8084 ──> web / keycloak / minio
```

Nothing on the home machine listens on the internet: `cloudflared` makes an outgoing connection, so no router port has to
be opened and it works behind carrier-grade NAT. Cloudflare terminates HTTPS, so it can technically read the traffic
(with Caddy on a public server, only your server can).

Files involved:

| File | Purpose |
|---|---|
| `docker-compose.tunnel.yml` | adds `cloudflared`, takes Caddy off ports 80/443 and gives it `Caddyfile.tunnel` |
| `Caddyfile.tunnel` | the same sites and rules as `Caddyfile`, on plain-HTTP ports 8081 (api), 8082 (auth), 8083 (dl), 8084 (files) |
| `docker-compose.staging.yml` | keeps the worker stopped during a rehearsal, so nobody is reminded twice |
| `restore-receipts.sh` | puts the receipt files from a `receipts-*.tar.enc` archive back into MinIO |

`COMPOSE_OVERLAY` in `.env.prod` lists the extra compose files (separated by spaces, in quotes); `./up.sh` and every
other script pick it up.

## 1. The machine

- Ubuntu with Docker and the compose plugin (`docker compose version` 2.24 or newer), the user in the `docker` group.
- Room for the stack: about 2 GB of RAM in use (ClamAV about 1 GB, Keycloak about 600 MB, MinIO about 250 MB) and
  10 GB of disk plus the receipts.
- It must keep running unattended:
  - `sudo systemctl enable docker` (containers come back after a reboot; they all have `restart: unless-stopped`)
  - closing the lid must not suspend it: in `/etc/systemd/logind.conf` set `HandleLidSwitch=ignore` and
    `HandleLidSwitchExternalPower=ignore`, then `sudo systemctl restart systemd-logind`
  - no automatic suspend: `sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target`
  - if the BIOS has "power on after AC loss", turn it on
- Tailscale (or another VPN) for administration: SSH and the Keycloak console (`ssh -L 8180:localhost:8180 <machine>`).

## 2. The domain and the tunnel (Cloudflare dashboard)

1. Add the domain to Cloudflare (Free plan) and change the nameservers at the registrar to the two Cloudflare gives.
   Wait until the domain shows **Active**. Turn on two-factor login at the registrar and at Cloudflare, and automatic
   renewal of the domain: whoever controls the domain controls the app.
2. **Zero Trust > Networks > Tunnels > Create a tunnel**, type Cloudflared, name `returnit`. Copy only the token from
   the install command (do not run that command; the container below runs the connector).
3. In the tunnel, add four **public hostnames** (Cloudflare creates the DNS records):

   | Hostname | Service |
   |---|---|
   | `api.<domain>` | `http://caddy:8081` |
   | `auth.<domain>` | `http://caddy:8082` |
   | `dl.<domain>` | `http://caddy:8083` |
   | `files.<domain>` | `http://caddy:8084` |

4. **Caching > Configuration > Browser Cache TTL: Respect Existing Headers.** The default (4 hours) replaces the
   `no-cache` Caddy sends on the download page and `returnit-latest.apk`, so phones that downloaded the previous
   release install it again. After publishing a release, **Purge Everything** once.
5. If "Sign in with Google" is on: in the Google Cloud console add
   `https://auth.<domain>/realms/returnit/broker/google/endpoint` to the OAuth client's redirect URIs and the domain to
   the consent screen's authorised domains.

## 3. The settings (`deploy/.env.prod`)

Moving a server: copy the old `.env.prod` as it is (it holds `BACKUP_PASSPHRASE`, needed to open the backups, and
`KEYCLOAK_WORKER_SECRET`, which is stored in the Keycloak database you restore), without printing it:

```bash
ssh old 'cat ~/returnit/deploy/.env.prod' | ssh new 'umask 077; cat > ~/returnit/deploy/.env.prod'
```

Copy the FCM key the same way to the path in `FCM_KEY_FILE` and give it to the containers' user:
`docker run --rm -v $HOME/secrets:/s alpine:3.20 chown 65532 /s/fcm.json` (mode 400).

Then change on the new machine:

```bash
DOMAIN=<domain>
COMPOSE_OVERLAY='docker-compose.tunnel.yml docker-compose.staging.yml'   # rehearsal; at cutover only the tunnel file
CLOUDFLARE_TUNNEL_TOKEN=<token from step 2>
```

and remove `BACKUP_PULLED_BY` if the old server had it (this machine is no longer the one that pulls; see step 7).

E-mail: if the mail service only accepts known IP addresses (Brevo: Security > Authorised IPs), turn that off; a home
connection's address changes. With your own domain, authenticate it at the mail service (SPF/DKIM/DMARC records in
Cloudflare) and send from it (`SMTP_FROM=noreply@<domain>`), then run `./sync-keycloak-mail.sh`. Changing any `SMTP_`
line recreates the Keycloak container: logins fail for about a minute while it starts.
A fresh server instead: `./init.sh`, then set the three lines above.

Also copy `deploy/updates/` (APKs and the download page) and `deploy/.release/env` (the pinned app image) from the old
server: `ssh old 'tar -C ~/returnit/deploy -cf - updates .release' | ssh new 'tar -C ~/returnit/deploy -xf -'`.

## 4. Rehearsal (old server still live)

With `docker-compose.staging.yml` in `COMPOSE_OVERLAY` the worker does not start, so no reminders, e-mails or push
notifications go out from here.

```bash
cd ~/returnit/deploy
./restore.sh --app <backups>/returnit-<time>.dump.enc --keycloak <backups>/keycloak-<time>.dump.enc --yes
./restore-receipts.sh <backups>/receipts-<time>.tar.enc --yes
./publish-apk.sh page            # rebuilds the download page for the new DOMAIN (latest.json's download
                                 # address follows at the next ./publish-apk.sh publish, see step 5)
./up.sh ps                       # everything healthy (worker absent)
```

Check from outside your home network (phone on mobile data):

- `https://api.<domain>/health/ready` answers
- `https://auth.<domain>/realms/returnit/.well-known/openid-configuration` shows `"issuer": "https://auth.<domain>/realms/returnit"`
- `https://auth.<domain>/admin` is 404
- `https://dl.<domain>/` and `/id` show the page, the APK downloads, `/.well-known/assetlinks.json` answers
- an app build pointed at the new domain can log in (e-mail and Google), see purchases, open a receipt (signed
  `files.` link) and upload one
- `free -m` leaves room; `docker stats --no-stream` per container

Data written during the rehearsal is thrown away at cutover.

## 5. The app

The app has the addresses built in, so it needs a release for the new domain
(ReturnitMobile: `src/lib/auth.ts`, `src/lib/buckets.ts`, and the invite link host in `app.json`).
Logins made on the old domain are not valid on the new one: everyone signs in once more.
Publish it on the new machine first (`./publish-apk.sh publish <version> <apk>`, which also points `latest.json` at
`https://dl.<domain>/`), then on the **old** server with `--min <version>`, so the installed apps are told to update
and land on the new server.

## 6. Cutover

1. Announce a short pause. On the old server: `./publish-apk.sh maintenance on`, then `./up.sh stop worker`
   (the API stays up only to answer "maintenance" and the minimum version).
2. On the old server: `./backup.sh` (a final dump and receipts archive) and copy the new files to the new machine.
3. On the new machine: restore them as in step 4, then set `COMPOSE_OVERLAY=docker-compose.tunnel.yml` (without the
   staging file) and `./up.sh up -d` (the worker starts now). Run the checks of step 4 again.
4. On the old server: `./publish-apk.sh maintenance off` and `./publish-apk.sh min <new version>`: old apps are now
   locked until they install the new release, which talks to the new server. Keep the old server running (worker
   stopped) for as long as you can afford, so late updaters are still told where to go.

Rollback until step 3 is finished: `./publish-apk.sh maintenance off` and `./up.sh up -d` on the old server; its data
was not changed.

## 7. Backups and monitoring on the new machine

- Cron as on the old server (`crontab -e`; the machine's clock zone applies, Azure runs in UTC):
  `0 2 * * * $HOME/returnit/deploy/backup.sh >> $HOME/backup.log 2>&1` and
  `*/5 * * * * $HOME/returnit/deploy/monitor.sh --quiet >> $HOME/monitor.log 2>&1`.
- The backups must leave the house: set `BACKUP_RCLONE_CONF` and `BACKUP_RCLONE_DEST` to an rclone remote (an
  encrypted `crypt` remote over Google Drive, Backblaze B2, ...). The files are already encrypted with
  `BACKUP_PASSPHRASE`; keep that passphrase and a copy of `.env.prod` somewhere other than this machine.
- Prove a restore now and then, as in step 4, on another machine.
