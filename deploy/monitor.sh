#!/usr/bin/env bash
# Health check for cron (every 5 minutes, see README.md section 9). Checks the public endpoints, the containers,
# disk, memory, the certificate, the last backup and stuck background jobs. When something is wrong it sends ONE
# e-mail (ALERT_EMAIL, through the SMTP settings of .env.prod), repeats it every 6 hours while the problem lasts,
# and sends "recovered" when everything is fine again. It cannot report a dead server: also use an outside
# uptime check (UptimeRobot, Better Stack) on https://api.<domain>/health/ready.
#   ./monitor.sh            run the checks; prints problems (and mails them when ALERT_EMAIL is set)
#   ./monitor.sh --quiet    same, but prints nothing when all is well (for cron)
set -uo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

quiet=0
[ "${1:-}" = "--quiet" ] && quiet=1
state=".monitor-state"
problems=()
add() { problems+=("$1"); }

domain="${DOMAIN:?}"
disk_max="${MONITOR_DISK_MAX_PERCENT:-80}"
backup_max_h="${MONITOR_BACKUP_MAX_HOURS:-26}"
cert_min_days="${MONITOR_CERT_MIN_DAYS:-14}"

# 1. public endpoints
curl -fsS -m 15 "https://api.$domain/health/ready" 2>/dev/null | grep -q '"ready"' ||
	add "API is not ready: https://api.$domain/health/ready"
curl -fsS -m 15 "https://auth.$domain/realms/returnit/.well-known/openid-configuration" 2>/dev/null | grep -q '"issuer"' ||
	add "Login server does not answer: https://auth.$domain/realms/returnit"

# 2. containers
expected="caddy postgres redis keycloak web worker minio"
[ "$MALWARE_SCAN" = on ] && expected="$expected clamav"
status="$(dc ps --all --format '{{.Service}}|{{.State}}|{{.Health}}' 2>/dev/null)"
for s in $expected; do
	line="$(printf '%s\n' "$status" | grep "^$s|" | head -1)"
	if [ -z "$line" ]; then
		add "Service $s does not exist"
	else
		case "$line" in
		"$s|running|"*) case "$line" in *"|unhealthy") add "Service $s is unhealthy" ;; esac ;;
		*) add "Service $s is not running ($line)" ;;
		esac
	fi
done

# 3. disk and memory
used="$(df --output=pcent / | tail -1 | tr -dc '0-9')"
[ "${used:-0}" -ge "$disk_max" ] && add "Disk is ${used}% full (limit ${disk_max}%)"
avail="$(free -m | awk '/^Mem:/ {print $7}')"
[ "${avail:-9999}" -lt 300 ] && add "Only ${avail} MB of memory is available"

# 4. certificate
end="$(echo | timeout 15 openssl s_client -connect "api.$domain:443" -servername "api.$domain" 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)"
if [ -n "$end" ]; then
	days=$(( ($(date -d "$end" +%s) - $(date +%s)) / 86400 ))
	[ "$days" -lt "$cert_min_days" ] && add "HTTPS certificate expires in $days days"
else
	add "Could not read the HTTPS certificate of api.$domain"
fi

# 5. newest backup
dir="${BACKUP_DIR:-./backups}"
newest="$(find "$dir" -maxdepth 1 -name 'returnit-*.dump.enc' -printf '%T@\n' 2>/dev/null | sort -n | tail -1)"
if [ -z "$newest" ]; then
	add "No backup found in $dir"
else
	age_h=$(( ($(date +%s) - ${newest%.*}) / 3600 ))
	[ "$age_h" -gt "$backup_max_h" ] && add "Last backup is ${age_h} hours old (limit ${backup_max_h})"
fi

# 6. background jobs that are stuck (rows that should have been handled over an hour ago)
for t in account_emails identity_deletions object_deletions; do
	n="$(dc exec -T postgres psql -U returnit -d returnit -Atc "SELECT count(*) FROM $t WHERE next_attempt_at < now() - interval '1 hour'" 2>/dev/null | tr -dc '0-9')"
	if [ -z "$n" ]; then add "Cannot read table $t"; elif [ "$n" -gt 0 ]; then add "$n item(s) stuck in $t (see worker logs)"; fi
done

# --- report ---
now="$(date +%s)"
sig="$(printf '%s\n' "${problems[@]:-}" | sha256sum | cut -c1-16)"
prev_sig="" prev_time=0 prev_bad=0
[ -f "$state" ] && read -r prev_sig prev_time prev_bad <"$state"

mail() { # subject, body (stdin)
	[ -n "${ALERT_EMAIL:-}" ] || { echo "(ALERT_EMAIL is not set: no e-mail sent)" >&2; return 0; }
	python3 - "$1" <<'PY' || echo "could not send the alert e-mail" >&2
import os, smtplib, sys
from email.message import EmailMessage
m = EmailMessage()
m['From'] = 'ReturnIt monitor <%s>' % os.environ['SMTP_FROM']
m['To'] = os.environ['ALERT_EMAIL']
m['Subject'] = sys.argv[1]
m.set_content(sys.stdin.read())
tls = os.environ.get('SMTP_TLS', 'starttls')
port = int(os.environ.get('SMTP_PORT', '587'))
s = smtplib.SMTP_SSL(os.environ['SMTP_HOST'], port, timeout=30) if tls == 'tls' else smtplib.SMTP(os.environ['SMTP_HOST'], port, timeout=30)
if tls == 'starttls':
    s.starttls()
if os.environ.get('SMTP_USERNAME'):
    s.login(os.environ['SMTP_USERNAME'], os.environ['SMTP_PASSWORD'])
s.send_message(m)
s.quit()
PY
}

if [ "${#problems[@]}" -gt 0 ]; then
	body="$(printf '%s\n' "${problems[@]}")"
	echo "$(date -u +%FT%TZ) PROBLEM"; printf '  - %s\n' "${problems[@]}"
	if [ "$sig" != "$prev_sig" ] || [ $((now - prev_time)) -ge 21600 ]; then
		printf 'Server: %s\nTime: %s\n\n%s\n\nWhat to do: see the SOP (section "Alert -> action").\n' "$domain" "$(date -u +%FT%TZ)" "$body" |
			mail "[ReturnIt] PROBLEM: ${#problems[@]} issue(s) on $domain"
		prev_time="$now"
	fi
	echo "$sig $prev_time 1" >"$state"
	exit 1
fi

if [ "$prev_bad" = 1 ]; then
	echo "$(date -u +%FT%TZ) RECOVERED"
	printf 'Server: %s\nTime: %s\n\nAll checks pass again.\n' "$domain" "$(date -u +%FT%TZ)" | mail "[ReturnIt] RECOVERED on $domain"
elif [ "$quiet" -eq 0 ]; then
	echo "$(date -u +%FT%TZ) OK"
fi
echo "$sig 0 0" >"$state"
