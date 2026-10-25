#!/usr/bin/env bash
# Publishes the Android app from this server and controls the "force update" (see Docs: SRS change FR-CR-04).
#
#   ./publish-apk.sh publish <version> <apk url or file> [--min <version>] [--message "text"] [--note "text"]...
#        Downloads (or copies) the APK, checks it, stores it as returnit-<version>.apk, makes returnit-latest.apk point
#        to it and updates the download page and latest.json. --min also raises the minimum version.
#   ./publish-apk.sh min <version>            Only raises (or lowers) the minimum version: older apps are locked until they update.
#   ./publish-apk.sh maintenance on|off ["message"]   Maintenance switch: every app call is refused (503) while it is on.
#   ./publish-apk.sh page [--note "text"]...   Rebuilds the download page (--note replaces its "What's new" list).
#   ./publish-apk.sh status                 Shows what the app is being told right now.
#
# The files live in ./updates (served at https://dl.<DOMAIN>/ by Caddy; the API reads latest.json, no restart needed).
# The version must be written the way the app reports it (the "version" in app.json, for example 1.4.0).
set -euo pipefail
# shellcheck disable=SC1091
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

DIR="./updates"
JSON="$DIR/latest.json"
KEEP=10
mkdir -p "$DIR"

usage() { sed -n '2,14p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }
valid_version() { [[ "$1" =~ ^[0-9]+(\.[0-9]+){1,3}$ ]]; }
die() { echo "error: $*" >&2; exit 1; }

# Edits latest.json atomically. Arguments are passed through the environment, never put on a command line.
edit_json() {
	python3 - <<'PY'
import json, os, sys
path = os.environ['JSON']
try:
    with open(path) as f:
        cfg = json.load(f)
except FileNotFoundError:
    cfg = {}
android = cfg.setdefault('android', {})
cfg.setdefault('ios', {})
cfg.setdefault('message', '')
cfg.setdefault('maintenance', False)
cfg.setdefault('maintenanceMessage', '')
op = os.environ['OP']
if op == 'publish':
    android.update({
        'latestVersion': os.environ['VERSION'],
        'downloadUrl': os.environ['URL'],
        'sha256': os.environ['SHA'],
        'sizeBytes': int(os.environ['SIZE']),
        'publishedAt': os.environ['NOW'],
    })
    if os.environ.get('MIN'):
        android['minVersion'] = os.environ['MIN']
    if os.environ.get('MESSAGE'):
        cfg['message'] = os.environ['MESSAGE']
    android['releaseNotes'] = [n for n in os.environ.get('NOTES', '').split(chr(10)) if n.strip()]
elif op == 'notes':
    android['releaseNotes'] = [n for n in os.environ.get('NOTES', '').split(chr(10)) if n.strip()]
elif op == 'min':
    android['minVersion'] = os.environ['MIN']
elif op == 'maintenance':
    cfg['maintenance'] = os.environ['STATE'] == 'on'
    if os.environ.get('MESSAGE'):
        cfg['maintenanceMessage'] = os.environ['MESSAGE']
android.setdefault('minVersion', '')
tmp = path + '.tmp'
with open(tmp, 'w') as f:
    json.dump(cfg, f, indent=2, ensure_ascii=False)
    f.write('\n')
os.replace(tmp, path)
os.chmod(path, 0o644)
PY
}

# The download page: plain HTML and CSS, no scripts. The look lives in download-page/index.template.html (with its
# logo and fonts in download-page/assets); this fills in the version, size, date, fingerprint and release notes.
write_page() {
	local here
	here="$(dirname "${BASH_SOURCE[0]}")/download-page"
	[ -f "$here/index.template.html" ] || die "download-page/index.template.html is missing"
	rm -rf "$DIR/assets"
	cp -r "$here/assets" "$DIR/assets"
	chmod -R a+rX "$DIR/assets"
	TEMPLATE="$here/index.template.html" python3 - <<'PY'
import html, json, os
cfg = json.load(open(os.environ['JSON']))
a = cfg['android']
size = a.get('sizeBytes', 0) / 1048576
notes = [n for n in a.get('releaseNotes', []) if n.strip()]
notes_html = ''
if notes:
    items = ''.join(f'<li>{html.escape(n)}</li>' for n in notes)
    notes_html = f'<section><div class="notes"><h2>What\'s new in {html.escape(a.get("latestVersion", "-"))}</h2><ul>{items}</ul></div></section>'
page = open(os.environ['TEMPLATE'], encoding='utf-8').read()
for key, value in {
    '{{VERSION}}': html.escape(a.get('latestVersion', '-')),
    '{{SIZE}}': f'{size:.0f}',
    '{{DATE}}': html.escape(a.get('publishedAt', '-')),
    '{{SHA}}': html.escape(a.get('sha256', '-')),
    '{{NOTES}}': notes_html,
}.items():
    page = page.replace(key, value)
tmp = os.path.join(os.environ['DIR'], 'index.html.tmp')
open(tmp, 'w', encoding='utf-8').write(page)
os.replace(tmp, os.path.join(os.environ['DIR'], 'index.html'))
os.chmod(os.path.join(os.environ['DIR'], 'index.html'), 0o644)
PY
}

cmd="${1:-help}"
case "$cmd" in
help | -h | --help) usage 0 ;;

status)
	if [ -f "$JSON" ]; then cat "$JSON"; else echo "nothing published yet ($JSON does not exist): the app is not restricted"; fi
	ls -1 "$DIR" 2>/dev/null | grep -E '\.apk$' || true
	;;

