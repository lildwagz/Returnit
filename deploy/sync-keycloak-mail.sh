#!/usr/bin/env bash
# Copies the SMTP_* settings of .env.prod into the running Keycloak realm.
#
# Why this exists: the realm file is read ONCE, the first time Keycloak starts. Changing SMTP_FROM,
# SMTP_USERNAME or SMTP_PASSWORD in .env.prod later changes the API and worker (they read the file at every
# start) but NOT Keycloak, which keeps sending registration, verification and password-reset e-mails with the old
# values (for example Brevo refusing "noreply@example.com"). Run this after changing any SMTP_ line.
#   ./sync-keycloak-mail.sh
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

: "${SMTP_HOST:?set SMTP_HOST in .env.prod}"
: "${SMTP_FROM:?set SMTP_FROM in .env.prod}"
export KC_URL="${KC_ADMIN_URL:-http://localhost:8180}"

python3 - <<'PY'
import json, os, sys, urllib.error, urllib.parse, urllib.request

kc = os.environ['KC_URL']
realm = os.environ.get('KEYCLOAK_REALM', 'returnit')


def call(method, path, token=None, form=None, body=None):
    h = {'Authorization': 'Bearer ' + token} if token else {}
    data = None
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
    elif body is not None:
        data = json.dumps(body).encode()
        h['Content-Type'] = 'application/json'
    try:
        r = urllib.request.urlopen(urllib.request.Request(kc + path, data=data, method=method, headers=h), timeout=30)
        raw = r.read()
        return r.status, json.loads(raw) if raw[:1] in (b'{', b'[') else None
    except urllib.error.HTTPError as e:
        print('Keycloak answered %s on %s %s' % (e.code, method, path), file=sys.stderr)
        sys.exit(1)
    except OSError as e:
        print('Cannot reach Keycloak at %s (%s). Is the stack up? Does the port 8180 listen on this server?' % (kc, e), file=sys.stderr)
        sys.exit(1)


_, tok = call('POST', '/realms/master/protocol/openid-connect/token',
              form={'grant_type': 'password', 'client_id': 'admin-cli', 'username': 'admin',
                    'password': os.environ['KEYCLOAK_ADMIN_PASSWORD']})
t = tok['access_token']

tls = os.environ.get('SMTP_TLS', 'starttls')
smtp = {
    'host': os.environ['SMTP_HOST'],
    'port': os.environ.get('SMTP_PORT', '587'),
    'from': os.environ['SMTP_FROM'],
    'fromDisplayName': 'ReturnIt',
    'ssl': 'true' if tls == 'tls' else 'false',
    'starttls': 'true' if tls == 'starttls' else 'false',
}
if os.environ.get('SMTP_USERNAME'):
    smtp.update({'auth': 'true', 'user': os.environ['SMTP_USERNAME'], 'password': os.environ.get('SMTP_PASSWORD', '')})
else:
    smtp['auth'] = 'false'

_, cur = call('GET', '/admin/realms/' + realm, t)
before = cur.get('smtpServer', {}).get('from')
call('PUT', '/admin/realms/' + realm, t, body={'smtpServer': smtp})  # partial update: nothing else changes
_, after = call('GET', '/admin/realms/' + realm, t)
now = after.get('smtpServer', {})
print('Keycloak realm "%s": sender %s -> %s (host %s:%s, user %s)' % (
    realm, before, now.get('from'), now.get('host'), now.get('port'), now.get('user', '-')))
PY
