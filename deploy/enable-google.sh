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

# Keycloak's own "first broker login" flow makes a person who already has an account with the same e-mail address
# confirm the link and verify it by e-mail, and that step fails with "email_already_verified" when the address is
# already verified, so the app only sees "error". This copy links such an account automatically. That is safe here
# because Google verifies e-mail addresses (trustEmail=true below); never reuse it for a provider that does not.
FLOW="first broker login auto-link"
ENC="${FLOW// /%20}"

# Prints the execution whose display name ends with the given name, with its requirement changed (reads the flow's executions on stdin).
with_requirement() {
	python3 -c '
import json, sys
name, req = sys.argv[1], sys.argv[2]
for e in json.load(sys.stdin):
    # a copied sub-flow is named "<new flow> <original name>", so match the end of the name
    if e.get("displayName", "").endswith(name):
        e["requirement"] = req
        print(json.dumps(e))
        break
' "$1" "$2"
}

ensure_flow() {
	local flows
	flows=$(kc get authentication/flows -r returnit --fields alias) # keep the output: "| grep -q" would end it with SIGPIPE under pipefail
	if ! printf '%s' "$flows" | grep -q "\"$FLOW\""; then
		kc create "authentication/flows/first%20broker%20login/copy" -r returnit -s newName="$FLOW" >/dev/null
	fi
	local execs body
	execs=$(kc get "authentication/flows/$ENC/executions" -r returnit)
	for step in "Confirm link existing account" "Account verification options"; do
		body=$(printf '%s' "$execs" | with_requirement "$step" DISABLED)
		[ -n "$body" ] && kc update "authentication/flows/$ENC/executions" -r returnit -b "$body" >/dev/null
	done
	if ! printf '%s' "$execs" | grep -q '"idp-auto-link"'; then
		kc create "authentication/flows/${FLOW// /%20}%20Handle%20Existing%20Account/executions/execution" -r returnit -s provider=idp-auto-link >/dev/null
		execs=$(kc get "authentication/flows/$ENC/executions" -r returnit)
	fi
	body=$(printf '%s' "$execs" | python3 -c '
import json, sys
for e in json.load(sys.stdin):
    if e.get("providerId") == "idp-auto-link":
        e["requirement"] = "REQUIRED"
        print(json.dumps(e))
        break
')
	[ -n "$body" ] && kc update "authentication/flows/$ENC/executions" -r returnit -b "$body" >/dev/null
	return 0
}

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
	# A new person is registered automatically from the Google account. Someone who already has an account with
	# the same (Google-verified) e-mail address is linked to it without any extra step: see the flow below.
	-s firstBrokerLoginFlowAlias="$FLOW"
	-s config.clientId="$GOOGLE_CLIENT_ID" -s config.clientSecret="$GOOGLE_CLIENT_SECRET"
	-s config.defaultScope="openid email profile"
	# Always show Google's account chooser, so a re-login (e.g. before deleting an account) needs a tap.
	-s config.prompt=select_account
)

ensure_flow

if kc get identity-provider/instances/google -r returnit >/dev/null 2>&1; then
	kc update identity-provider/instances/google -r returnit "${settings[@]}"
	echo "Google login updated."
else
	kc create identity-provider/instances -r returnit "${settings[@]}"
	echo "Google login enabled."
fi
echo "Redirect URI to register at Google: https://auth.${DOMAIN}/realms/returnit/broker/google/endpoint"
