#!/usr/bin/env bash
# Nightly backup (run from cron, see README.md):
#   - app database and Keycloak database, compressed and encrypted with BACKUP_PASSPHRASE
#   - deletion-log-<time>.csv: ids of erased accounts (no personal data). Kept FOREVER, because after a
#     restore these accounts must be erased again (see restore.sh)
#   - a mirror of the receipt files (when MinIO runs on this server)
#   - optional upload of everything off-site with rclone
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

dir="${BACKUP_DIR:-./backups}"
keep="${BACKUP_KEEP_DAYS:-35}"
ts="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$dir"
umask 077

for db in returnit keycloak; do
	out="$dir/$db-$ts.dump.enc"
	tmp="$out.partial"
	dc exec -T postgres pg_dump -U returnit -Fc "$db" |
		openssl enc -aes-256-cbc -pbkdf2 -salt -pass env:BACKUP_PASSPHRASE >"$tmp"
	[ -s "$tmp" ] || { echo "backup of $db is empty: aborting" >&2; rm -f "$tmp"; exit 1; }
	mv "$tmp" "$out"
	echo "wrote $out"
done

dc exec -T postgres psql -U returnit -d returnit -At -F, \
	-c "SELECT user_id, requested_at, deleted_at FROM account_deletion_log ORDER BY deleted_at" >"$dir/deletion-log-$ts.csv"

if dc ps --services --status running | grep -qx minio; then
	mkdir -p "$dir/receipts"
	docker run --rm --network "$(dc ps -q postgres | head -1 | xargs docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{end}}')" \
		-e RCLONE_CONFIG_SRC_TYPE=s3 -e RCLONE_CONFIG_SRC_PROVIDER=Minio \
		-e RCLONE_CONFIG_SRC_ENDPOINT=http://minio:9000 \
		-e RCLONE_CONFIG_SRC_ACCESS_KEY_ID="$S3_ACCESS_KEY" -e RCLONE_CONFIG_SRC_SECRET_ACCESS_KEY="$S3_SECRET_KEY" \
		-v "$(cd "$dir" && pwd)/receipts:/dest" rclone/rclone:1.68 \
		sync "src:${S3_BUCKET:-returnit-receipts}" /dest --quiet
	echo "mirrored receipt files to $dir/receipts"
fi

# Old dumps go; deletion logs stay.
find "$dir" -maxdepth 1 -name '*.dump.enc' -mtime "+$keep" -delete

if [ -n "${BACKUP_RCLONE_DEST:-}" ]; then
	docker run --rm -v "$(cd "$dir" && pwd):/src:ro" -v "${BACKUP_RCLONE_CONF:?set BACKUP_RCLONE_CONF}:/config/rclone/rclone.conf:ro" \
		rclone/rclone:1.68 sync /src "$BACKUP_RCLONE_DEST" --quiet
	echo "uploaded to $BACKUP_RCLONE_DEST"
else
	echo "WARNING: no off-site copy configured (BACKUP_RCLONE_DEST). A lost server means lost data." >&2
fi
