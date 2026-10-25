#!/usr/bin/env bash
# Puts the ReturnIt login theme on the running realm and adds Arabic to its languages. The realm file only seeds a
# fresh Keycloak, so a server that is already running needs this once (safe to run again):
#   ./apply-login-theme.sh            use the ReturnIt theme
#   ./apply-login-theme.sh --reset    back to Keycloak's own look
# The theme files are mounted into the container by docker-compose.prod.yml (deploy/keycloak/themes/returnit); after
# changing them, restart Keycloak:  ./up.sh restart keycloak
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

kc() { dc exec -T keycloak /opt/keycloak/bin/kcadm.sh "$@"; }

kc config credentials --server http://localhost:8080 --realm master --user admin --password "$KEYCLOAK_ADMIN_PASSWORD" >/dev/null

if [ "${1:-}" = "--reset" ]; then
	kc update realms/returnit -s loginTheme=keycloak.v2
	echo "Login pages use Keycloak's default theme again."
	exit 0
fi

kc update realms/returnit -s loginTheme=returnit \
	-s internationalizationEnabled=true -s 'supportedLocales=["id","en","ar"]' -s defaultLocale=id
echo "Login pages now use the ReturnIt theme (Indonesian, English, Arabic)."
