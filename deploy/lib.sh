#!/usr/bin/env bash
# Shared by the other scripts: go to this folder, load .env.prod, define dc() = docker compose for production.
cd "$(dirname "${BASH_SOURCE[0]}")"
if [ ! -f .env.prod ]; then
	echo "deploy/.env.prod is missing: run ./init.sh first" >&2
	exit 1
fi
set -a
# shellcheck disable=SC1091
. ./.env.prod
set +a

# Keycloak's realm file wants these in its own spelling.
case "${SMTP_TLS:-starttls}" in
tls) export KC_SMTP_SSL=true KC_SMTP_STARTTLS=false ;;
starttls) export KC_SMTP_SSL=false KC_SMTP_STARTTLS=true ;;
*) export KC_SMTP_SSL=false KC_SMTP_STARTTLS=false ;;
esac
if [ -n "${SMTP_USERNAME:-}" ]; then export KC_SMTP_AUTH=true; else export KC_SMTP_AUTH=false; fi

# One switch for the virus scanner: MALWARE_SCAN=on|off (default on). The "scan" profile starts ClamAV.
MALWARE_SCAN="${MALWARE_SCAN:-on}"
case "$MALWARE_SCAN" in
on | off) ;;
*) echo "MALWARE_SCAN in .env.prod must be on or off" >&2; exit 1 ;;
esac
export MALWARE_SCAN
profiles="$(printf '%s' "${COMPOSE_PROFILES:-}" | tr ',' '\n' | { grep -vx 'scan' || true; } | { grep -v '^$' || true; } | paste -sd, -)"
if [ "$MALWARE_SCAN" = on ]; then profiles="${profiles:+$profiles,}scan"; fi
export COMPOSE_PROFILES="$profiles"

dc() { docker compose --env-file .env.prod -f docker-compose.prod.yml "$@"; }
