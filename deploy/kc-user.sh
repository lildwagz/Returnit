#!/usr/bin/env bash
# Everyday user administration for the login server (Keycloak), without the web console.
# Run on the server, from this folder:
#   ./kc-user.sh list [text]              users (optionally filtered by part of the e-mail or name)
#   ./kc-user.sh show <email>             one user: status, verified?, created, pending actions
#   ./kc-user.sh resend-verify <email>    send the "verify your e-mail" message again
#   ./kc-user.sh reset-password <email>   send a "set a new password" e-mail to the user
#   ./kc-user.sh verify <email>           mark the e-mail as verified (when the user cannot receive mail)
#   ./kc-user.sh disable <email>          block login      |   ./kc-user.sh enable <email>   allow it again
#   ./kc-user.sh create <email> [first last]   new verified user; asks for a password
#                                              (non-interactive: KC_NEW_PASSWORD=... ./kc-user.sh create ...)
#   ./kc-user.sh delete <email>           remove the LOGIN only (read the warning it prints)
# Passwords are never taken from the command line, so they do not end up in the shell history.
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

export KC_URL="${KC_ADMIN_URL:-http://localhost:8180}"
cmd="${1:-help}"
case "$cmd" in
help | -h | --help) sed -n '2,15p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
create)
	if [ -z "${KC_NEW_PASSWORD:-}" ]; then
		read -r -s -p "Password for the new user (min. 10 characters): " KC_NEW_PASSWORD
		echo
		export KC_NEW_PASSWORD
	fi
	export KC_NEW_PASSWORD
	;;
delete)
	echo "WARNING: this removes the login only. The person's purchases and receipts stay in the app database,"
	echo "and a later registration with the same e-mail address will fail with 'identity_conflict'."
	echo "To erase a person completely, use the account deletion in the app (7 days grace period)."
	read -r -p "Type the e-mail address to confirm: " confirm
	[ "$confirm" = "${2:-}" ] || { echo "cancelled"; exit 1; }
	;;
esac

python3 - "$@" <<'PY'
import json, os, sys, urllib.error, urllib.parse, urllib.request

kc = os.environ['KC_URL']
realm = os.environ.get('KEYCLOAK_REALM', 'returnit')
args = sys.argv[1:]
cmd = args[0] if args else 'help'


def call(method, path, token=None, form=None, body=None, ok=(200, 201, 204)):
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
        return r.status, (json.loads(raw) if raw[:1] in (b'{', b'[') else None), r.headers
    except urllib.error.HTTPError as e:
        detail = e.read()[:200].decode('utf8', 'replace')
        sys.exit('Keycloak answered %s: %s' % (e.code, detail))
    except OSError as e:
        sys.exit('Cannot reach Keycloak at %s (%s). Is the stack up?' % (kc, e))


_, tok, _ = call('POST', '/realms/master/protocol/openid-connect/token',
                 form={'grant_type': 'password', 'client_id': 'admin-cli', 'username': 'admin',
                       'password': os.environ['KEYCLOAK_ADMIN_PASSWORD']})
t = tok['access_token']
base = '/admin/realms/' + realm


def find(email):
    _, users, _ = call('GET', base + '/users?exact=true&email=' + urllib.parse.quote(email), t)
    if not users:
        sys.exit('No user with e-mail %s' % email)
    return users[0]


def need(i, what):
    if len(args) <= i:
        sys.exit('missing %s (see ./kc-user.sh help)' % what)
    return args[i]


def when(ms):
    import datetime
    return datetime.datetime.fromtimestamp(ms / 1000, datetime.timezone.utc).strftime('%Y-%m-%d %H:%M UTC') if ms else '-'


def mail_params():
    return urllib.parse.urlencode({'client_id': 'returnit-app', 'redirect_uri': os.environ.get('MOBILE_REDIRECT_URI', 'com.returnit.app:/oauth2redirect'), 'lifespan': 86400})


if cmd == 'list':
    q = '&search=' + urllib.parse.quote(args[1]) if len(args) > 1 else ''
    _, users, _ = call('GET', base + '/users?max=200&briefRepresentation=false' + q, t)
    for u in users:
        print('%-38s %-8s %-9s %s' % (u.get('email', '-'), 'enabled' if u['enabled'] else 'DISABLED',
                                       'verified' if u.get('emailVerified') else 'UNVERIFIED', when(u.get('createdTimestamp'))))
    print('%d user(s)' % len(users))
elif cmd == 'show':
    u = find(need(1, 'e-mail'))
    print(json.dumps({k: u.get(k) for k in ('id', 'email', 'firstName', 'lastName', 'enabled', 'emailVerified', 'requiredActions')} |
                     {'created': when(u.get('createdTimestamp'))}, indent=2))
elif cmd == 'resend-verify':
    u = find(need(1, 'e-mail'))
    call('PUT', '%s/users/%s/execute-actions-email?%s' % (base, u['id'], mail_params()), t, body=['VERIFY_EMAIL'])
    print('verification e-mail sent to', u['email'])
elif cmd == 'reset-password':
    u = find(need(1, 'e-mail'))
    call('PUT', '%s/users/%s/execute-actions-email?%s' % (base, u['id'], mail_params()), t, body=['UPDATE_PASSWORD'])
    print('password e-mail sent to', u['email'])
elif cmd == 'verify':
    u = find(need(1, 'e-mail'))
    call('PUT', '%s/users/%s' % (base, u['id']), t, body={'emailVerified': True, 'requiredActions': [a for a in u.get('requiredActions', []) if a != 'VERIFY_EMAIL']})
    print('e-mail marked as verified:', u['email'])
elif cmd in ('disable', 'enable'):
    u = find(need(1, 'e-mail'))
    call('PUT', '%s/users/%s' % (base, u['id']), t, body={'enabled': cmd == 'enable'})
    print('%sd: %s' % (cmd, u['email']))
elif cmd == 'create':
    email = need(1, 'e-mail')
    pw = os.environ.get('KC_NEW_PASSWORD', '')
    if len(pw) < 10:
        sys.exit('the password needs at least 10 characters')
    first = args[2] if len(args) > 2 else 'Pengguna'
    last = args[3] if len(args) > 3 else 'Baru'
    call('POST', base + '/users', t, body={'username': email, 'email': email, 'firstName': first, 'lastName': last, 'enabled': True,
                                           'emailVerified': True, 'requiredActions': [],
                                           'credentials': [{'type': 'password', 'value': pw, 'temporary': False}]})
    print('created:', email)
elif cmd == 'delete':
    u = find(need(1, 'e-mail'))
    call('DELETE', '%s/users/%s' % (base, u['id']), t)
    print('login removed:', u['email'])
else:
    sys.exit('unknown command %r (see ./kc-user.sh help)' % cmd)
PY
