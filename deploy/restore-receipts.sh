#!/usr/bin/env bash
# Put the receipt files back into MinIO from an archive made by backup.sh:
#   ./restore-receipts.sh backups/receipts-<time>.tar.enc [--yes]
# Files in the bucket that are not in the archive are removed (the bucket ends up exactly like the archive).
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

archive="${1:-}" yes="${2:-}"
[ -n "$archive" ] && [ -f "$archive" ] || { echo "usage: ./restore-receipts.sh <receipts-*.tar.enc> [--yes]" >&2; exit 2; }
if [ "$yes" != "--yes" ]; then
	read -r -p "This REPLACES every receipt file in the bucket with $archive. Type RESTORE to continue: " answer
	[ "$answer" = "RESTORE" ] || { echo "cancelled"; exit 1; }
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
openssl enc -d -aes-256-cbc -pbkdf2 -pass env:BACKUP_PASSPHRASE <"$archive" | tar -C "$work" -xf -
[ -d "$work/receipts" ] || { echo "the archive has no receipts/ folder" >&2; exit 1; }

dc up -d minio >/dev/null
net="$(dc ps -q minio | head -1 | xargs docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{end}}')"
docker run --rm --network "$net" \
	-e RCLONE_CONFIG_DST_TYPE=s3 -e RCLONE_CONFIG_DST_PROVIDER=Minio \
	-e RCLONE_CONFIG_DST_ENDPOINT=http://minio:9000 \
	-e RCLONE_CONFIG_DST_ACCESS_KEY_ID="$S3_ACCESS_KEY" -e RCLONE_CONFIG_DST_SECRET_ACCESS_KEY="$S3_SECRET_KEY" \
	-v "$work/receipts:/src:ro" rclone/rclone:1.68 \
	sync /src "dst:${S3_BUCKET:-returnit-receipts}" --quiet
echo "restored $(find "$work/receipts" -type f | wc -l) receipt file(s) into ${S3_BUCKET:-returnit-receipts}"
