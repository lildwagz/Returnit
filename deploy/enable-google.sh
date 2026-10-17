#!/usr/bin/env bash
# Adds (or updates) "Sign in with Google" on the login page, or removes it:
#   ./enable-google.sh            needs GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET in .env.prod
#   ./enable-google.sh --disable
# Safe to run again. Uses Keycloak's admin tool inside the running container; nothing is exposed.
#
# In Google Cloud Console (APIs & Services > Credentials) create an OAuth client of type "Web application"
# and add this "Authorized redirect URI":   https://auth.<DOMAIN>/realms/returnit/broker/google/endpoint
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

kc() { dc exec -T keycloak /opt/keycloak/bin/kcadm.sh "$@"; }

kc config credentials --server http://localhost:8080 --realm master --user admin --password "$KEYCLOAK_ADMIN_PASSWORD" >/dev/null

if [ "${1:-}" = "--disable" ]; then
	kc delete identity-provider/instances/google -r returnit 2>/dev/null && echo "Google login removed." || echo "Google login was not enabled."
	exit 0
fi

: "${GOOGLE_CLIENT_ID:?set GOOGLE_CLIENT_ID in .env.prod}"
: "${GOOGLE_CLIENT_SECRET:?set GOOGLE_CLIENT_SECRET in .env.prod}"

settings=(
	-s alias=google -s providerId=google -s enabled=true -s displayName=Google
	# Google verifies e-mail addresses itself.
	-s trustEmail=true -s storeToken=false -s config.syncMode=IMPORT
	# Existing e-mail/password users who sign in with Google are asked to link to their account
	# (Keycloak's standard "first broker login" flow), so one person stays one account.
	-s firstBrokerLoginFlowAlias="first broker login"
	-s config.clientId="$GOOGLE_CLIENT_ID" -s config.clientSecret="$GOOGLE_CLIENT_SECRET"
	-s config.defaultScope="openid email profile"
	# Always show Google's account chooser, so a re-login (e.g. before deleting an account) needs a tap.
	-s config.prompt=select_account
)

if kc get identity-provider/instances/google -r returnit >/dev/null 2>&1; then
	kc update identity-provider/instances/google -r returnit "${settings[@]}"
	echo "Google login updated."
else
	kc create identity-provider/instances -r returnit "${settings[@]}"
	echo "Google login enabled."
fi
echo "Redirect URI to register at Google: https://auth.${DOMAIN}/realms/returnit/broker/google/endpoint"
