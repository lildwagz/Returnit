#!/usr/bin/env bash
# Rolls the production stack to one published image:  ./release.sh <image@sha256:digest> <40-hex git commit>
# The deploy job of CI calls it over SSH; authorized_keys forces this script, so the arguments arrive in
# SSH_ORIGINAL_COMMAND. By hand:  ./release.sh ghcr.io/lildwagz/returnit@sha256:<64 hex> <commit>
# Steps: check arguments, take a lock, fast-forward the checkout to the commit (migrations and the Caddyfile
# come from it), pull the image, `up -d` (the migrate service runs first), wait until web and worker are
# healthy, otherwise roll back to the previous image. Database migrations are NOT undone by a rollback.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

args="${SSH_ORIGINAL_COMMAND:-$*}"
read -r image sha extra <<<"$args"
if [[ -n "${extra:-}" || ! "$image" =~ ^ghcr\.io/lildwagz/returnit@sha256:[0-9a-f]{64}$ || ! "$sha" =~ ^[0-9a-f]{40}$ ]]; then
	echo "usage: release.sh ghcr.io/lildwagz/returnit@sha256:<64 hex> <40 hex commit>" >&2
	exit 2
fi

mkdir -p .release
exec 9>.release/lock
flock -n 9 || { echo "another release is running" >&2; exit 3; }

# shellcheck disable=SC1091
. ./lib.sh # loads .env.prod and .release/env (RETURNIT_IMAGE of the previous release), defines dc()
previous="${RETURNIT_IMAGE:-}" # empty on the first release: the rollback target is then the locally built image

set_image() {
	printf 'RETURNIT_IMAGE=%s\n' "$1" >.release/env.tmp && mv .release/env.tmp .release/env
	export RETURNIT_IMAGE="$1"
}

wait_healthy() {
	local _ s id ok
	for _ in $(seq 1 40); do
		ok=1
		for s in web worker; do
			id="$(dc ps -q "$s")"
			[ -n "$id" ] && [ "$(docker inspect -f '{{.State.Health.Status}}' "$id" 2>/dev/null || echo none)" = healthy ] || ok=0
		done
		[ "$ok" = 1 ] && return 0
		sleep 5
	done
	return 1
}

old_head="$(git rev-parse HEAD)"
echo "release: fetching $sha"
git fetch -q origin
git merge -q --ff-only "$sha"

echo "release: pulling $image"
docker pull -q "$image" >/dev/null

echo "release: starting"
set_image "$image"
if ! dc up -d --remove-orphans || ! wait_healthy; then
	echo "release: FAILED, rolling back to ${previous:-the locally built image}" >&2
	dc logs --tail 40 web worker >&2 || true
	if [ -n "$previous" ]; then set_image "$previous"; else rm -f .release/env; unset RETURNIT_IMAGE; fi
	dc up -d web worker || true
	exit 1
fi

# Caddy reads its file at start: a changed Caddyfile needs a recreate.
if ! git diff --quiet "$old_head" HEAD -- Caddyfile; then
	echo "release: Caddyfile changed, recreating caddy"
	dc up -d --force-recreate caddy
fi

echo "$(date -u +%FT%TZ) $sha $image" >>.release/history
echo "release: done ($sha)"