min)
	v="${2:-}"
	valid_version "$v" || die "give the minimum version, for example: ./publish-apk.sh min 1.2.0"
	latest=""
	[ -f "$JSON" ] && latest="$(python3 -c "import json;print(json.load(open('$JSON'))['android'].get('latestVersion',''))")"
	if [ -n "$latest" ]; then
		# A minimum above the newest published build would lock every user out with nothing to update to.
		python3 - "$v" "$latest" <<'PY' || die "the minimum $v is higher than the newest published build $latest: nobody could update. Publish a newer build first."
import sys
t = lambda s: tuple(int(x) for x in (s.split('.') + ['0'] * 4)[:4])
sys.exit(0 if t(sys.argv[1]) <= t(sys.argv[2]) else 1)
PY
	else
		die "no build has been published yet; publish one first so the locked app has somewhere to go"
	fi
	JSON="$JSON" OP=min MIN="$v" edit_json
	echo "Minimum version is now $v. Apps older than that are locked until they update."
	;;

page)
	# Rebuilds the download page from latest.json (after changing the template), optionally replacing the release notes.
	[ -f "$JSON" ] || die "nothing published yet"
	shift
	notes="" set_notes=""
	while [ $# -gt 0 ]; do
		case "$1" in
		--note) notes+="${2:-}"$'\n'; set_notes=1; shift 2 ;;
		*) die "unknown option $1" ;;
		esac
	done
	[ -z "$set_notes" ] || JSON="$JSON" OP=notes NOTES="$notes" edit_json
	JSON="$JSON" DIR="$DIR" write_page
	echo "Download page rebuilt: https://dl.${DOMAIN}/"
	;;

maintenance)
	state="${2:-}"
	[ "$state" = on ] || [ "$state" = off ] || die "use: ./publish-apk.sh maintenance on|off [\"message\"]"
	JSON="$JSON" OP=maintenance STATE="$state" MESSAGE="${3:-}" edit_json
	echo "Maintenance is $state."
	;;

publish)
	version="${2:-}"
	source="${3:-}"
	valid_version "$version" || die "version looks wrong (use digits and dots, e.g. 1.4.0)"
	[ -n "$source" ] || die "give the APK address or file"
	shift 3
	min="" message="" notes=""
	while [ $# -gt 0 ]; do
		case "$1" in
		--min) valid_version "${2:-}" || die "--min needs a version"; min="$2"; shift 2 ;;
		--message) message="${2:-}"; shift 2 ;;
		--note) notes+="${2:-}"$'\n'; shift 2 ;;
		*) die "unknown option $1" ;;
		esac
	done
	if [ -n "$min" ]; then
		python3 - "$min" "$version" <<'PY' || die "--min $min is higher than the version being published ($version): nobody could update"
import sys
t = lambda s: tuple(int(x) for x in (s.split('.') + ['0'] * 4)[:4])
sys.exit(0 if t(sys.argv[1]) <= t(sys.argv[2]) else 1)
PY
	fi

	tmp="$(mktemp "$DIR/.incoming.XXXXXX")"
	trap 'rm -f "$tmp"' EXIT
	case "$source" in
	http://* | https://*) curl -fsSL --max-filesize 209715200 --connect-timeout 20 --max-time 600 -o "$tmp" "$source" ;;
	*) [ -f "$source" ] || die "file not found: $source"; cp "$source" "$tmp" ;;
	esac
	size="$(stat -c %s "$tmp")"
	[ "$size" -gt 1048576 ] || die "the file is only $size bytes: this is not an APK"
	# An APK is a zip that holds AndroidManifest.xml.
	python3 - "$tmp" <<'PY' || die "the file is not a valid APK (no AndroidManifest.xml)"
import sys, zipfile
sys.exit(0 if 'AndroidManifest.xml' in zipfile.ZipFile(sys.argv[1]).namelist() else 1)
PY
	sha="$(sha256sum "$tmp" | cut -d' ' -f1)"
	target="returnit-$version.apk"
	if [ -e "$DIR/$target" ] && [ "$(sha256sum "$DIR/$target" | cut -d' ' -f1)" != "$sha" ]; then
		die "$target already exists with different content: a published version must not change. Use a new version number."
	fi
	chmod 644 "$tmp"
	mv "$tmp" "$DIR/$target"
	trap - EXIT
	printf '%s  %s\n' "$sha" "$target" >"$DIR/$target.sha256"
	ln -sfn "$target" "$DIR/returnit-latest.apk"

	JSON="$JSON" OP=publish VERSION="$version" URL="https://dl.${DOMAIN}/returnit-latest.apk" SHA="$sha" SIZE="$size" \
		NOW="$(date -u +%Y-%m-%d)" MIN="$min" MESSAGE="$message" NOTES="$notes" edit_json
	JSON="$JSON" DIR="$DIR" write_page

	# keep the newest $KEEP builds
	mapfile -t old < <(ls -1 "$DIR" | grep -E '^returnit-[0-9][0-9.]*\.apk$' | sort -V | head -n "-$KEEP" || true)
	for f in "${old[@]:-}"; do [ -n "$f" ] && rm -f "$DIR/$f" "$DIR/$f.sha256"; done

	echo "Published $version ($((size / 1048576)) MB, sha256 $sha)"
	echo "  download page: https://dl.${DOMAIN}/"
	echo "  the app is told: latest $version${min:+, minimum $min}"
	;;

*) usage 1 ;;
esac
