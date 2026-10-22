#!/usr/bin/env bash
# Restore the databases from backups made by backup.sh.
#   ./restore.sh --app backups/returnit-<time>.dump.enc [--keycloak backups/keycloak-<time>.dump.enc] [--yes]
#
# This REPLACES the current data. Afterwards it erases again every account that was erased since the
# backup was made (taken from the live database if it is still readable, from the backup, and from all
# deletion-log-*.csv files in BACKUP_DIR), as the data retention policy requires.
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

app="" kc="" yes=0
while [ $# -gt 0 ]; do
	case "$1" in
	--app) app="$2"; shift 2 ;;
	--keycloak) kc="$2"; shift 2 ;;
	--yes) yes=1; shift ;;
	*) echo "unknown option $1" >&2; exit 2 ;;
	esac
done
[ -n "$app" ] && [ -f "$app" ] || { echo "usage: ./restore.sh --app <returnit dump> [--keycloak <dump>] [--yes]" >&2; exit 2; }
dir="${BACKUP_DIR:-./backups}"

if [ "$yes" -ne 1 ]; then
	echo "This will REPLACE the current database(s) with $app${kc:+ and $kc}."
	read -r -p "Type RESTORE to continue: " answer
	[ "$answer" = "RESTORE" ] || { echo "cancelled"; exit 1; }
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
psql_app() { dc exec -T postgres psql -U returnit -d returnit -v ON_ERROR_STOP=1 "$@"; }

dc up -d postgres >/dev/null
dc exec -T postgres sh -c 'until pg_isready -U returnit -d postgres >/dev/null 2>&1; do sleep 1; done'

# 1. Remember who was erased on the live system (it may be newer than any backup).
if psql_app -At -F, -c "SELECT user_id, requested_at, deleted_at FROM account_deletion_log" >"$work/live.csv" 2>/dev/null; then
	echo "kept $(wc -l <"$work/live.csv") erasure record(s) from the live database"
else
	: >"$work/live.csv"
	echo "live database not readable: relying on the backup and the saved deletion logs"
fi

# 2. Stop the programs that use the database.
dc stop web worker >/dev/null 2>&1 || true

# 3. Replace the app database.
dc exec -T postgres psql -U returnit -d postgres -v ON_ERROR_STOP=1 \
	-c "DROP DATABASE IF EXISTS returnit WITH (FORCE)" -c "CREATE DATABASE returnit OWNER returnit"
openssl enc -d -aes-256-cbc -pbkdf2 -pass env:BACKUP_PASSPHRASE <"$app" |
	dc exec -T postgres pg_restore -U returnit -d returnit --no-owner --exit-on-error
echo "restored the app database"

# 4. Optionally replace Keycloak's database.
if [ -n "$kc" ]; then
	dc stop keycloak >/dev/null 2>&1 || true
	dc exec -T postgres psql -U returnit -d postgres -v ON_ERROR_STOP=1 \
		-c "DROP DATABASE IF EXISTS keycloak WITH (FORCE)" -c "CREATE DATABASE keycloak OWNER keycloak"
	openssl enc -d -aes-256-cbc -pbkdf2 -pass env:BACKUP_PASSPHRASE <"$kc" |
		dc exec -T postgres pg_restore -U returnit -d keycloak --no-owner --role=keycloak --exit-on-error
	echo "restored the Keycloak database"
fi

# 5. Bring the schema up to date (the backup may be older than the current code).
dc run --rm migrate >/dev/null
echo "migrations applied"

# 6. Erase again everyone who was erased. Combine: live records, saved logs, and the restored table itself.
cat "$work/live.csv" >"$work/all.csv"
for f in "$dir"/deletion-log-*.csv; do [ -f "$f" ] && cat "$f" >>"$work/all.csv"; done
{
	echo "CREATE TABLE restore_erasure_stage (user_id uuid, requested_at timestamptz, deleted_at timestamptz);"
	echo "COPY restore_erasure_stage FROM STDIN WITH (FORMAT csv);"
	grep -E '^[0-9a-fA-F-]{36},' "$work/all.csv" || true
	echo '\.'
	cat <<'SQL'
INSERT INTO account_deletion_log (user_id, requested_at, deleted_at)
  SELECT DISTINCT ON (user_id) user_id, requested_at, deleted_at FROM restore_erasure_stage ORDER BY user_id, deleted_at
  ON CONFLICT (user_id) DO NOTHING;
-- Their identities at the identity provider must go as well.
INSERT INTO identity_deletions (subject)
  SELECT u.identity_subject FROM users u JOIN account_deletion_log l ON l.user_id = u.id
  WHERE u.identity_subject IS NOT NULL ON CONFLICT (subject) DO NOTHING;
-- Cascade removes their data; a trigger queues their receipt files for deletion.
DELETE FROM users WHERE id IN (SELECT user_id FROM account_deletion_log);
DROP TABLE restore_erasure_stage;
SQL
} | psql_app -q
n="$(psql_app -At -c "SELECT count(*) FROM account_deletion_log")"
echo "re-applied erasures ($n account(s) on record)"

# 7. Start everything again.
dc up -d
echo "Restore finished. Check: ./up.sh ps   and   the app's /health/ready"
