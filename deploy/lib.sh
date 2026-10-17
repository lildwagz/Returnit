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

dc() { docker compose --env-file .env.prod -f docker-compose.prod.yml "$@"; }
