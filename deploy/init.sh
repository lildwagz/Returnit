#!/usr/bin/env bash
# Creates .env.prod from the template with fresh random secrets. Run once; refuses to overwrite.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
if [ -e .env.prod ]; then
	echo ".env.prod already exists: not touching it" >&2
	exit 1
fi
command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 1; }
umask 077
: >.env.prod
while IFS= read -r line; do
	if [[ "$line" == *"=__GENERATE__" ]]; then
		line="${line%__GENERATE__}$(openssl rand -hex 24)"
	fi
	printf '%s\n' "$line" >>.env.prod
done <.env.prod.example
chmod 600 .env.prod
echo "Created deploy/.env.prod (readable only by you)."
echo "Next: edit DOMAIN, ACME_EMAIL, SMTP_* in it, then: ./up.sh up -d --build"
echo "IMPORTANT: copy BACKUP_PASSPHRASE to a password manager now. Without it backups cannot be restored."
